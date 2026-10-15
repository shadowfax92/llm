package contextstore

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

func (s *Store) Projects(ctx context.Context) ([]Project, error) {
	projects := []Project{}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(filepath.Join(s.cfg.Root, ".projects")); err == nil && !info.Mode().IsRegular() {
		return nil, conflict("registry must be a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.cfg.Root, ".projects"), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if os.IsNotExist(err) {
		return projects, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := scanner.Text()
		if strings.TrimSpace(key) == "" || strings.HasPrefix(key, "#") {
			continue
		}
		root, err := s.projectPath(key, false)
		if err != nil {
			return nil, err
		}
		projects = append(projects, Project{Key: key, Root: root})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(projects, func(a, b Project) int { return strings.Compare(a.Key, b.Key) })
	return slices.CompactFunc(projects, func(a, b Project) bool { return a.Key == b.Key }), nil
}

// Registry updates run under the store lock; temp+sync+rename keeps readers on
// a complete sorted list. Reject symlink registries rather than replacing or
// reading a file outside the store.
func (s *Store) addToRegistry(ctx context.Context, key string) error {
	projects, err := s.Projects(ctx)
	if err != nil {
		return err
	}
	keys := []string{key}
	for _, p := range projects {
		keys = append(keys, p.Key)
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)
	if err := os.MkdirAll(s.cfg.Root, 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.cfg.Root, ".projects-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0644); err != nil {
		f.Close()
		return err
	}
	for _, key := range keys {
		if _, err := fmt.Fprintln(f, key); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(s.cfg.Root, ".projects"))
}
