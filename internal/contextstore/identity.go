package contextstore

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The baseline auto-link lookup preserves worktree-relative nested paths.
// It is retained for compatibility, not a claim of the final canonical policy.
func (s *Store) resolveAutoLinkProject(ctx context.Context, startDir string) (string, error) {
	home := s.cfg.Home
	startDir, err := realPath(startDir)
	if err != nil {
		return "", err
	}
	home, err = realPath(home)
	if err != nil {
		return "", err
	}

	worktreeRoot, err := gitOutput(ctx, startDir, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("resolve git worktree root: %w", err)
	}
	worktreeRoot, err = realPath(worktreeRoot)
	if err != nil {
		return "", err
	}
	commonGitDir, err := gitOutput(ctx, startDir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("resolve git common dir: %w", err)
	}
	commonGitDir, err = realPath(commonGitDir)
	if err != nil {
		return "", err
	}

	canonicalRoot := canonicalProjectRoot(worktreeRoot, commonGitDir)
	insideWorktree, err := filepath.Rel(worktreeRoot, startDir)
	if err != nil {
		return "", err
	}
	if insideWorktree == ".." || strings.HasPrefix(insideWorktree, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("%s is outside git worktree root %s", startDir, worktreeRoot)
	}

	projectPath := canonicalRoot
	if insideWorktree != "." {
		projectPath = filepath.Join(projectPath, insideWorktree)
	}
	if project, ok, err := s.linkedProject(projectPath); err != nil {
		return "", err
	} else if ok {
		return project, nil
	}

	project, err := filepath.Rel(home, projectPath)
	if err != nil {
		return "", err
	}
	if project == ".." || strings.HasPrefix(project, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("project must be under home directory (%s)", home)
	}
	return project, nil
}

// linkedProject returns the registry key from an existing .llm symlink.
func (s *Store) linkedProject(projectPath string) (string, bool, error) {
	llmPath := filepath.Join(projectPath, ".llm")
	info, err := os.Lstat(llmPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return "", false, nil
	}

	target, err := os.Readlink(llmPath)
	if err != nil {
		return "", false, err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(projectPath, target)
	}
	target, err = realPath(target)
	if err != nil {
		return "", false, fmt.Errorf("resolve %s: %w", llmPath, err)
	}
	key, err := s.managedKey(target)
	return key, err == nil, err
}

func realPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func sameResolvedPath(a, b string) bool {
	aReal, aErr := realPath(a)
	bReal, bErr := realPath(b)
	if aErr == nil && bErr == nil {
		return aReal == bReal
	}
	aAbs, aErr := filepath.Abs(a)
	bAbs, bErr := filepath.Abs(b)
	return aErr == nil && bErr == nil && filepath.Clean(aAbs) == filepath.Clean(bAbs)
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func pathWithinOrEqual(path, root string) bool {
	return path == root || pathWithin(path, root)
}

func canonicalProjectRoot(worktreeRoot, commonGitDir string) string {
	commonGitDir = filepath.Clean(commonGitDir)
	if filepath.Base(commonGitDir) == ".git" {
		return filepath.Dir(commonGitDir)
	}
	return filepath.Clean(worktreeRoot)
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s (%w)", strings.TrimSpace(string(out)), err)
	}
	result := strings.TrimSpace(string(out))
	if result == "" {
		return "", fmt.Errorf("empty git output")
	}
	return filepath.Clean(result), nil
}

func validKey(key string) bool {
	if filepath.IsAbs(key) || filepath.Clean(key) != key || key == "." || key == ".." || strings.HasPrefix(key, ".."+string(os.PathSeparator)) {
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
