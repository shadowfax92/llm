package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestResolveAutoLinkProjectUsesCanonicalCheckoutLink(t *testing.T) {
	home := t.TempDir()
	base := filepath.Join(home, "code", "browseros-project", "grove-ref", "main-3")
	worktree := filepath.Join(home, "worktrees", "m3", "feature")
	nested := filepath.Join(worktree, "packages", "browseros-agent")
	linkedProject := filepath.Join(home, "llm", "code", "browseros-project", "grove-ref", "browseros-main", "packages", "browseros-agent")
	baseNested := filepath.Join(base, "packages", "browseros-agent")

	initGitRepo(t, base)
	runGit(t, base, "worktree", "add", worktree, "-b", "feature")
	for _, dir := range []string{nested, linkedProject, baseNested} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(linkedProject, filepath.Join(baseNested, ".llm")); err != nil {
		t.Fatal(err)
	}

	project, err := resolveAutoLinkProject(nested, home)
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join("code", "browseros-project", "grove-ref", "browseros-main", "packages", "browseros-agent")
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

func TestLinkRunMergesExistingSymlinkTargetBeforeRelinking(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "code", "checkout")
	oldProject := filepath.Join("code", "old")
	newProject := filepath.Join("code", "new")
	oldTarget := filepath.Join(home, "llm", oldProject)
	newTarget := filepath.Join(home, "llm", newProject)

	for _, dir := range []string{cwd, oldTarget, newTarget} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(oldTarget, "old-only.md"), "old\n")
	writeTestFile(t, filepath.Join(oldTarget, "same.md"), "old same\n")
	writeTestFile(t, filepath.Join(newTarget, "same.md"), "new same\n")
	if err := os.Symlink(oldTarget, filepath.Join(cwd, ".llm")); err != nil {
		t.Fatal(err)
	}

	restore := useTestHome(t, home, cwd)
	defer restore()

	if err := linkRun(linkCmd, []string{newProject}); err != nil {
		t.Fatal(err)
	}

	target, err := os.Readlink(filepath.Join(cwd, ".llm"))
	if err != nil {
		t.Fatal(err)
	}
	if target != newTarget {
		t.Fatalf(".llm target = %q, want %q", target, newTarget)
	}
	assertFileContent(t, filepath.Join(newTarget, "old-only.md"), "old\n")
	assertFileContent(t, filepath.Join(newTarget, "same.md"), "new same\n")
	assertFileContent(t, filepath.Join(oldTarget, "old-only.md"), "old\n")
}

func TestLinkRunRejectsMergeIntoDescendantTarget(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "code", "checkout")
	oldProject := filepath.Join("code", "project")
	newProject := filepath.Join("code", "project", "packages", "cli")
	oldTarget := filepath.Join(home, "llm", oldProject)
	newTarget := filepath.Join(home, "llm", newProject)

	for _, dir := range []string{cwd, oldTarget, newTarget} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(oldTarget, filepath.Join(cwd, ".llm")); err != nil {
		t.Fatal(err)
	}

	restore := useTestHome(t, home, cwd)
	defer restore()

	err := linkRun(linkCmd, []string{newProject})
	if err == nil {
		t.Fatal("expected descendant merge to fail")
	}
	if !strings.Contains(err.Error(), "cannot merge") {
		t.Fatalf("error = %q, want cannot merge", err)
	}
	target, err := os.Readlink(filepath.Join(cwd, ".llm"))
	if err != nil {
		t.Fatal(err)
	}
	if target != oldTarget {
		t.Fatalf(".llm target = %q, want %q", target, oldTarget)
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

func useTestHome(t *testing.T, home, cwd string) func() {
	t.Helper()
	oldHome := homeDir
	oldRoot := llmRoot
	oldLinkAuto := linkAuto
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	homeDir = home
	llmRoot = filepath.Join(home, "llm")
	linkAuto = false
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	return func() {
		homeDir = oldHome
		llmRoot = oldRoot
		linkAuto = oldLinkAuto
		if err := os.Chdir(oldCwd); err != nil {
			t.Fatal(err)
		}
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != want {
		t.Fatalf("%s = %q, want %q", path, content, want)
	}
}
