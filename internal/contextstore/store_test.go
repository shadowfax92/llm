package contextstore

import (
	"context"
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

	binding, err := testStore(t, home).Resolve(context.Background(), nested, ResolveOptions{Scope: ScopeRepo})
	project := binding.Project.Key
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join("code", "project")
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

	binding, err := testStore(t, home).Resolve(context.Background(), nested, ResolveOptions{Scope: ScopeRepo})
	project := binding.Project.Key
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

	binding, err := testStore(t, home).Resolve(context.Background(), nested, ResolveOptions{Scope: ScopeRepo})
	project := binding.Project.Key
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join("code", "project")
	if project != want {
		t.Fatalf("project = %q, want %q", project, want)
	}
}

func TestReadOnlyResolveOutsideGitReportsMissing(t *testing.T) {
	home := t.TempDir()

	binding, err := testStore(t, home).Resolve(context.Background(), home, ResolveOptions{Scope: ScopeRepo})
	if err != nil || binding.State != "missing" {
		t.Fatalf("binding=%+v err=%v", binding, err)
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

	store := testStore(t, home)

	if _, err := store.Link(context.Background(), cwd, newProject, ScopeCwd); err != nil {
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

	store := testStore(t, home)

	_, err := store.Link(context.Background(), cwd, newProject, ScopeCwd)
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

func testStore(t *testing.T, home string) *Store {
	t.Helper()
	s, err := Open(Config{Home: home, Root: filepath.Join(home, "llm")})
	if err != nil {
		t.Fatal(err)
	}
	return s
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

func TestRelativeBindingIsIdempotentAndTraversalIsRejected(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "checkout")
	target := filepath.Join(home, "llm", "code", "project")
	for _, dir := range []string{cwd, target} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	relative, err := filepath.Rel(cwd, target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relative, filepath.Join(cwd, ".llm")); err != nil {
		t.Fatal(err)
	}
	store := testStore(t, home)
	result, err := store.Link(context.Background(), cwd, filepath.Join("code", "project"), ScopeCwd)
	if err != nil || result.Changed {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, key := range []string{"..", "../escape", ".", "/tmp/outside", ".projects", "templates"} {
		if _, err := store.Link(context.Background(), cwd, key, ScopeCwd); err == nil {
			t.Fatalf("accepted invalid key %q", key)
		}
	}
}

func TestLinkPreservesBackupAndDoesNotFollowDestinationSymlink(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "checkout")
	target := filepath.Join(home, "llm", "project")
	outside := filepath.Join(home, "outside")
	for _, dir := range []string{filepath.Join(cwd, ".llm", "nested"), target, outside} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(cwd, ".llm", "nested", "private.md"), "private")
	if err := os.Symlink(outside, filepath.Join(target, "nested")); err != nil {
		t.Fatal(err)
	}
	result, err := testStore(t, home).Link(context.Background(), cwd, "project", ScopeCwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) != 1 {
		t.Fatalf("conflicts=%+v", result.Conflicts)
	}
	assertFileContent(t, filepath.Join(cwd, ".llm.bak", "nested", "private.md"), "private")
	if _, err := os.Lstat(filepath.Join(outside, "private.md")); !os.IsNotExist(err) {
		t.Fatalf("followed destination symlink: %v", err)
	}
}

func TestCanonicalEnsureFailsWithoutCreatingDirectories(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "project")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatal(err)
	}
	_, err := testStore(t, home).Ensure(context.Background(), cwd, ResolveOptions{})
	if err == nil {
		t.Fatal("automatic initialization outside Git claimed success")
	}
	if _, err := os.Lstat(filepath.Join(home, "llm")); !os.IsNotExist(err) {
		t.Fatalf("non-Git initialization mutated store: %v", err)
	}
}

func TestEnsureRejectsStoreAndHomeAnchorsAndDifferentExplicitProject(t *testing.T) {
	home := t.TempDir()
	store := testStore(t, home)
	root := filepath.Join(home, "llm")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{home, root} {
		if _, err := store.Ensure(context.Background(), cwd, ResolveOptions{Scope: ScopeCwd, ProjectKey: "project"}); err == nil {
			t.Fatalf("initialized forbidden anchor %s", cwd)
		}
	}
	cwd := filepath.Join(home, "checkout")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Ensure(context.Background(), cwd, ResolveOptions{Scope: ScopeCwd, ProjectKey: "first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Ensure(context.Background(), cwd, ResolveOptions{Scope: ScopeCwd, ProjectKey: "second"}); err == nil {
		t.Fatal("explicit project silently ignored")
	}
}

func TestLegacyRegistryKeysRemainUsableDuringInitialization(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "llm")
	cwd := filepath.Join(home, "new")
	for _, dir := range []string{root, cwd} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	keys := []string{".config/nvim", "code/project/.worktrees/feature", "code/project/archive", "code/templates"}
	if err := os.WriteFile(filepath.Join(root, ".projects"), []byte(strings.Join(keys, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	store := testStore(t, home)
	if _, err := store.Ensure(context.Background(), cwd, ResolveOptions{Scope: ScopeCwd}); err != nil {
		t.Fatal(err)
	}
	projects, err := store.Projects(context.Background())
	if err != nil || len(projects) != len(keys)+1 {
		t.Fatalf("projects=%+v err=%v", projects, err)
	}
}

func TestRelinkCopiesFromResolvedManagedAlias(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "checkout")
	root := filepath.Join(home, "llm")
	old := filepath.Join(root, "old")
	target := filepath.Join(root, "new")
	for _, dir := range []string{cwd, old, target} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(old, "keep.md"), "keep")
	if err := os.Symlink(old, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "alias"), filepath.Join(cwd, ".llm")); err != nil {
		t.Fatal(err)
	}
	result, err := testStore(t, home).Link(context.Background(), cwd, "new", ScopeCwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Added) != 1 {
		t.Fatalf("copied=%+v", result.Added)
	}
	assertFileContent(t, filepath.Join(target, "keep.md"), "keep")
	assertFileContent(t, filepath.Join(old, "keep.md"), "keep")
}

func TestExplicitLinkRepairsMissingManagedTarget(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "checkout")
	target := filepath.Join(home, "llm", "new")
	for _, dir := range []string{cwd, target} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(home, "llm", "missing"), filepath.Join(cwd, ".llm")); err != nil {
		t.Fatal(err)
	}
	result, err := testStore(t, home).Link(context.Background(), cwd, "new", ScopeCwd)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || len(result.Warnings) != 1 {
		t.Fatalf("result=%+v", result)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(cwd, ".llm"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("target=%s want=%s", resolved, want)
	}
}

func TestLocalContextCannotMergeIntoItsOwnAncestor(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "llm", "project")
	local := filepath.Join(cwd, ".llm")
	if err := os.MkdirAll(local, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(local, "keep.md"), "keep")
	if _, err := testStore(t, home).Link(context.Background(), cwd, "project", ScopeCwd); err == nil {
		t.Fatal("accepted overlapping local migration")
	}
	assertFileContent(t, filepath.Join(local, "keep.md"), "keep")
	if _, err := os.Lstat(local + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("local source moved before validation: %v", err)
	}
}
