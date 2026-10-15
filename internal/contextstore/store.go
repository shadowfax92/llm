package contextstore

import (
	"context"
	"os"
	"path/filepath"
)

// Store is the context lifecycle module. Its small interface hides local Git
// discovery, filesystem containment and cooperating-writer coordination. Open,
// Resolve and Projects never create files; mutations lock before re-resolving.
type Store struct{ cfg Config }

func Open(cfg Config) (*Store, error) {
	if !filepath.IsAbs(cfg.Root) || !filepath.IsAbs(cfg.Home) {
		return nil, &Error{Code: "invalid_input", Message: "store and home must be absolute directories", NextAction: "Set --store to a dedicated context directory."}
	}
	cfg.Root, cfg.Home = filepath.Clean(cfg.Root), filepath.Clean(cfg.Home)
	home, err := realPath(cfg.Home)
	if err != nil {
		return nil, err
	}
	cfg.Home = home
	root, err := resolveParent(cfg.Root)
	if err != nil {
		return nil, err
	}
	if pathWithinOrEqual(home, root) {
		return nil, &Error{Code: "invalid_input", Message: "store must not be home or an ancestor of home", NextAction: "Choose a dedicated context store directory."}
	}
	return &Store{cfg: cfg}, nil
}

// Resolve honors the nearest existing binding before deriving a repo identity.
// Missing/broken/external bindings are diagnostic states, not permission to
// replace them. Only Ensure/Link may publish, after validating and locking.
func (s *Store) Resolve(ctx context.Context, cwd string, opts ResolveOptions) (Binding, error) {
	if err := ctx.Err(); err != nil {
		return Binding{}, err
	}
	if opts.Scope != "" && opts.Scope != ScopeCwd && opts.Scope != ScopeRepo {
		return Binding{}, invalidScope()
	}
	cwd, err := realPath(cwd)
	if err != nil {
		return Binding{}, err
	}
	info, err := discoverGit(ctx, cwd)
	if err != nil {
		return Binding{}, err
	}
	b := Binding{Anchor: filepath.Join(cwd, ".llm"), WorktreeRoot: info.worktree, CanonicalRoot: info.canonical, CommonGitDir: info.common, State: "missing"}
	if opts.Scope != ScopeCwd {
		limit := info.worktree
		if limit == "" {
			limit = s.cfg.Home
			if !pathWithinOrEqual(cwd, limit) {
				limit = string(filepath.Separator)
			}
		}
		for dir := cwd; ; dir = filepath.Dir(dir) {
			if err := ctx.Err(); err != nil {
				return b, err
			}
			if dir != s.cfg.Home {
				candidate := b
				candidate.Anchor = filepath.Join(dir, ".llm")
				if err := s.inspect(&candidate); err != nil {
					return candidate, err
				}
				if candidate.State == "local" {
					// A local directory is a source to preserve, not a durable project
					// identity. Continue canonical resolution before choosing its target.
					b = candidate
					break
				}
				if candidate.State != "missing" {
					return s.selectProject(candidate, opts.ProjectKey)
				}
			}
			if dir == limit || dir == filepath.Dir(dir) {
				break
			}
		}
		if info.worktree != "" && b.State == "missing" {
			b.Anchor = filepath.Join(info.worktree, ".llm")
		}
	} else {
		if err := s.inspect(&b); err != nil {
			return b, err
		}
		if b.State != "missing" {
			return s.selectProject(b, opts.ProjectKey)
		}
	}
	if opts.ProjectKey != "" {
		// A selector may rescue an ambiguous Git layout only by naming an existing
		// target. Creating a new project still needs validated identity or cwd scope.
		if opts.Scope != ScopeCwd && info.common != "" && !info.canonicalValid {
			if _, err := s.projectPath(opts.ProjectKey, true); err != nil {
				return b, err
			}
		}
		return s.selectProject(b, opts.ProjectKey)
	}
	identityRoot := filepath.Dir(b.Anchor)
	if opts.Scope != ScopeCwd && info.worktree != "" {
		// A canonical nested binding is a legacy identity, even if this worktree has
		// no corresponding .llm. New links still belong at this worktree's repo root.
		if info.canonicalValid {
			rel, _ := filepath.Rel(info.worktree, cwd)
			for dir := filepath.Join(info.canonical, rel); ; dir = filepath.Dir(dir) {
				if err := ctx.Err(); err != nil {
					return b, err
				}
				candidate := b
				candidate.Anchor = filepath.Join(dir, ".llm")
				if err := s.inspect(&candidate); err != nil {
					return b, err
				}
				if candidate.State == "managed" {
					b.Project = candidate.Project
					return b, nil
				}
				if candidate.State != "missing" && candidate.Anchor != b.Anchor {
					return b, conflict("canonical .llm needs resolution at " + candidate.Anchor)
				}
				if dir == info.canonical {
					break
				}
			}
		} else {
			return b, ambiguousIdentity()
		}
		identityRoot = info.canonical
		if b.State == "local" {
			rel, _ := filepath.Rel(info.worktree, filepath.Dir(b.Anchor))
			identityRoot = filepath.Join(info.canonical, rel)
		}
	}
	if rel, err := filepath.Rel(s.cfg.Home, identityRoot); err == nil && validKey(rel) {
		root, err := s.projectPath(rel, false)
		if err != nil {
			return b, err
		}
		b.Project = Project{Key: rel, Root: root}
	}
	return b, nil
}

func (s *Store) inspect(b *Binding) error {
	b.State = "missing"
	b.Project = Project{}
	info, err := os.Lstat(b.Anchor)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(b.Anchor)
		if err != nil {
			return err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(b.Anchor), target)
		}
		targetInfo, err := os.Stat(target)
		if os.IsNotExist(err) {
			root, e := resolveParent(s.cfg.Root)
			resolved, targetErr := resolveParent(target)
			rel, relErr := filepath.Rel(root, resolved)
			if e != nil || targetErr != nil || relErr != nil || !validKey(rel) {
				b.State = "external"
			} else {
				b.State = "dangling"
			}
			return nil
		}
		if err != nil {
			return err
		}
		if !targetInfo.IsDir() {
			b.State = "conflict"
			return nil
		}
		key, err := s.managedKey(target)
		if err != nil {
			b.State = "external"
			return nil
		}
		b.Project = Project{Key: key, Root: filepath.Join(s.cfg.Root, key)}
		b.State = "managed"
	case info.IsDir():
		b.State = "local"
	default:
		b.State = "conflict"
	}
	return nil
}

func (s *Store) selectProject(b Binding, key string) (Binding, error) {
	if key == "" {
		return b, nil
	}
	root, err := s.projectPath(key, false)
	if err != nil {
		return b, err
	}
	if b.State == "managed" && !sameResolvedPath(root, b.Project.Root) {
		return b, conflict(".llm is already bound to a different project; use dotllm link <project>")
	}
	b.Project = Project{Key: key, Root: root}
	return b, nil
}

// Ensure creates at repo scope by default; explicit cwd scope is the opt-in for
// non-Git/nested projects. Existing nested bindings keep their original identity.
func (s *Store) Ensure(ctx context.Context, cwd string, opts ResolveOptions) (LinkResult, error) {
	b, err := s.Resolve(ctx, cwd, opts)
	if err != nil {
		return emptyResult(b), err
	}
	if (b.State == "missing" || b.State == "local") && opts.Scope != ScopeCwd && b.WorktreeRoot == "" {
		if b.CommonGitDir == "" || opts.ProjectKey == "" {
			return emptyResult(b), ambiguousIdentity()
		}
		if _, err := s.projectPath(opts.ProjectKey, true); err != nil {
			return emptyResult(b), err
		}
	}
	if b.Project.Key == "" {
		// Real directories need a derived identity too; inspection deliberately does
		// not interpret their contents as a managed project before migration.
		identity := filepath.Dir(b.Anchor)
		if opts.Scope != ScopeCwd && b.CanonicalRoot != "" {
			identity = b.CanonicalRoot
		}
		rel, e := filepath.Rel(s.cfg.Home, identity)
		if e != nil || !validKey(rel) {
			return emptyResult(b), &Error{Code: "selector_required", Message: "cannot derive a project key", NextAction: "Use init --project <key> (and --scope cwd outside Git)."}
		}
		root, e := s.projectPath(rel, false)
		if e != nil {
			return emptyResult(b), e
		}
		b.Project = Project{Key: rel, Root: root}
	}
	if b.State != "managed" && b.State != "local" && b.State != "missing" {
		return emptyResult(b), conflict("existing .llm binding needs resolution at " + b.Anchor)
	}
	return s.mutate(ctx, cwd, opts, b, true)
}

func emptyResult(b Binding) LinkResult {
	return LinkResult{Binding: b, Added: []Change{}, Conflicts: []Change{}, Warnings: []string{}}
}
func invalidScope() error {
	return &Error{Code: "invalid_input", Message: "scope must be repo or cwd", NextAction: "Use --scope repo or --scope cwd."}
}
func conflict(message string) error {
	return &Error{Code: "conflict", Message: message, NextAction: "Inspect .llm and any .llm.bak; preserve their contents before retrying."}
}
func ambiguousIdentity() error {
	return &Error{Code: "identity_ambiguous", Message: "no valid canonical Git checkout or existing binding", NextAction: "Use dotllm link <project>, or init --scope cwd --project <key> for an explicit directory."}
}
