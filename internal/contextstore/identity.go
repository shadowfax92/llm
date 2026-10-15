package contextstore

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitIdentity uses Git's own worktree records, including separate git dirs.
// A missing/bare primary is retained as diagnostic identity, but never used to
// invent a new store key. An existing durable binding can still be reused.
type gitIdentity struct {
	worktree, canonical, common string
	canonicalValid              bool
}

func discoverGit(ctx context.Context, cwd string) (id gitIdentity, err error) {
	defer func() {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
	}()
	inside, err := gitOutput(ctx, cwd, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		if ctx.Err() != nil {
			return id, ctx.Err()
		}
		return id, nil
	}
	common, err := gitOutput(ctx, cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return id, err
	}
	id.common, err = realPath(common)
	if err != nil {
		return id, err
	}
	if inside != "true" {
		return id, nil
	}
	worktree, err := gitOutput(ctx, cwd, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err != nil {
		return id, err
	}
	id.worktree, err = realPath(worktree)
	if err != nil {
		return id, err
	}
	// core.worktree can point elsewhere. Never walk ancestors or project a
	// relative path until Git's reported root actually contains this cwd.
	if !pathWithinOrEqual(cwd, id.worktree) {
		return id, ambiguousIdentity()
	}
	records, err := gitBytes(ctx, cwd, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return id, err
	}
	// With --separate-git-dir and no core.worktree, porcelain may list the
	// metadata directory as the main worktree. A primary invocation itself is
	// authoritative when its per-worktree git dir equals the common directory.
	gitDir, e := gitOutput(ctx, cwd, "rev-parse", "--absolute-git-dir")
	if e != nil {
		return id, e
	}
	if sameResolvedPath(gitDir, id.common) {
		id.canonical = id.worktree
		id.canonicalValid = true
		return id, nil
	}
	fields := strings.Split(string(records), "\x00")
	bare := false
	for _, field := range fields {
		if field == "" {
			break
		}
		if strings.HasPrefix(field, "worktree ") {
			id.canonical = strings.TrimPrefix(field, "worktree ")
		}
		if field == "bare" {
			bare = true
		}
	}
	if id.canonical == "" || bare {
		return id, nil
	}
	// Separate metadata directories may need core.worktree to identify the
	// primary; this is Git configuration, not a guessed common-dir parent.
	if configured, e := gitOutput(ctx, cwd, "config", "--path", "--get", "core.worktree"); e == nil {
		if !filepath.IsAbs(configured) {
			configured = filepath.Join(id.common, configured)
		}
		id.canonical = configured
	}
	canonical, e := realPath(id.canonical)
	if e != nil {
		return id, nil
	}
	// The record may be stale after a move or replacement. Verify both the root
	// and common directory before treating another path as this repo's primary.
	top, e := gitOutput(ctx, canonical, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if e != nil {
		return id, nil
	}
	common, e = gitOutput(ctx, canonical, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if e != nil {
		return id, nil
	}
	if sameResolvedPath(top, canonical) && sameResolvedPath(common, id.common) {
		id.canonical = canonical
		id.canonicalValid = true
	}
	return id, ctx.Err()
}

func realPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", &Error{Code: "invalid_input", Message: "context paths must be absolute", NextAction: "Pass an absolute working directory."}
	}
	return filepath.EvalSymlinks(filepath.Clean(path))
}

func sameResolvedPath(a, b string) bool {
	aReal, aErr := realPath(a)
	bReal, bErr := realPath(b)
	if aErr == nil && bErr == nil {
		return aReal == bReal
	}
	return filepath.IsAbs(a) && filepath.IsAbs(b) && filepath.Clean(a) == filepath.Clean(b)
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func pathWithinOrEqual(path, root string) bool {
	return path == root || pathWithin(path, root)
}

// gitCommand shares the environment policy with NUL-delimited stdin queries
// used by local exclusion management; neither path may redirect Git identity.
func gitCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	// Git environment overrides can redirect -C to a different repository. Only
	// cwd and Git's on-disk records are allowed to establish context identity.
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	return cmd
}

func gitBytes(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := gitCommand(ctx, dir, args...)
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}
func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := gitBytes(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	// Remove only Git's record terminator: whitespace is legal in path names.
	result := strings.TrimSuffix(string(out), "\n")
	if result == "" {
		return "", fmt.Errorf("empty git output")
	}
	return result, nil
}

func validKey(key string) bool {
	if strings.ContainsAny(key, "\r\n\x00") || filepath.IsAbs(key) || filepath.Clean(key) != key || key == "." || key == ".." || strings.HasPrefix(key, ".."+string(os.PathSeparator)) {
		return false
	}
	// Legacy keys mirror real home paths, including .config and .worktrees.
	// Reserve store-owned roots, not ordinary names inside source project paths.
	first := strings.Split(key, string(os.PathSeparator))[0]
	switch first {
	case "", ".projects", ".dotllm", "templates", "runs", "archive":
		return false
	}
	return true
}
func (s *Store) managedKey(path string) (string, error) {
	root, err := realPath(s.cfg.Root)
	if err != nil {
		return "", err
	}
	target, err := realPath(path)
	if err != nil {
		return "", err
	}
	key, err := filepath.Rel(root, target)
	if err != nil || !validKey(key) {
		return "", &Error{Code: "conflict", Message: ".llm target is not a managed project", NextAction: "Preserve its contents and choose a project strictly inside the store."}
	}
	return key, nil
}

// Resolve the nearest existing parent before creation so a missing leaf cannot
// conceal an intermediate symlink outside the configured store.
func (s *Store) projectPath(key string, mustExist bool) (string, error) {
	if !validKey(key) {
		return "", &Error{Code: "invalid_input", Message: fmt.Sprintf("invalid project %q", key), NextAction: "Use a relative managed project key without traversal or reserved paths."}
	}
	root, err := resolveParent(s.cfg.Root)
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.cfg.Root, key)
	target, err := resolveParent(path)
	if err != nil {
		return "", err
	}
	if !pathWithin(target, root) {
		return "", &Error{Code: "conflict", Message: "project resolves outside the managed store", NextAction: "Inspect store symlinks before retrying."}
	}
	if mustExist {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			return "", &Error{Code: "missing_project", Message: fmt.Sprintf("project %q not found", key), NextAction: "Run dotllm list or initialize the source project."}
		}
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return "", fmt.Errorf("project %q is not a directory", key)
		}
	}
	return path, nil
}
func resolveParent(path string) (string, error) {
	result, err := filepath.EvalSymlinks(path)
	if err == nil {
		return result, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	// A dangling link is not an absent directory and must never be traversed.
	if info, e := os.Lstat(path); e == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("dangling symlink at %s", path)
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", err
	}
	resolved, err := resolveParent(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(path)), nil
}
