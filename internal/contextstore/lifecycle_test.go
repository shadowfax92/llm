package contextstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestConcurrentEnsurePreservesAllRegistryUpdates(t *testing.T) {
	home := t.TempDir()
	s := testStore(t, home)
	const count = 12
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		cwd := filepath.Join(home, fmt.Sprintf("project-%02d", i))
		if err := os.Mkdir(cwd, 0755); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Ensure(context.Background(), cwd, ResolveOptions{Scope: ScopeCwd})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	projects, err := s.Projects(context.Background())
	if err != nil || len(projects) != count {
		t.Fatalf("projects=%+v err=%v", projects, err)
	}
	cwd := filepath.Join(home, "shared")
	if err := os.Mkdir(cwd, 0755); err != nil {
		t.Fatal(err)
	}
	errs = make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Ensure(context.Background(), cwd, ResolveOptions{Scope: ScopeCwd})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	projects, err = s.Projects(context.Background())
	if err != nil || len(projects) != count+1 {
		t.Fatalf("shared projects=%+v err=%v", projects, err)
	}
}

func TestLocalMigrationPreservesBytesSymlinksAndConflicts(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "project")
	local := filepath.Join(cwd, ".llm")
	target := filepath.Join(home, "llm", "chosen")
	for _, p := range []string{local, target} {
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(local, "notes.md"), "original\x00bytes")
	writeTestFile(t, filepath.Join(local, "conflict.md"), "old")
	writeTestFile(t, filepath.Join(target, "conflict.md"), "new")
	if err := os.Symlink("notes.md", filepath.Join(local, "relative")); err != nil {
		t.Fatal(err)
	}
	result, err := testStore(t, home).Ensure(context.Background(), cwd, ResolveOptions{Scope: ScopeCwd, ProjectKey: "chosen"})
	if err != nil || result.BackupPath != filepath.Join(result.Binding.Anchor+".bak") || len(result.Conflicts) != 2 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	assertFileContent(t, filepath.Join(local+".bak", "notes.md"), "original\x00bytes")
	assertFileContent(t, filepath.Join(target, "notes.md"), "original\x00bytes")
	assertFileContent(t, filepath.Join(target, "conflict.md"), "new")
	if link, err := os.Readlink(filepath.Join(local+".bak", "relative")); err != nil || link != "notes.md" {
		t.Fatalf("symlink=%q err=%v", link, err)
	}
	if _, err := os.Lstat(filepath.Join(target, "relative")); !os.IsNotExist(err) {
		t.Fatalf("copied source symlink: %v", err)
	}
}

func TestExternalRootAncestorAndBackupConflictsNeverReplaceSource(t *testing.T) {
	for _, kind := range []string{"external", "store-root", "backup", "target-escape", "registry-link"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			cwd := filepath.Join(home, "checkout")
			target := filepath.Join(home, "llm", "chosen")
			outside := filepath.Join(home, "outside")
			for _, p := range []string{cwd, target, outside} {
				if err := os.MkdirAll(p, 0755); err != nil {
					t.Fatal(err)
				}
			}
			anchor := filepath.Join(cwd, ".llm")
			switch kind {
			case "external":
				if err := os.Symlink(outside, anchor); err != nil {
					t.Fatal(err)
				}
			case "store-root":
				if err := os.Symlink(filepath.Dir(target), anchor); err != nil {
					t.Fatal(err)
				}
			case "backup":
				if err := os.Mkdir(anchor, 0755); err != nil {
					t.Fatal(err)
				}
				writeTestFile(t, filepath.Join(anchor, "keep"), "original")
				if err := os.Symlink("missing", anchor+".bak"); err != nil {
					t.Fatal(err)
				}
			case "target-escape":
				if err := os.Symlink(outside, filepath.Join(home, "llm", "escape")); err != nil {
					t.Fatal(err)
				}
			case "registry-link":
				writeTestFile(t, filepath.Join(outside, "registry"), "chosen\n")
				if err := os.Symlink(filepath.Join(outside, "registry"), filepath.Join(home, "llm", ".projects")); err != nil {
					t.Fatal(err)
				}
			}
			key := "chosen"
			if kind == "target-escape" {
				key = "escape"
			}
			if _, err := testStore(t, home).Link(context.Background(), cwd, key, ScopeCwd); err == nil {
				t.Fatal("accepted unsafe mutation")
			}
			if kind == "backup" {
				assertFileContent(t, filepath.Join(anchor, "keep"), "original")
			}
			if kind == "external" {
				if got, err := os.Readlink(anchor); err != nil || got != outside {
					t.Fatalf("external link changed: %q %v", got, err)
				}
			}
			entries, err := os.ReadDir(outside)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if kind == "registry-link" {
				want = 1
			}
			if len(entries) != want {
				t.Fatalf("outside changed: %v", entries)
			}
		})
	}
}

func TestWaitingWriterCancelsAndBusyIsActionable(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "project")
	root := filepath.Join(home, "llm")
	if err := os.Mkdir(cwd, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".dotllm"), 0755); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(root, ".dotllm", "context.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	s := testStore(t, home)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := s.Ensure(ctx, cwd, ResolveOptions{Scope: ScopeCwd}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel err=%v", err)
	}
	_, err = s.Ensure(context.Background(), cwd, ResolveOptions{Scope: ScopeCwd})
	var domain *Error
	if !errors.As(err, &domain) || domain.Code != "busy" || !domain.Retryable {
		t.Fatalf("busy err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(cwd, ".llm")); !os.IsNotExist(err) {
		t.Fatalf("published despite lock: %v", err)
	}
}

func TestRegisteredDescendantPreventsAncestorMerge(t *testing.T) {
	home := t.TempDir()
	s := testStore(t, home)
	cwd := filepath.Join(home, "checkout")
	target := filepath.Join(home, "llm", "parent", "child")
	for _, p := range []string{filepath.Join(cwd, ".llm"), target} {
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(home, "llm", ".projects"), "parent/child\n")
	writeTestFile(t, filepath.Join(cwd, ".llm", "keep"), "keep")
	if _, err := s.Link(context.Background(), cwd, "parent", ScopeCwd); err == nil {
		t.Fatal("accepted registered project ancestor")
	}
	assertFileContent(t, filepath.Join(cwd, ".llm", "keep"), "keep")
}

func TestMissingAnchorWithBackupRequiresRecovery(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "project")
	backup := filepath.Join(cwd, ".llm.bak")
	if err := os.MkdirAll(backup, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(backup, "keep"), "original")
	if _, err := testStore(t, home).Ensure(context.Background(), cwd, ResolveOptions{Scope: ScopeCwd}); err == nil {
		t.Fatal("published over incomplete migration")
	}
	assertFileContent(t, filepath.Join(backup, "keep"), "original")
	if _, err := os.Lstat(filepath.Join(cwd, ".llm")); !os.IsNotExist(err) {
		t.Fatalf("published link: %v", err)
	}
}

func TestTemplatesAreCopiedWithoutClobberingExistingContext(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "checkout")
	root := filepath.Join(home, "llm")
	for _, path := range []string{cwd, filepath.Join(root, "templates"), filepath.Join(root, "chosen")} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(root, "templates", "new.md"), "template")
	writeTestFile(t, filepath.Join(root, "templates", "existing.md"), "template default")
	writeTestFile(t, filepath.Join(root, "chosen", "existing.md"), "personal")
	s := testStore(t, home)
	result, err := s.Ensure(context.Background(), cwd, ResolveOptions{Scope: ScopeCwd, ProjectKey: "chosen"})
	if err != nil || len(result.Warnings) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	assertFileContent(t, filepath.Join(root, "chosen", "new.md"), "template")
	assertFileContent(t, filepath.Join(root, "chosen", "existing.md"), "personal")
	again, err := s.Ensure(context.Background(), cwd, ResolveOptions{Scope: ScopeCwd})
	if err != nil || again.Changed {
		t.Fatalf("repeat=%+v err=%v", again, err)
	}
}

func TestWaitingEnsureUsesCanonicalBindingObservedUnderLock(t *testing.T) {
	home := t.TempDir()
	primary := filepath.Join(home, "code", "repo")
	linked := filepath.Join(home, "trees", "linked")
	initGitRepo(t, primary)
	runGit(t, primary, "worktree", "add", "--detach", linked)
	root := filepath.Join(home, "llm")
	if err := os.MkdirAll(filepath.Join(root, ".dotllm"), 0755); err != nil {
		t.Fatal(err)
	}
	anchorLock, err := os.OpenFile(filepath.Join(linked, ".dotllm.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer anchorLock.Close()
	if err := syscall.Flock(int(anchorLock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(anchorLock.Fd()), syscall.LOCK_UN)
	type outcome struct {
		result LinkResult
		err    error
	}
	finished := make(chan outcome, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s := testStore(t, home)
	go func() { result, err := s.Ensure(ctx, linked, ResolveOptions{}); finished <- outcome{result, err} }()
	// Hold the anchor externally so Ensure must wait after its initial Resolve.
	// Observe its store lock, publish a new canonical binding, then release it.
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	waiting := false
	for !waiting {
		select {
		case <-deadline.C:
			t.Fatal("writer never reached lock")
		case <-tick.C:
		}
		file, err := os.OpenFile(filepath.Join(root, ".dotllm", "context.lock"), os.O_RDWR, 0)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		waiting = errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
		if err == nil {
			_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		}
		file.Close()
	}
	chosen := filepath.Join(root, "chosen")
	if err := os.Mkdir(chosen, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(chosen, filepath.Join(primary, ".llm")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(anchorLock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	got := <-finished
	if got.err != nil || got.result.Binding.Project.Key != "chosen" {
		t.Fatalf("stale selection=%+v err=%v", got.result, got.err)
	}
	if !sameResolvedPath(filepath.Join(linked, ".llm"), chosen) {
		t.Fatal("linked worktree forked canonical context")
	}
}
