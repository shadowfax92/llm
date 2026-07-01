package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestArchiveCandidatesUsesRecursiveNewestModTime(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	cutoff := now.AddDate(0, 0, -30)
	old := now.AddDate(0, 0, -31)
	fresh := now.AddDate(0, 0, -1)
	projectDir := t.TempDir()

	writeFileAt(t, filepath.Join(projectDir, "old.md"), old)

	oldDir := filepath.Join(projectDir, "old-dir")
	writeFileAt(t, filepath.Join(oldDir, "notes.md"), old)
	chtimes(t, oldDir, old)

	activeDir := filepath.Join(projectDir, "active-dir")
	writeFileAt(t, filepath.Join(activeDir, "notes.md"), fresh)
	chtimes(t, activeDir, old)

	archiveDir := filepath.Join(projectDir, "archive")
	writeFileAt(t, filepath.Join(archiveDir, "archived.md"), old)
	chtimes(t, archiveDir, old)

	candidates, err := archiveCandidates(projectDir, cutoff)
	if err != nil {
		t.Fatal(err)
	}

	names := archiveCandidateNames(candidates)
	want := []string{"old-dir", "old.md"}
	if !slices.Equal(names, want) {
		t.Fatalf("candidates = %v, want %v", names, want)
	}
}

func archiveCandidateNames(candidates []archiveCandidate) []string {
	names := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		names = append(names, candidate.name)
	}
	slices.Sort(names)
	return names
}

func writeFileAt(t *testing.T, path string, modTime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	chtimes(t, path, modTime)
}

func chtimes(t *testing.T, path string, modTime time.Time) {
	t.Helper()
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
}
