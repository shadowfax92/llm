package contextstore

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func (s *Store) Projects(ctx context.Context) ([]Project, error) {
	projects := []Project{}
	f, err := os.Open(filepath.Join(s.cfg.Root, ".projects"))
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
		key := strings.TrimSpace(scanner.Text())
		if key == "" || strings.HasPrefix(key, "#") {
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

// Registry replacement is atomic so readers never observe a truncated list.
// Cross-process locking belongs to the next context slice; no lock is claimed here.
func (s *Store) addToRegistry(key string) error {
	projects, err := s.Projects(context.Background())
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
	return os.Rename(f.Name(), filepath.Join(s.cfg.Root, ".projects"))
}
