package contextstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Git itself is the observer: a helper-level pattern assertion would miss
// linked-worktree metadata and symlink-vs-directory matching mistakes.
func TestEnsureKeepsFreshGitStatusClean(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	initGitRepo(t, repo)
	runGit(t, repo, "config", "core.excludesFile", os.DevNull)
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	original := "# existing exclusion without final newline\n/owner-only"
	writeTestFile(t, exclude, original)
	beforeIgnore, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	s := testStore(t, home)
	first, err := s.Ensure(context.Background(), repo, ResolveOptions{})
	if err != nil || !first.Changed {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	assertGitClean(t, repo)
	lock, err := os.Stat(filepath.Join(repo, ".dotllm.lock"))
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile(exclude)
	if err != nil || !strings.HasPrefix(string(metadata), original+"\n") {
		t.Fatalf("exclusions lost: %q %v", metadata, err)
	}
	again, err := s.Ensure(context.Background(), repo, ResolveOptions{})
	if err != nil || again.Changed {
		t.Fatalf("repeat=%+v err=%v", again, err)
	}
	current, err := os.Stat(filepath.Join(repo, ".dotllm.lock"))
	if err != nil || !os.SameFile(lock, current) {
		t.Fatalf("lock inode replaced: %v", err)
	}
	after, _ := os.ReadFile(exclude)
	if string(after) != string(metadata) {
		t.Fatalf("repeat rewrote exclusions: %q", after)
	}
	afterIgnore, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if string(afterIgnore) != string(beforeIgnore) {
		t.Fatal("tracked gitignore changed")
	}
}

func assertGitClean(t *testing.T, repo string) {
	t.Helper()
	status, err := gitBytes(context.Background(), repo, "status", "--porcelain", "--untracked-files=all")
	if err != nil || len(status) != 0 {
		t.Fatalf("Git status at %s = %q, err=%v", repo, status, err)
	}
}

func TestEnsureExcludesLiteralNestedAnchorsAcrossWorktrees(t *testing.T) {
	home := t.TempDir()
	repo, linked := filepath.Join(home, "repo"), filepath.Join(home, "linked")
	initGitRepo(t, repo)
	runGit(t, repo, "config", "core.excludesFile", os.DevNull)
	runGit(t, repo, "worktree", "add", "--detach", linked)
	s := testStore(t, home)
	for _, tree := range []string{repo, linked} {
		dir := filepath.Join(tree, "!notes [a]*?\\ space\t", "nested ")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Ensure(context.Background(), dir, ResolveOptions{Scope: ScopeCwd, ProjectKey: "explicit/nested"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Ensure(context.Background(), tree, ResolveOptions{}); err != nil {
			t.Fatal(err)
		}
		assertGitClean(t, tree)
	}
	// Literal quoting must not hide an unrelated path matching the glob shape.
	other := filepath.Join(repo, "!notes aZZ space", "nested ")
	if err := os.MkdirAll(other, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(other, ".llm"), "owner data")
	status, err := gitBytes(context.Background(), repo, "status", "--porcelain", "--untracked-files=all")
	if err != nil || !strings.Contains(string(status), ".llm") {
		t.Fatalf("unrelated file hidden: %q %v", status, err)
	}
}

func TestTrackedContextIsNeverMigratedOrRelinked(t *testing.T) {
	for _, kind := range []string{"directory", "symlink", "missing", "lock"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			repo := filepath.Join(home, "repo")
			initGitRepo(t, repo)
			runGit(t, repo, "config", "core.excludesFile", os.DevNull)
			s := testStore(t, home)
			target := filepath.Join(home, "llm", "existing")
			if err := os.MkdirAll(target, 0755); err != nil {
				t.Fatal(err)
			}
			anchor := filepath.Join(repo, ".llm")
			tracked := anchor
			switch kind {
			case "directory", "missing":
				if err := os.MkdirAll(anchor, 0755); err != nil {
					t.Fatal(err)
				}
				tracked = filepath.Join(anchor, "notes.md")
				writeTestFile(t, tracked, "tracked bytes\x00\n")
			case "symlink":
				if err := os.Symlink(target, anchor); err != nil {
					t.Fatal(err)
				}
			case "lock":
				tracked = filepath.Join(repo, ".dotllm.lock")
				writeTestFile(t, tracked, "tracked lock bytes")
			}
			runGit(t, repo, "add", "--", tracked)
			if kind == "missing" {
				if err := os.Remove(tracked); err != nil {
					t.Fatal(err)
				}
			}
			before, err := gitBytes(context.Background(), repo, "status", "--porcelain", "--untracked-files=all")
			if err != nil {
				t.Fatal(err)
			}
			index, err := gitBytes(context.Background(), repo, "ls-files", "--stage", "-z")
			if err != nil {
				t.Fatal(err)
			}
			for _, operation := range []func() error{
				func() error { _, err := s.Ensure(context.Background(), repo, ResolveOptions{}); return err },
				func() error { _, err := s.Link(context.Background(), repo, "existing", ScopeRepo); return err },
			} {
				var domain *Error
				if err := operation(); !errors.As(err, &domain) || domain.Code != "conflict" || !strings.Contains(domain.NextAction, "index") {
					t.Fatalf("expected actionable tracked conflict: %v", err)
				}
			}
			after, _ := gitBytes(context.Background(), repo, "status", "--porcelain", "--untracked-files=all")
			afterIndex, _ := gitBytes(context.Background(), repo, "ls-files", "--stage", "-z")
			if string(before) != string(after) || string(index) != string(afterIndex) {
				t.Fatalf("tracked state changed: before=%q after=%q", before, after)
			}
			switch kind {
			case "directory", "lock":
				data, err := os.ReadFile(tracked)
				want := "tracked bytes\x00\n"
				if kind == "lock" {
					want = "tracked lock bytes"
				}
				if err != nil || string(data) != want {
					t.Fatalf("tracked data changed: %q %v", data, err)
				}
			case "symlink":
				link, err := os.Readlink(anchor)
				if err != nil || link != target {
					t.Fatalf("tracked link changed: %q %v", link, err)
				}
			case "missing":
				if _, err := os.Lstat(tracked); !os.IsNotExist(err) {
					t.Fatalf("tracked deletion changed: %v", err)
				}
			}
			if _, err := os.Lstat(anchor + ".bak"); !os.IsNotExist(err) {
				t.Fatalf("tracked context migrated: %v", err)
			}
		})
	}
}

func TestConcurrentStoresPreserveSharedGitExclusions(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	initGitRepo(t, repo)
	runGit(t, repo, "config", "core.excludesFile", os.DevNull)
	var wg sync.WaitGroup
	errors := make(chan error, 12)
	for i := 0; i < 12; i++ {
		dir := filepath.Join(repo, fmt.Sprintf("nested-%d", i))
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		s, err := Open(Config{Home: home, Root: filepath.Join(home, fmt.Sprintf("store-%d", i))})
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Ensure(context.Background(), dir, ResolveOptions{Scope: ScopeCwd})
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertGitClean(t, repo)
}

func TestResolveDoesNotRepairGitExclusions(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	initGitRepo(t, repo)
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	if err := os.Remove(exclude); err != nil {
		t.Fatal(err)
	}
	s := testStore(t, home)
	if _, err := s.Resolve(context.Background(), repo, ResolveOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Projects(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{exclude, filepath.Join(repo, ".dotllm.lock"), filepath.Join(home, "llm")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("read wrote %s: %v", path, err)
		}
	}
}
