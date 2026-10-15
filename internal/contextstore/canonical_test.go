package contextstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureCanonicalRepoFromNestedAndLinkedWorktree(t *testing.T) {
	home := t.TempDir()
	primary := filepath.Join(home, "code", "repo")
	linked := filepath.Join(home, "trees", "feature")
	initGitRepo(t, primary)
	runGit(t, primary, "worktree", "add", "--detach", linked)
	nested := filepath.Join(linked, "src", "deep")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	s := testStore(t, home)
	result, err := s.Ensure(context.Background(), nested, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "llm", "code", "repo")
	if !sameResolvedPath(result.Binding.Project.Root, want) || result.Binding.Project.Key != "code/repo" {
		t.Fatalf("binding=%+v", result.Binding)
	}
	if !sameResolvedPath(filepath.Dir(result.Binding.Anchor), linked) || !sameResolvedPath(result.Binding.CanonicalRoot, primary) || result.Binding.CommonGitDir == "" {
		t.Fatalf("identity=%+v", result.Binding)
	}
	again, err := s.Ensure(context.Background(), nested, ResolveOptions{})
	if err != nil || again.Changed {
		t.Fatalf("repeat=%+v err=%v", again, err)
	}
	if _, err := os.Lstat(filepath.Join(nested, ".llm")); !os.IsNotExist(err) {
		t.Fatalf("nested context created: %v", err)
	}
}

func TestCanonicalIdentitySeparateGitDir(t *testing.T) {
	home := t.TempDir()
	primary := filepath.Join(home, "code", "separate")
	gitDir := filepath.Join(home, "metadata", "repo.git")
	if err := os.MkdirAll(filepath.Dir(gitDir), 0755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, primary)
	runGit(t, primary, "init", "--separate-git-dir", gitDir)
	runGit(t, primary, "config", "core.worktree", primary)
	linked := filepath.Join(home, "trees", "detached")
	runGit(t, primary, "worktree", "add", "--detach", linked)
	s := testStore(t, home)
	first, err := s.Ensure(context.Background(), linked, ResolveOptions{})
	if err != nil || first.Binding.Project.Key != "code/separate" || !sameResolvedPath(first.Binding.CanonicalRoot, primary) {
		t.Fatalf("result=%+v err=%v", first, err)
	}
	if _, err := s.Ensure(context.Background(), primary, ResolveOptions{}); err != nil {
		t.Fatal(err)
	}

}

func TestNestedBindingWinsAndCanonicalNestedFallbackSurvives(t *testing.T) {
	home := t.TempDir()
	primary := filepath.Join(home, "code", "repo")
	linked := filepath.Join(home, "trees", "linked")
	initGitRepo(t, primary)
	runGit(t, primary, "worktree", "add", "--detach", linked)
	s := testStore(t, home)
	primaryNested := filepath.Join(primary, "packages", "cli")
	nested := filepath.Join(linked, "packages", "cli", "deep")
	for _, p := range []string{primaryNested, nested} {
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Ensure(context.Background(), primaryNested, ResolveOptions{Scope: ScopeCwd, ProjectKey: "legacy/cli"}); err != nil {
		t.Fatal(err)
	}
	first, err := s.Ensure(context.Background(), nested, ResolveOptions{})
	if err != nil || first.Binding.Project.Key != "legacy/cli" || !sameResolvedPath(filepath.Dir(first.Binding.Anchor), linked) {
		t.Fatalf("fallback=%+v err=%v", first, err)
	}
	localNested := filepath.Join(linked, "packages", "cli")
	if _, err := s.Link(context.Background(), localNested, "legacy/cli", ScopeCwd); err != nil {
		t.Fatal(err)
	}
	binding, err := s.Resolve(context.Background(), nested, ResolveOptions{})
	if err != nil || !sameResolvedPath(filepath.Dir(binding.Anchor), localNested) {
		t.Fatalf("nearest=%+v err=%v", binding, err)
	}
}

func TestMissingPrimaryNeverCreatesForkButExistingBindingWorks(t *testing.T) {
	home := t.TempDir()
	primary := filepath.Join(home, "code", "repo")
	linked := filepath.Join(home, "trees", "linked")
	metadata := filepath.Join(home, "git-data")
	initGitRepo(t, primary)
	runGit(t, primary, "init", "--separate-git-dir", metadata)
	runGit(t, primary, "worktree", "add", "--detach", linked)
	if err := os.Rename(primary, primary+"-moved"); err != nil {
		t.Fatal(err)
	}
	s := testStore(t, home)
	if _, err := s.Ensure(context.Background(), linked, ResolveOptions{}); err == nil {
		t.Fatal("missing primary silently forked context")
	}
	if _, err := os.Lstat(filepath.Join(home, "llm")); !os.IsNotExist(err) {
		t.Fatalf("created store: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(home, "llm", "legacy"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Link(context.Background(), linked, "legacy", ScopeCwd); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ensure(context.Background(), linked, ResolveOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestMovedAndSymlinkedRootKeepsDurableProject(t *testing.T) {
	home := t.TempDir()
	primary := filepath.Join(home, "code", "repo")
	moved := filepath.Join(home, "code", "renamed")
	initGitRepo(t, primary)
	s := testStore(t, home)
	if _, err := s.Ensure(context.Background(), primary, ResolveOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(primary, moved); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(home, "alias")
	if err := os.Symlink(moved, alias); err != nil {
		t.Fatal(err)
	}
	again, err := s.Ensure(context.Background(), alias, ResolveOptions{})
	if err != nil || again.Changed || again.Binding.Project.Key != "code/repo" {
		t.Fatalf("moved=%+v err=%v", again, err)
	}
}

func TestSeparateGitDirWithoutPrimaryRecordRequiresExplicitSelection(t *testing.T) {
	home := t.TempDir()
	primary := filepath.Join(home, "code", "repo")
	linked := filepath.Join(home, "trees", "linked")
	initGitRepo(t, primary)
	runGit(t, primary, "init", "--separate-git-dir", filepath.Join(home, "metadata"))
	runGit(t, primary, "worktree", "add", "--detach", linked)
	s := testStore(t, home)
	if _, err := s.Ensure(context.Background(), primary, ResolveOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ensure(context.Background(), linked, ResolveOptions{}); err == nil {
		t.Fatal("guessed primary from separate metadata")
	}
	if _, err := s.Link(context.Background(), linked, "code/repo", ScopeRepo); err != nil {
		t.Fatal(err)
	}
	if result, err := s.Ensure(context.Background(), linked, ResolveOptions{}); err != nil || result.Changed {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestBareRepoRequiresExplicitExistingProject(t *testing.T) {
	home := t.TempDir()
	bare := filepath.Join(home, "bare.git")
	if err := os.MkdirAll(bare, 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, bare, "init", "--bare")
	s := testStore(t, home)
	if _, err := s.Ensure(context.Background(), bare, ResolveOptions{}); err == nil {
		t.Fatal("auto initialized bare repo")
	}
	if err := os.MkdirAll(filepath.Join(home, "llm", "chosen"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Link(context.Background(), bare, "chosen", ScopeRepo); err != nil {
		t.Fatal(err)
	}
}
