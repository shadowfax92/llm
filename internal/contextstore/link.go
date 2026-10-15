package contextstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

func isAmbiguous(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == "identity_ambiguous"
}

// Link selects an exact existing project; automatic creation goes through Ensure.
// Repo scope is the default, while --scope cwd opts into a nested anchor.
func (s *Store) Link(ctx context.Context, cwd, key string, scope Scope) (LinkResult, error) {
	b, err := s.Resolve(ctx, cwd, ResolveOptions{Scope: scope})
	if err != nil && (!isAmbiguous(err) || !filepath.IsAbs(b.Anchor)) {
		return emptyResult(b), err
	}
	target, err := s.projectPath(key, true)
	if err != nil {
		return emptyResult(b), err
	}
	b.Project = Project{Key: key, Root: target}
	return s.mutate(ctx, cwd, ResolveOptions{Scope: scope}, b, false)
}

// mutate is the single publication lifecycle. Locks coordinate dotllm processes,
// not arbitrary editors: source/target/anchor identities are checked again at
// the commit seam. Failures preserve original data and name any partial copy.
func (s *Store) mutate(ctx context.Context, cwd string, opts ResolveOptions, initial Binding, ensure bool) (result LinkResult, retErr error) {
	result = emptyResult(initial)
	parent := filepath.Dir(initial.Anchor)
	storeRoot, err := resolveParent(s.cfg.Root)
	if err != nil {
		return result, err
	}
	if parent == s.cfg.Home || pathWithinOrEqual(parent, storeRoot) {
		return result, &Error{Code: "invalid_input", Message: "cannot bind home or a directory inside the context store", NextAction: "Run in the source project directory."}
	}
	prospective, err := resolveParent(initial.Project.Root)
	if err != nil {
		return result, err
	}
	if initial.State == "local" && (pathWithinOrEqual(prospective, initial.Anchor) || pathWithinOrEqual(initial.Anchor, prospective)) {
		return result, conflict("cannot merge overlapping local context and managed project")
	}
	// Resolve and validate before creating lock/store files, then repeat while
	// locked because another writer may have established the binding meanwhile.
	unlock, err := s.lock(ctx, initial.Anchor)
	if err != nil {
		return result, err
	}
	defer unlock()
	b, err := s.Resolve(ctx, cwd, opts)
	if err != nil && (ensure || !isAmbiguous(err)) {
		return result, err
	}
	if b.Anchor != initial.Anchor {
		return result, conflict("context anchor changed while waiting for its lock; retry")
	}
	if ensure && b.State == "managed" {
		result.Binding = b
		return result, s.addToRegistry(ctx, b.Project.Key)
	}
	// Automatic identity can change while waiting (e.g. the primary gets its
	// first explicit binding). Use the locked resolution, never the stale key.
	if !ensure || b.Project.Key == "" {
		b.Project = initial.Project
	}
	result.Binding = b
	target, err := s.projectPath(b.Project.Key, !ensure)
	if err != nil {
		return result, err
	}
	if ensure {
		if err := os.MkdirAll(target, 0755); err != nil {
			return result, err
		}
	}
	targetReal, err := realPath(target)
	if err != nil {
		return result, err
	}
	targetInfo, err := os.Stat(targetReal)
	if err != nil {
		return result, err
	}
	observed, err := observeAnchor(b.Anchor)
	if err != nil {
		return result, err
	}
	var source string
	switch b.State {
	case "missing":
		if _, err := os.Lstat(b.Anchor + ".bak"); !os.IsNotExist(err) {
			return result, conflict("missing .llm with possible interrupted migration at " + b.Anchor + ".bak")
		}
	case "managed":
		source, err = realPath(b.Anchor)
		if err != nil {
			return result, err
		}
		if sameResolvedPath(source, targetReal) {
			result.Binding.State = "managed"
			return result, s.addToRegistry(ctx, b.Project.Key)
		}
	case "local":
		source = b.Anchor
	case "dangling":
		if ensure {
			return result, conflict("dangling .llm requires an explicit link <project>")
		}
		result.Warnings = append(result.Warnings, "Previous .llm target was missing; no content was copied: "+b.Anchor)
	default:
		return result, conflict("refusing to replace an external or unsupported .llm at " + b.Anchor)
	}
	if source != "" && (pathWithinOrEqual(source, targetReal) || pathWithinOrEqual(targetReal, source)) {
		return result, conflict("cannot merge overlapping context projects")
	}
	projects, err := s.Projects(ctx)
	if err != nil {
		return result, err
	}
	for _, project := range projects {
		if project.Key == b.Project.Key {
			continue
		}
		resolved, err := resolveParent(project.Root)
		if err != nil {
			return result, err
		}
		if pathWithin(resolved, targetReal) {
			return result, conflict("target is an ancestor of registered project " + project.Key)
		}
	}
	if b.State == "local" {
		backup := b.Anchor + ".bak"
		if _, err := os.Lstat(backup); !os.IsNotExist(err) {
			return result, conflict(".llm.bak already exists or cannot be inspected: " + backup)
		}
		if err := observed.check(b.Anchor); err != nil {
			return result, err
		}
		if err := os.Rename(b.Anchor, backup); err != nil {
			return result, err
		}
		result.BackupPath = backup
		source = backup
		// Once moved, failure recovery must refer to the real backup, not an anchor
		// that is now absent. Publication still waits until the entire copy succeeds.
		observed, err = observeAnchor(b.Anchor)
		if err != nil {
			return result, err
		}
	}
	defer func() {
		if retErr != nil {
			retErr = fmt.Errorf("context operation incomplete (anchor %s; target %s; preserved source %s): %w", b.Anchor, target, source, retErr)
		}
	}()
	var sourceInfo os.FileInfo
	if source != "" {
		sourceInfo, err = os.Stat(source)
		if err != nil {
			return result, err
		}
		if b.State == "managed" {
			if _, err := s.managedKey(source); err != nil {
				return result, err
			}
		}
		added, conflicts, err := mergeDir(ctx, source, targetReal)
		for _, p := range added {
			result.Added = append(result.Added, Change{Path: filepath.Join(target, p), PreservedAt: filepath.Join(source, p), Reason: "copied"})
		}
		for _, p := range conflicts {
			result.Conflicts = append(result.Conflicts, Change{Path: filepath.Join(target, p), PreservedAt: filepath.Join(source, p), Reason: "existing destination or unsupported entry preserved"})
		}
		if err != nil {
			return result, err
		}
	}
	// Path/root commands use a compact PathData DTO. Preserve recovery details
	// in warnings as well as LinkResult so every command discloses migrations.
	if result.BackupPath != "" {
		result.Warnings = append(result.Warnings, "Original .llm preserved at "+result.BackupPath)
	}
	for _, entry := range result.Conflicts {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Conflict at %s; original preserved at %s", entry.Path, entry.PreservedAt))
	}
	if ensure {
		s.copyTemplates(ctx, targetReal, &result)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := observed.check(b.Anchor); err != nil {
		return result, err
	}
	if sourceInfo != nil {
		current, err := os.Stat(source)
		if err != nil {
			return result, err
		}
		if !os.SameFile(sourceInfo, current) {
			return result, conflict("source directory changed before publication")
		}
	}
	current, err := s.projectPath(b.Project.Key, true)
	if err != nil {
		return result, err
	}
	info, err := os.Stat(current)
	if err != nil {
		return result, err
	}
	if !os.SameFile(targetInfo, info) {
		return result, conflict("target directory changed before publication")
	}
	if err := publishLink(b.Anchor, target); err != nil {
		return result, err
	}
	result.Binding.State = "managed"
	result.Changed = true
	if err := s.addToRegistry(ctx, b.Project.Key); err != nil {
		return result, fmt.Errorf("link already published; registry update failed: %w", err)
	}
	return result, nil
}

// anchorObservation detects changes across the copy/async cancellation seam.
// Raw link text matters as well as inode identity, including dangling links.
type anchorObservation struct {
	info os.FileInfo
	link string
}

func observeAnchor(path string) (anchorObservation, error) {
	var o anchorObservation
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return o, nil
	}
	if err != nil {
		return o, err
	}
	o.info = info
	if info.Mode()&os.ModeSymlink != 0 {
		o.link, err = os.Readlink(path)
	}
	return o, err
}
func (o anchorObservation) check(path string) error {
	current, err := observeAnchor(path)
	if err != nil {
		return err
	}
	if o.info == nil && current.info == nil {
		return nil
	}
	if o.info == nil || current.info == nil || !os.SameFile(o.info, current.info) || o.link != current.link {
		return conflict(".llm changed before publication; retry after inspecting " + path)
	}
	return nil
}
func publishLink(anchor, target string) error {
	temp, err := os.MkdirTemp(filepath.Dir(anchor), ".dotllm-link-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	staged := filepath.Join(temp, "link")
	if err := os.Symlink(target, staged); err != nil {
		return err
	}
	return os.Rename(staged, anchor)
}

// mergeDir confines BOTH ends to opened roots. Symlinks/special entries stay in
// the source; exclusive creation preserves conflicts. A failed/cancelled file
// copy removes only its own incomplete output, leaving the source recoverable.
func mergeDir(ctx context.Context, src, dst string) (added, skipped []string, err error) {
	source, err := os.OpenRoot(src)
	if err != nil {
		return nil, nil, err
	}
	defer source.Close()
	target, err := os.OpenRoot(dst)
	if err != nil {
		return nil, nil, err
	}
	defer target.Close()
	err = fs.WalkDir(source.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if path == "." {
			return nil
		}
		existing, statErr := target.Lstat(path)
		if statErr == nil {
			if entry.IsDir() && existing.IsDir() {
				return nil
			}
			skipped = append(skipped, path)
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !os.IsNotExist(statErr) {
			return statErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return target.Mkdir(path, info.Mode().Perm()|0700)
		}
		if !info.Mode().IsRegular() {
			skipped = append(skipped, path)
			return nil
		}
		if err := copyFile(ctx, source, target, path, info.Mode().Perm()); err != nil {
			return err
		}
		added = append(added, path)
		return nil
	})
	return
}
func copyFile(ctx context.Context, source, target *os.Root, path string, mode os.FileMode) error {
	in, err := source.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return conflict("source changed to an unsupported file: " + path)
	}
	out, err := target.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, &contextReader{ctx: ctx, reader: in})
	if copyErr == nil {
		copyErr = out.Sync()
	}
	closeErr := out.Close()
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = target.Remove(path)
	}
	return copyErr
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func (s *Store) copyTemplates(ctx context.Context, dst string, result *LinkResult) {
	templates := filepath.Join(s.cfg.Root, "templates")
	info, err := os.Lstat(templates)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		result.Warnings = append(result.Warnings, err.Error())
		return
	}
	if !info.IsDir() {
		result.Warnings = append(result.Warnings, "Templates skipped: expected a real directory at "+templates)
		return
	}
	source, err := os.OpenRoot(templates)
	if err != nil {
		result.Warnings = append(result.Warnings, err.Error())
		return
	}
	defer source.Close()
	target, err := os.OpenRoot(dst)
	if err != nil {
		result.Warnings = append(result.Warnings, err.Error())
		return
	}
	defer target.Close()
	entries, err := os.ReadDir(templates)
	if err != nil {
		result.Warnings = append(result.Warnings, err.Error())
		return
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		if err := copyFile(ctx, source, target, entry.Name(), 0644); err != nil && !os.IsExist(err) {
			result.Warnings = append(result.Warnings, fmt.Sprintf("template %s: %v", entry.Name(), err))
		}
	}
}
