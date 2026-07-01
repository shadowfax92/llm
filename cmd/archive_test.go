package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	chtimes(t, oldDir, fresh)

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

func TestArchiveProjectMovesStaleEntriesAndKeepsFreshEntries(t *testing.T) {
	root := setTestLLMRoot(t)
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	cutoff := now.AddDate(0, 0, -30)
	old := now.AddDate(0, 0, -31)
	fresh := now.AddDate(0, 0, -1)
	project := filepath.Join("code", "project")
	projectDir := filepath.Join(root, project)

	writeFileAt(t, filepath.Join(projectDir, "old.md"), old)
	writeFileAt(t, filepath.Join(projectDir, "fresh.md"), fresh)
	oldDir := filepath.Join(projectDir, "old-dir")
	writeFileAt(t, filepath.Join(oldDir, "notes.md"), old)
	chtimes(t, oldDir, fresh)

	result, err := archiveProject(project, cutoff, now)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.moved) != 2 {
		t.Fatalf("moved = %d, want 2", len(result.moved))
	}
	assertMissing(t, filepath.Join(projectDir, "old.md"))
	assertExists(t, filepath.Join(projectDir, "archive", "old.md"))
	assertMissing(t, filepath.Join(projectDir, "old-dir"))
	assertExists(t, filepath.Join(projectDir, "archive", "old-dir", "notes.md"))
	assertExists(t, filepath.Join(projectDir, "fresh.md"))
}

func TestArchiveProjectUsesUniqueDestinationForCollisions(t *testing.T) {
	root := setTestLLMRoot(t)
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	cutoff := now.AddDate(0, 0, -30)
	old := now.AddDate(0, 0, -31)
	project := filepath.Join("code", "project")
	projectDir := filepath.Join(root, project)

	writeFileAt(t, filepath.Join(projectDir, "notes.md"), old)
	writeFileAt(t, filepath.Join(projectDir, "archive", "notes.md"), old)

	result, err := archiveProject(project, cutoff, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.moved) != 1 {
		t.Fatalf("moved = %d, want 1", len(result.moved))
	}
	if result.moved[0].destName == "notes.md" {
		t.Fatalf("destName = %q, want unique collision name", result.moved[0].destName)
	}
	if !strings.HasPrefix(result.moved[0].destName, "notes.md-archived-20260701-120000") {
		t.Fatalf("destName = %q, want archived timestamp suffix", result.moved[0].destName)
	}
	assertExists(t, filepath.Join(projectDir, "archive", "notes.md"))
	assertExists(t, filepath.Join(projectDir, "archive", result.moved[0].destName))
}

func TestArchiveProjectSummariesUseRegistryCounts(t *testing.T) {
	root := setTestLLMRoot(t)
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	cutoff := now.AddDate(0, 0, -30)
	old := now.AddDate(0, 0, -31)
	fresh := now.AddDate(0, 0, -1)

	projectOne := filepath.Join("code", "one")
	projectTwo := filepath.Join("code", "two")
	writeFileAt(t, filepath.Join(root, projectOne, "old.md"), old)
	writeFileAt(t, filepath.Join(root, projectOne, "fresh.md"), fresh)
	writeFileAt(t, filepath.Join(root, projectTwo, "fresh.md"), fresh)

	if err := saveRegistry([]string{projectOne, projectTwo, filepath.Join("code", "missing")}); err != nil {
		t.Fatal(err)
	}

	summaries, err := archiveProjectSummaries(cutoff)
	if err != nil {
		t.Fatal(err)
	}

	if len(summaries) != 1 {
		t.Fatalf("summaries = %#v, want one stale project", summaries)
	}
	if summaries[0].project != projectOne || summaries[0].count != 1 {
		t.Fatalf("summary = %#v, want %q count 1", summaries[0], projectOne)
	}
	assertExists(t, filepath.Join(root, projectOne, "old.md"))
}

func TestArchiveProjectRejectsPathsOutsideLLMRoot(t *testing.T) {
	root := setTestLLMRoot(t)
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	cutoff := now.AddDate(0, 0, -30)
	old := now.AddDate(0, 0, -31)
	outsideDir := filepath.Join(filepath.Dir(root), "outside")
	writeFileAt(t, filepath.Join(outsideDir, "old.md"), old)

	_, err := archiveProject(filepath.Join("..", "outside"), cutoff, now)
	if err == nil {
		t.Fatal("expected archiveProject to reject project outside llm root")
	}
	assertExists(t, filepath.Join(outsideDir, "old.md"))
	assertMissing(t, filepath.Join(outsideDir, "archive", "old.md"))
}

func TestArchiveProjectRejectsSymlinkedProjectOutsideLLMRoot(t *testing.T) {
	root := setTestLLMRoot(t)
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	cutoff := now.AddDate(0, 0, -30)
	old := now.AddDate(0, 0, -31)
	outsideDir := filepath.Join(filepath.Dir(root), "outside")
	writeFileAt(t, filepath.Join(outsideDir, "old.md"), old)
	if err := os.Symlink(outsideDir, filepath.Join(root, "evil")); err != nil {
		t.Fatal(err)
	}

	_, err := archiveProject("evil", cutoff, now)
	if err == nil {
		t.Fatal("expected archiveProject to reject symlinked project outside llm root")
	}
	assertExists(t, filepath.Join(outsideDir, "old.md"))
	assertMissing(t, filepath.Join(outsideDir, "archive", "old.md"))
}

func archiveCandidateNames(candidates []archiveCandidate) []string {
	names := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		names = append(names, candidate.name)
	}
	slices.Sort(names)
	return names
}

func setTestLLMRoot(t *testing.T) string {
	t.Helper()
	oldRoot := llmRoot
	oldHome := homeDir
	root := t.TempDir()
	llmRoot = root
	homeDir = filepath.Dir(root)
	t.Cleanup(func() {
		llmRoot = oldRoot
		homeDir = oldHome
	})
	return root
}

func assertExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be missing, got err %v", path, err)
	}
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
