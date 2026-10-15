package archive

import (
	"context"
	"dotllm/internal/contextstore"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// legacyArchive preserves the baseline algorithm and fixtures during transfer.
// It is intentionally private and unreachable from Manager or CLI commands:
// immediate moves cannot satisfy reviewed-plan/journal/restore guarantees.
// The archive implementation owner replaces it after transferring its cases.
type legacyArchive struct {
	root  string
	store *contextstore.Store
}
type archiveCandidate struct {
	name          string
	path          string
	newestModTime time.Time
}

type archiveSummary struct {
	project string
	count   int
}

type archiveMove struct {
	destName string
}

type archiveResult struct {
	project    string
	archiveDir string
	moved      []archiveMove
}

func archiveCandidates(projectDir string, cutoff time.Time) ([]archiveCandidate, error) {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil, err
	}

	var candidates []archiveCandidate
	for _, entry := range entries {
		if entry.Name() == "archive" && entry.IsDir() {
			continue
		}

		path := filepath.Join(projectDir, entry.Name())
		newest, err := newestContentModTime(path)
		if err != nil {
			return nil, err
		}
		if newest.Before(cutoff) {
			candidates = append(candidates, archiveCandidate{
				name:          entry.Name(),
				path:          path,
				newestModTime: newest,
			})
		}
	}
	slices.SortFunc(candidates, func(a, b archiveCandidate) int {
		return cmpString(a.name, b.name)
	})
	return candidates, nil
}

func (a *legacyArchive) archiveProjectSummaries(cutoff time.Time) ([]archiveSummary, error) {
	registered, err := a.store.Projects(context.Background())
	projects := []string{}
	for _, p := range registered {
		projects = append(projects, p.Key)
	}
	if err != nil {
		return nil, fmt.Errorf("load registry: %w", err)
	}

	var summaries []archiveSummary
	for _, project := range projects {
		projectDir, err := a.projectStorePath(project)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		candidates, err := archiveCandidates(projectDir, cutoff)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("scan %s: %w", project, err)
		}
		if len(candidates) == 0 {
			continue
		}
		summaries = append(summaries, archiveSummary{
			project: project,
			count:   len(candidates),
		})
	}
	return summaries, nil
}

// archiveProject moves stale top-level entries for one registered project into its archive folder.
func (a *legacyArchive) archiveProject(project string, cutoff, now time.Time) (archiveResult, error) {
	projectDir, err := a.projectStorePath(project)
	result := archiveResult{
		project:    project,
		archiveDir: filepath.Join(projectDir, "archive"),
	}
	if err != nil {
		return result, err
	}

	candidates, err := archiveCandidates(projectDir, cutoff)
	if err != nil {
		return result, err
	}
	if len(candidates) == 0 {
		return result, nil
	}
	candidates, err = stageArchiveFileCandidate(projectDir, candidates, now)
	if err != nil {
		return result, err
	}
	if err := os.MkdirAll(result.archiveDir, 0755); err != nil {
		return result, err
	}

	for _, candidate := range candidates {
		dest, err := uniqueArchivePath(result.archiveDir, candidate.name, now)
		if err != nil {
			return result, err
		}
		if err := os.Rename(candidate.path, dest); err != nil {
			return result, err
		}
		result.moved = append(result.moved, archiveMove{
			destName: filepath.Base(dest),
		})
	}
	return result, nil
}

func stageArchiveFileCandidate(projectDir string, candidates []archiveCandidate, now time.Time) ([]archiveCandidate, error) {
	for i := range candidates {
		if candidates[i].name != "archive" {
			continue
		}

		info, err := os.Lstat(candidates[i].path)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			continue
		}

		staged, err := uniqueProjectPath(projectDir, ".archive-archiving-"+now.Format("20060102-150405"))
		if err != nil {
			return nil, err
		}
		if err := os.Rename(candidates[i].path, staged); err != nil {
			return nil, err
		}
		candidates[i].path = staged
	}
	return candidates, nil
}

// projectStorePath rejects project keys that would escape the managed llm root.
func (a *legacyArchive) projectStorePath(project string) (string, error) {
	if filepath.IsAbs(project) {
		return "", fmt.Errorf("invalid project %q", project)
	}

	clean := filepath.Clean(project)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid project %q", project)
	}

	root, err := filepath.EvalSymlinks(a.root)
	if err != nil {
		return "", fmt.Errorf("resolve llm root: %w", err)
	}
	projectDir, err := filepath.EvalSymlinks(filepath.Join(a.root, clean))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, projectDir)
	if err != nil {
		return "", err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("project %q resolves outside llm root", project)
	}
	return projectDir, nil
}

func uniqueProjectPath(projectDir, name string) (string, error) {
	path := filepath.Join(projectDir, name)
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return path, nil
		}
		return "", err
	}

	for i := 2; ; i++ {
		candidate := filepath.Join(projectDir, fmt.Sprintf("%s-%d", name, i))
		if _, err := os.Lstat(candidate); err != nil {
			if os.IsNotExist(err) {
				return candidate, nil
			}
			return "", err
		}
	}
}

func uniqueArchivePath(archiveDir, name string, now time.Time) (string, error) {
	dest := filepath.Join(archiveDir, name)
	if _, err := os.Lstat(dest); err != nil {
		if os.IsNotExist(err) {
			return dest, nil
		}
		return "", err
	}

	stamp := now.Format("20060102-150405")
	for i := 1; ; i++ {
		suffix := fmt.Sprintf("-archived-%s", stamp)
		if i > 1 {
			suffix = fmt.Sprintf("%s-%d", suffix, i)
		}
		candidate := filepath.Join(archiveDir, name+suffix)
		if _, err := os.Lstat(candidate); err != nil {
			if os.IsNotExist(err) {
				return candidate, nil
			}
			return "", err
		}
	}
}

func newestContentModTime(path string) (time.Time, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return time.Time{}, err
	}
	if !info.IsDir() {
		return info.ModTime(), nil
	}

	var newest time.Time
	foundContent := false
	err = filepath.WalkDir(path, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		foundContent = true
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	if err == nil && !foundContent {
		newest = info.ModTime()
	}
	return newest, err
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
