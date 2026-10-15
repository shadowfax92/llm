package contextstore

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Link carries forward the preserved patch: copy non-conflicting managed files,
// keep the old target intact, then publish the replacement link. Full writer
// locking/rollback semantics remain the next context slice's responsibility.
func (s *Store) Link(ctx context.Context, cwd, key string, scope Scope) (LinkResult, error) {
	result := LinkResult{Added: []Change{}, Conflicts: []Change{}, Warnings: []string{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if scope != "" && scope != ScopeCwd {
		return result, unavailable("repo-scoped linking")
	}
	cwd, err := realPath(cwd)
	if err != nil {
		return result, err
	}
	target, err := s.projectPath(key, true)
	if err != nil {
		return result, err
	}
	anchor := filepath.Join(cwd, ".llm")
	result.Binding = Binding{Anchor: anchor, Project: Project{Key: key, Root: target}, State: "missing"}
	info, err := os.Lstat(anchor)
	var source string
	if err == nil {
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			source, err = os.Readlink(anchor)
			if err != nil {
				return result, err
			}
			if !filepath.IsAbs(source) {
				source = filepath.Join(cwd, source)
			}
			if sameResolvedPath(source, target) {
				result.Binding.State = "managed"
				return result, nil
			}
			if _, err := os.Stat(source); os.IsNotExist(err) {
				// Explicit relinking may repair a dangling link. There is no old
				// content to merge, but retain its location in the result diagnostics.
				result.Warnings = append(result.Warnings, fmt.Sprintf("Previous .llm target %s was missing; no content was copied.", source))
				source = ""
				break
			} else if err != nil {
				return result, err
			}
			if _, err := s.managedKey(source); err != nil {
				return result, err
			}
			srcReal, err := realPath(source)
			if err != nil {
				return result, err
			}
			dstReal, err := realPath(target)
			if err != nil {
				return result, err
			}
			if pathWithin(srcReal, dstReal) || pathWithin(dstReal, srcReal) {
				return result, &Error{Code: "conflict", Message: "cannot merge overlapping managed projects", NextAction: "Choose independent projects; original .llm remains in place."}
			}
			// Walk the validated directory, not an alias symlink: filepath.Walk
			// deliberately does not follow a symlink at the source root.
			source = srcReal
		case info.IsDir():
			srcReal, err := realPath(anchor)
			if err != nil {
				return result, err
			}
			dstReal, err := realPath(target)
			if err != nil {
				return result, err
			}
			if pathWithinOrEqual(srcReal, dstReal) || pathWithinOrEqual(dstReal, srcReal) {
				return result, &Error{Code: "conflict", Message: "cannot merge overlapping local context and managed project", NextAction: "Choose an independent project; local .llm remains in place."}
			}
			backup := anchor + ".bak"
			if _, err := os.Lstat(backup); !os.IsNotExist(err) {
				return result, &Error{Code: "conflict", Message: ".llm.bak already exists or cannot be inspected", NextAction: "Resolve the backup before linking; no data was replaced."}
			}
			if err := os.Rename(anchor, backup); err != nil {
				return result, err
			}
			source = backup
			result.BackupPath = backup
		default:
			return result, &Error{Code: "conflict", Message: ".llm is not a directory or symlink", NextAction: "Preserve and move .llm manually."}
		}
	} else if !os.IsNotExist(err) {
		return result, err
	}
	if source != "" {
		added, conflicts, err := mergeDir(source, target)
		for _, p := range added {
			result.Added = append(result.Added, Change{Path: filepath.Join(target, p), PreservedAt: filepath.Join(source, p), Reason: "copied"})
		}
		for _, p := range conflicts {
			result.Conflicts = append(result.Conflicts, Change{Path: filepath.Join(target, p), PreservedAt: filepath.Join(source, p), Reason: "existing destination or unsupported entry preserved"})
		}
		if err != nil {
			return result, fmt.Errorf("merge context (source preserved at %s): %w", source, err)
		}
	}
	// A sibling temporary directory reserves the link name without a shared
	// predictable path. Rename avoids an unlink gap for an existing managed link.
	temp, err := os.MkdirTemp(cwd, ".dotllm-link-*")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(temp)
	staged := filepath.Join(temp, "link")
	if err := os.Symlink(target, staged); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := os.Rename(staged, anchor); err != nil {
		return result, fmt.Errorf("publish .llm: %w", err)
	}
	result.Binding.State = "managed"
	result.Changed = true
	return result, nil
}

// mergeDir never follows source symlinks or overwrites destinations. Rooted
// destination operations also contain existing parent symlinks; otherwise a
// nested destination symlink could copy private context outside its project.
func mergeDir(src, dst string) (added, skipped []string, err error) {
	root, err := os.OpenRoot(dst)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	err = filepath.Walk(src, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		existing, statErr := root.Lstat(rel)
		if statErr == nil {
			if info.IsDir() && existing.IsDir() {
				return nil
			}
			skipped = append(skipped, rel)
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !os.IsNotExist(statErr) {
			return statErr
		}
		if info.IsDir() {
			return root.Mkdir(rel, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			skipped = append(skipped, rel)
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		added = append(added, rel)
		return nil
	})
	return
}

func (s *Store) copyTemplates(dst string, result *LinkResult) {
	entries, err := os.ReadDir(filepath.Join(s.cfg.Root, "templates"))
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		result.Warnings = append(result.Warnings, err.Error())
		return
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		// Reuse the no-clobber copy policy, reporting failures instead of silently
		// claiming a complete template migration as the legacy command did.
		root, err := os.OpenRoot(dst)
		if err != nil {
			result.Warnings = append(result.Warnings, err.Error())
			return
		}
		in, err := os.Open(filepath.Join(s.cfg.Root, "templates", entry.Name()))
		if err != nil {
			root.Close()
			result.Warnings = append(result.Warnings, err.Error())
			continue
		}
		out, err := root.OpenFile(entry.Name(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			_, err = io.Copy(out, in)
			if closeErr := out.Close(); err == nil {
				err = closeErr
			}
		}
		in.Close()
		root.Close()
		if err != nil && !os.IsExist(err) {
			result.Warnings = append(result.Warnings, err.Error())
		}
	}
}
