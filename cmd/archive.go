package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"time"
)

type archiveCandidate struct {
	name          string
	path          string
	newestModTime time.Time
}

// archiveCandidates returns stale top-level project entries using recursive mtime freshness.
func archiveCandidates(projectDir string, cutoff time.Time) ([]archiveCandidate, error) {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil, err
	}

	var candidates []archiveCandidate
	for _, entry := range entries {
		if entry.Name() == "archive" {
			continue
		}

		path := filepath.Join(projectDir, entry.Name())
		newest, err := newestModTime(path)
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

func newestModTime(path string) (time.Time, error) {
	var newest time.Time
	err := filepath.WalkDir(path, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
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
