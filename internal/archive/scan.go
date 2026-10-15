package archive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"dotllm/internal/contextstore"
)

// A candidate containing any protected node is skipped as a whole. Moving the
// containing directory would otherwise bypass exclusions for runs or symlinks.
type excludedError struct{ path, reason string }

func (e *excludedError) Error() string { return "excluded " + e.path + ": " + e.reason }

const maxTreeEntries = 100000

func scan(ctx context.Context, root *os.Root, path, identity string, projects []contextstore.Project) (Entry, error) {
	result := Entry{}
	// Pin the candidate's parent before Lstat/OpenFile so symlink ancestors are
	// rejected even for a top-level regular file and during recovery scans.
	logicalPath := path
	if containsProject(path, projects) {
		return result, &excludedError{path, "registered project root or descendant"}
	}
	parent, err := openDir(root, filepath.Dir(path), false)
	if err != nil {
		return result, err
	}
	defer parent.Close()
	root = parent
	path = filepath.Base(path)
	h := sha256.New()
	fmt.Fprintln(h, identity)
	count := 0
	var walk func(string, string) error
	walk = func(path, relative string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		if count > maxTreeEntries {
			return &excludedError{path, "tree exceeds 100000 entries"}
		}
		info, err := root.Lstat(path)
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return &excludedError{path, "symlink or special file"}
		}
		if relative != "." && (reserved(filepath.Base(path)) || containsProject(filepath.Join(logicalPath, relative), projects)) {
			return &excludedError{path, "protected descendant"}
		}
		if info.ModTime().After(result.NewestModTime) {
			result.NewestModTime = info.ModTime().UTC()
		}
		metadata := struct {
			Path  string
			Mode  uint32
			Size  int64
			Mtime int64
		}{relative, uint32(info.Mode()), info.Size(), info.ModTime().UnixNano()}
		if err := json.NewEncoder(h).Encode(metadata); err != nil {
			return err
		}
		if info.IsDir() {
			dir, err := openDir(root, path, false)
			if err != nil {
				return err
			}
			names, err := readNames(dir)
			dir.Close()
			if err != nil {
				return err
			}
			for _, name := range names {
				if err := walk(filepath.Join(path, name), filepath.Join(relative, name)); err != nil {
					return err
				}
			}
		} else {
			// Root contains resolution, O_NOFOLLOW prevents a raced final symlink, and
			// SameFile/metadata checks reject replacement or writes during the hash.
			f, err := root.OpenFile(path, os.O_RDONLY|noFollow, 0)
			if err != nil {
				return err
			}
			opened, err := f.Stat()
			if err != nil {
				f.Close()
				return err
			}
			if !os.SameFile(info, opened) {
				f.Close()
				return problem("stale_plan", "entry replaced while scanning: "+path)
			}
			content := sha256.New()
			buf := make([]byte, 64*1024)
			for {
				if err := ctx.Err(); err != nil {
					f.Close()
					return err
				}
				n, readErr := f.Read(buf)
				if n > 0 {
					content.Write(buf[:n])
				}
				if errors.Is(readErr, io.EOF) {
					break
				}
				if readErr != nil {
					f.Close()
					return readErr
				}
			}
			after, err := f.Stat()
			f.Close()
			if err != nil {
				return err
			}
			if !unchangedInfo(info, after) {
				return problem("stale_plan", "entry modified while hashing: "+path)
			}
			fmt.Fprintln(h, hex.EncodeToString(content.Sum(nil)))
			result.SizeBytes += info.Size()
		}
		after, err := root.Lstat(path)
		if err != nil {
			return err
		}
		if !unchangedInfo(info, after) {
			return problem("stale_plan", "tree changed while scanning: "+path)
		}
		return nil
	}
	if err := walk(path, "."); err != nil {
		return result, err
	}
	result.Fingerprint = hex.EncodeToString(h.Sum(nil))
	return result, nil
}
func unchangedInfo(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

// Open each directory relative to an already-open parent. Handles keep rename
// and journal operations anchored even if an unrelated writer changes a path.
// Lstat plus SameFile rejects links rather than relying only on containment.
func openDir(root *os.Root, path string, create bool) (*os.Root, error) {
	current, err := root.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	if path == "." || path == "" {
		return current, nil
	}
	if !filepath.IsLocal(path) {
		current.Close()
		return nil, problem("invalid_input", "directory escapes store")
	}
	for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		info, err := current.Lstat(part)
		if os.IsNotExist(err) && create {
			if err := current.Mkdir(part, 0700); err != nil && !os.IsExist(err) {
				current.Close()
				return nil, err
			}
			// Persist newly-created ancestry before a journal claims durable intent.
			if err := syncDir(current); err != nil {
				current.Close()
				return nil, err
			}
			info, err = current.Lstat(part)
		}
		if err != nil {
			current.Close()
			return nil, err
		}
		if !info.IsDir() {
			current.Close()
			return nil, problem("conflict", "directory is a symlink or non-directory: "+path)
		}
		next, err := current.OpenRoot(part)
		if err != nil {
			current.Close()
			return nil, err
		}
		actual, err := next.Stat(".")
		current.Close()
		if err != nil {
			next.Close()
			return nil, err
		}
		if !os.SameFile(info, actual) {
			next.Close()
			return nil, problem("conflict", "directory changed while opening: "+path)
		}
		current = next
	}
	return current, nil
}
func readNames(root *os.Root) ([]string, error) {
	f, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(maxTreeEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(names) > maxTreeEntries {
		return nil, problem("unavailable", "directory exceeds 100000 entries")
	}
	slices.Sort(names)
	return names, nil
}
func absent(root *os.Root, path string) error {
	parent, err := openDir(root, filepath.Dir(path), false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer parent.Close()
	_, err = parent.Lstat(filepath.Base(path))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return problem("conflict", "destination already exists: "+path)
}
func legacyWarnings(root *os.Root, key string) ([]string, error) {
	dir, err := openDir(root, filepath.Join(key, "archive"), false)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, problem("conflict", "archive path is not a real directory: "+key+"/archive")
	}
	defer dir.Close()
	names, err := readNames(dir)
	if err != nil {
		return nil, err
	}
	warnings := []string{}
	for _, name := range names {
		if validArchiveID(name) {
			if _, err := loadJournal(root, name); err == nil {
				continue
			}
		}
		warnings = append(warnings, "legacy archive "+key+"/archive/"+name+": missing recorded origin/provenance; restore unavailable")
	}
	return warnings, nil
}
