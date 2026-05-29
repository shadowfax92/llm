package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestResolveAutoLinkProjectUsesCommonGitDirForWorktree(t *testing.T) {
	home := t.TempDir()
	base := filepath.Join(home, "code", "project")
	worktree := filepath.Join(base, ".worktrees", "feature")
	nested := filepath.Join(worktree, "packages", "cli")

	initGitRepo(t, base)
	runGit(t, base, "worktree", "add", worktree, "-b", "feature")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}

	project, err := resolveAutoLinkProject(nested, home)
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join("code", "project", "packages", "cli")
	if project != want {
		t.Fatalf("project = %q, want %q", project, want)
	}
}

func TestResolveAutoLinkProjectUsesCurrentRootForPrimaryCheckout(t *testing.T) {
	home := t.TempDir()
	base := filepath.Join(home, "code", "project")
	nested := filepath.Join(base, "packages", "cli")

	initGitRepo(t, base)
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}

	project, err := resolveAutoLinkProject(nested, home)
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join("code", "project", "packages", "cli")
	if project != want {
		t.Fatalf("project = %q, want %q", project, want)
	}
}

func TestResolveAutoLinkProjectRequiresGitRepo(t *testing.T) {
	home := t.TempDir()

	_, err := resolveAutoLinkProject(home, home)
	if err == nil {
		t.Fatal("expected error")
	}
}

func initGitRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, path, "init", "-q")
	runGit(t, path, "config", "user.email", "test@example.com")
	runGit(t, path, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(path, ".gitignore"), []byte(".worktrees/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, path, "add", ".")
	runGit(t, path, "commit", "-q", "-m", "init")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
