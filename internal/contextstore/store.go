package contextstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

func Open(cfg Config) (*Store, error) {
	if !filepath.IsAbs(cfg.Root) || !filepath.IsAbs(cfg.Home) || filepath.Clean(cfg.Root) == filepath.Clean(cfg.Home) {
		return nil, &Error{Code: "invalid_input", Message: "store and home must be distinct absolute directories", NextAction: "Set --store to a dedicated context directory."}
	}
	cfg.Root, cfg.Home = filepath.Clean(cfg.Root), filepath.Clean(cfg.Home)
	// macOS temp/home paths can contain aliases such as /var -> /private/var.
	// Compare cwd against the same resolved home identity before deriving a key.
	home, err := realPath(cfg.Home)
	if err != nil {
		return nil, err
	}
	cfg.Home = home
	root, err := resolveParent(cfg.Root)
	if err != nil {
		return nil, err
	}
	if root == home {
		return nil, &Error{Code: "invalid_input", Message: "store resolves to home", NextAction: "Choose a dedicated context store directory."}
	}
	return &Store{cfg: cfg}, nil
}

// Resolve retains the baseline cwd binding and worktree-relative auto lookup.
// Canonical repo-root creation is deliberately unavailable in this skeleton;
// the context implementation owner replaces this legacy identity policy.
func (s *Store) Resolve(ctx context.Context, cwd string, opts ResolveOptions) (Binding, error) {
	if err := ctx.Err(); err != nil {
		return Binding{}, err
	}
	cwd, err := realPath(cwd)
	if err != nil {
		return Binding{}, err
	}
	b := Binding{Anchor: filepath.Join(cwd, ".llm"), State: "missing"}
	if opts.Scope != "" && opts.Scope != ScopeCwd && opts.Scope != ScopeRepo {
		return b, invalidScope()
	}
	info, err := os.Lstat(b.Anchor)
	if err == nil {
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(b.Anchor)
			if err != nil {
				return b, err
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(cwd, target)
			}
			if targetInfo, err := os.Stat(target); os.IsNotExist(err) {
				b.State = "dangling"
				return b, nil
			} else if err != nil {
				return b, err
			} else if !targetInfo.IsDir() {
				b.State = "conflict"
				return b, &Error{Code: "conflict", Message: ".llm target is not a directory", NextAction: "Preserve the target and choose a managed project directory."}
			}
			key, err := s.managedKey(target)
			if err != nil {
				b.State = "conflict"
				return b, err
			}
			b.Project = Project{Key: key, Root: filepath.Join(s.cfg.Root, key)}
			b.State = "managed"
			return b, nil
		case info.IsDir():
			b.State = "local"
		default:
			b.State = "conflict"
		}
	} else if !os.IsNotExist(err) {
		return b, err
	}
	if opts.ProjectKey != "" {
		root, err := s.projectPath(opts.ProjectKey, false)
		if err != nil {
			return b, err
		}
		b.Project = Project{Key: opts.ProjectKey, Root: root}
	} else if opts.Scope == ScopeRepo {
		key, err := s.resolveAutoLinkProject(ctx, cwd)
		if err != nil {
			return b, err
		}
		b.Project = Project{Key: key, Root: filepath.Join(s.cfg.Root, key)}
	} else if rel, err := filepath.Rel(s.cfg.Home, cwd); err == nil && validKey(rel) {
		b.Project = Project{Key: rel, Root: filepath.Join(s.cfg.Root, rel)}
	}
	return b, nil
}

// Ensure preserves explicit cwd initialization, including migration. Repo scope
// must not silently create a second store while canonical identity is unfinished.
func (s *Store) Ensure(ctx context.Context, cwd string, opts ResolveOptions) (LinkResult, error) {
	result := LinkResult{Added: []Change{}, Conflicts: []Change{}, Warnings: []string{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if opts.Scope == "" || opts.Scope == ScopeRepo {
		return result, unavailable("canonical automatic initialization")
	}
	if opts.Scope != ScopeCwd {
		return result, invalidScope()
	}
	anchorRoot, err := realPath(cwd)
	if err != nil {
		return result, err
	}
	storeRoot, err := resolveParent(s.cfg.Root)
	if err != nil {
		return result, err
	}
	if anchorRoot == s.cfg.Home || pathWithinOrEqual(anchorRoot, storeRoot) {
		return result, &Error{Code: "invalid_input", Message: "cannot initialize home or a directory inside the context store", NextAction: "Run init in the source project directory."}
	}
	b, err := s.Resolve(ctx, cwd, opts)
	result.Binding = b
	if err != nil {
		return result, err
	}
	if b.State == "managed" {
		if opts.ProjectKey != "" && opts.ProjectKey != b.Project.Key {
			return result, &Error{Code: "conflict", Message: ".llm is already bound to a different project", NextAction: "Use dotllm link <project> to change the binding."}
		}
		return result, nil
	}
	if b.State == "dangling" || b.State == "conflict" {
		return result, &Error{Code: "conflict", Message: "existing .llm binding needs manual resolution", NextAction: "Inspect .llm, then use dotllm link <project>."}
	}
	if b.Project.Key == "" {
		return result, &Error{Code: "invalid_input", Message: "project must be a dedicated directory under home", NextAction: "Use an explicit --project and --scope cwd."}
	}
	centralPath, err := s.projectPath(b.Project.Key, false)
	if err != nil {
		return result, err
	}
	if b.State == "local" {
		if _, err := os.Lstat(centralPath); !os.IsNotExist(err) {
			return result, &Error{Code: "conflict", Message: "central path already exists; local .llm preserved", NextAction: "Use dotllm link <project> to merge existing context."}
		}
		if err := os.MkdirAll(filepath.Dir(centralPath), 0755); err != nil {
			return result, err
		}
		if err := os.Rename(b.Anchor, centralPath); err != nil {
			return result, fmt.Errorf("migrate .llm: %w", err)
		}
	} else if err := os.MkdirAll(centralPath, 0755); err != nil {
		return result, err
	}
	if err := os.Symlink(centralPath, b.Anchor); err != nil {
		return result, fmt.Errorf("create symlink: %w", err)
	}
	result.Changed = true
	result.Binding.State = "managed"
	s.copyTemplates(centralPath, &result)
	if err := s.addToRegistry(b.Project.Key); err != nil {
		return result, fmt.Errorf("update registry (link already published): %w", err)
	}
	return result, nil
}

func unavailable(feature string) error {
	return &Error{Code: "unavailable", Message: feature + " is unavailable in the skeleton", NextAction: "Use existing explicit cwd init/link operations or wait for the implementation slice."}
}
func invalidScope() error {
	return &Error{Code: "invalid_input", Message: "scope must be repo or cwd", NextAction: "Use --scope repo or --scope cwd."}
}
