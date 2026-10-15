package contextstore

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Git owns local exclusions; Store owns when a context may be published. These
// private operations stay in the mutation lifecycle so Resolve/Projects remain
// read-only and every CLI adapter gets the same tracked-data protection.
func gitContextPaths(b Binding) ([]string, error) {
	if b.WorktreeRoot == "" || b.CommonGitDir == "" {
		return nil, nil
	}
	paths := []string{b.Anchor, filepath.Join(filepath.Dir(b.Anchor), ".dotllm.lock")}
	for i, path := range paths {
		rel, err := filepath.Rel(b.WorktreeRoot, path)
		if err != nil || !pathWithin(path, b.WorktreeRoot) || strings.ContainsAny(rel, "\r\n") {
			return nil, &Error{Code: "invalid_input", Message: "context path cannot be represented safely in Git exclusions: " + path, NextAction: "Choose an anchor inside the checkout with no line breaks in its path."}
		}
		paths[i] = filepath.ToSlash(rel)
	}
	return paths, nil
}

func checkTrackedContext(ctx context.Context, b Binding) error {
	paths, err := gitContextPaths(b)
	if err != nil || len(paths) == 0 {
		return err
	}
	args := []string{"ls-files", "--cached", "-z", "--"}
	for _, path := range paths {
		args = append(args, ":(top,literal)"+path)
	}
	tracked, err := gitBytes(ctx, b.WorktreeRoot, args...)
	if err != nil {
		return err
	}
	if len(tracked) != 0 {
		return &Error{Code: "conflict", Message: fmt.Sprintf("Git tracks context or lock content at %s; refusing to change it", b.Anchor), NextAction: "Preserve the tracked content and deliberately remove it from Git's index before using dotllm here; local exclusions cannot hide tracked files."}
	}
	return nil
}

// A Git-style exclusive lockfile serializes read/append/rename across different
// stores and linked worktrees sharing this metadata. We never flock then unlink
// the exclude inode (or the stable anchor locks). Existing bytes and permissions
// survive; readers see either complete version. A stale lock is an actionable
// busy error, never permission to steal another writer's lock.
func excludeGitContext(ctx context.Context, b Binding) error {
	paths, err := gitContextPaths(b)
	if err != nil || len(paths) == 0 {
		return err
	}
	root, err := os.OpenRoot(b.CommonGitDir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Mkdir("info", 0755); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := root.Lstat("info")
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return conflict("Git info must be a real directory: " + b.CommonGitDir)
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	retry := time.NewTicker(20 * time.Millisecond)
	defer retry.Stop()
	var lock *os.File
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		lock, err = root.OpenFile("info/exclude.lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0666)
		if err == nil {
			break
		}
		if !os.IsExist(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return &Error{Code: "busy", Message: "Git exclusion update is locked: " + filepath.Join(b.CommonGitDir, "info", "exclude.lock"), NextAction: "Wait for the Git metadata writer; inspect a leftover lock only after confirming that writer has stopped.", Retryable: true}
		case <-retry.C:
		}
	}
	defer lock.Close()
	published := false
	defer func() {
		// After rename the path belongs to the next writer, if any.
		if !published {
			_ = root.Remove("info/exclude.lock")
		}
	}()
	var original []byte
	info, err = root.Lstat("info/exclude")
	if err == nil {
		if !info.Mode().IsRegular() {
			return conflict("Git exclude must be a regular file: " + filepath.Join(b.CommonGitDir, "info", "exclude"))
		}
		original, err = root.ReadFile("info/exclude")
		if err != nil {
			return err
		}
		if err := lock.Chmod(info.Mode().Perm()); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	updated := bytes.Clone(original)
	for _, path := range paths {
		// A leading slash anchors to each worktree's root; no trailing slash,
		// because .llm is a symlink. Quote glob syntax and whitespace literally.
		pattern := "/" + strings.NewReplacer("\\", "\\\\", "*", "\\*", "?", "\\?", "[", "\\[", "]", "\\]", " ", "\\ ", "\t", "\\\t").Replace(path)
		found := false
		for _, line := range bytes.Split(updated, []byte("\n")) {
			if string(bytes.TrimSuffix(line, []byte("\r"))) == pattern {
				found = true
				break
			}
		}
		if found {
			continue
		}
		if len(updated) > 0 && updated[len(updated)-1] != '\n' {
			updated = append(updated, '\n')
		}
		updated = append(updated, pattern...)
		updated = append(updated, '\n')
	}
	if bytes.Equal(original, updated) {
		return nil
	}
	if _, err := lock.Write(updated); err != nil {
		return err
	}
	if err := lock.Sync(); err != nil {
		return err
	}
	if err := lock.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Rename("info/exclude.lock", "info/exclude"); err != nil {
		return err
	}
	published = true
	return nil
}
