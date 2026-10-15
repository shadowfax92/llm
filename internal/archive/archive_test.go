package archive_test

import (
	"context"
	"dotllm/internal/archive"
	"dotllm/internal/contextstore"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fixture struct {
	home, root string
	store      *contextstore.Store
	manager    *archive.Manager
	project    contextstore.Project
}

func setup(t *testing.T) fixture {
	t.Helper()
	home := t.TempDir()
	home, canonicalErr := filepath.EvalSymlinks(home)
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	root := filepath.Join(home, "llm")
	project := contextstore.Project{Key: "repo", Root: filepath.Join(root, "repo")}
	if err := os.MkdirAll(project.Root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".projects"), []byte("repo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := contextstore.Open(contextstore.Config{Home: home, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{home, root, store, archive.New(store), project}
}
func put(t *testing.T, path string, data []byte, when time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}
func TestReviewedArchiveRoundtripAndRetry(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	old := time.Now().Add(-90 * 24 * time.Hour)
	original := []byte{0, 1, 2, 0xff, '\n'}
	path := filepath.Join(f.project.Root, "old.bin")
	put(t, path, original, old)
	plan, err := f.manager.Plan(ctx, archive.Selection{Projects: []contextstore.Project{f.project}, OlderThan: time.Now().Add(-30 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Entries) != 1 || plan.Entries[0].RelativePath != "old.bin" {
		t.Fatalf("plan=%+v", plan)
	}
	if _, err := os.Stat(filepath.Join(f.root, ".dotllm")); !os.IsNotExist(err) {
		t.Fatalf("plan mutated store: %v", err)
	}
	result, err := f.manager.Apply(ctx, plan)
	if err != nil || !result.Complete {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	retry, err := f.manager.Apply(ctx, plan)
	if err != nil || !reflect.DeepEqual(result, retry) {
		t.Fatalf("retry=%+v err=%v", retry, err)
	}
	restore, err := f.manager.PlanRestore(ctx, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := f.manager.Apply(ctx, restore)
	if err != nil || !restored.Complete {
		t.Fatalf("restore=%+v err=%v", restored, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !reflect.DeepEqual(data, original) {
		t.Fatalf("roundtrip=%v err=%v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 || !info.ModTime().Equal(old) {
		t.Fatalf("metadata=%v err=%v", info, err)
	}
}

func TestPlanStrictRecursiveAgeAndExclusions(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	old := cutoff.Add(-time.Hour)
	put(t, filepath.Join(f.project.Root, "old-dir", "file"), []byte("old"), old)
	// Directory freshness catches recent deletions even when every file is old.
	if err := os.Chtimes(filepath.Join(f.project.Root, "old-dir"), cutoff.Add(time.Hour), cutoff.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(f.project.Root, "equal"), []byte("strict"), cutoff)
	put(t, filepath.Join(f.project.Root, "stale"), []byte("selected"), old)
	for _, name := range []string{"runs", "templates", "internal", ".dotllm", "nested", "linked", "with-run/runs", "with-link"} {
		if err := os.MkdirAll(filepath.Join(f.project.Root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	put(t, filepath.Join(f.project.Root, "nested", "child", "data"), []byte("nested project"), old)
	if err := os.WriteFile(filepath.Join(f.root, ".projects"), []byte("repo\nrepo/nested/child\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.home, filepath.Join(f.project.Root, "symlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.home, filepath.Join(f.project.Root, "with-link", "symlink")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"runs", "templates", "internal", ".dotllm", "nested/child", "nested", "linked", "with-run/runs", "with-run", "with-link"} {
		p := filepath.Join(f.project.Root, name)
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	// An ordinary empty directory is a legitimate stale entry, using its own mtime.
	plan, err := f.manager.Plan(ctx, archive.Selection{Projects: []contextstore.Project{f.project}, OlderThan: cutoff})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, e := range plan.Entries {
		names = append(names, e.RelativePath)
	}
	if !reflect.DeepEqual(names, []string{"linked", "stale"}) {
		t.Fatalf("entries=%v warnings=%v", names, plan.Warnings)
	}
	if len(plan.Warnings) < 6 {
		t.Fatalf("missing exclusion reasons: %+v", plan.Warnings)
	}
}

func TestApplyRejectsAllStaleEntriesBeforeAnyMove(t *testing.T) {
	for _, change := range []string{"content", "directory-time", "symlink", "new-nested-project", "wrong-store", "tampered-plan"} {
		t.Run(change, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			old := time.Now().Add(-90 * 24 * time.Hour)
			put(t, filepath.Join(f.project.Root, "a"), []byte("a"), old)
			put(t, filepath.Join(f.project.Root, "z", "data"), []byte("original"), old)
			os.Chtimes(filepath.Join(f.project.Root, "z"), old, old)
			plan, err := f.manager.Plan(ctx, archive.Selection{Projects: []contextstore.Project{f.project}, OlderThan: time.Now().Add(-30 * 24 * time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "content":
				put(t, filepath.Join(f.project.Root, "z", "data"), []byte("replaced"), old)
			case "directory-time":
				os.Chtimes(filepath.Join(f.project.Root, "z"), time.Now(), time.Now())
			case "symlink":
				os.Remove(filepath.Join(f.project.Root, "z", "data"))
				os.Symlink(filepath.Join(f.project.Root, "a"), filepath.Join(f.project.Root, "z", "data"))
			case "new-nested-project":
				os.WriteFile(filepath.Join(f.root, ".projects"), []byte("repo\nrepo/z\n"), 0600)
			case "wrong-store":
				other := setup(t)
				put(t, filepath.Join(other.project.Root, "a"), []byte("a"), old)
				put(t, filepath.Join(other.project.Root, "z", "data"), []byte("original"), old)
				os.Chtimes(filepath.Join(other.project.Root, "z"), old, old)
				f.manager = other.manager
			case "tampered-plan":
				plan.Entries[0].RelativePath = "../escape"
			}
			result, err := f.manager.Apply(ctx, plan)
			if err == nil || result.Complete {
				t.Fatalf("accepted stale plan: %+v %v", result, err)
			}
			if data, err := os.ReadFile(filepath.Join(f.project.Root, "a")); err != nil || string(data) != "a" {
				t.Fatalf("preflight moved unchanged entry: %q %v", data, err)
			}
		})
	}
}

func TestRestoreNoClobberAndArchivePathConflict(t *testing.T) {
	for _, kind := range []string{"file", "directory", "dangling-symlink"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			old := time.Now().Add(-90 * 24 * time.Hour)
			origin := filepath.Join(f.project.Root, "note")
			put(t, origin, []byte("preserve archived"), old)
			p, err := f.manager.Plan(ctx, archive.Selection{Projects: []contextstore.Project{f.project}, OlderThan: time.Now().Add(-30 * 24 * time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			moved, err := f.manager.Apply(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			restore, err := f.manager.PlanRestore(ctx, p.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "file":
				put(t, origin, []byte("new owner data"), time.Now())
			case "directory":
				os.Mkdir(origin, 0700)
			case "dangling-symlink":
				os.Symlink("absent", origin)
			}
			if _, err := f.manager.Apply(ctx, restore); err == nil {
				t.Fatal("clobbered occupied origin")
			}
			data, err := os.ReadFile(moved.Outcomes[0].Destination)
			if err != nil || string(data) != "preserve archived" {
				t.Fatalf("archive lost: %q %v", data, err)
			}
			if _, err := f.manager.PlanRestore(ctx, p.ID, nil); err == nil {
				t.Fatal("planned occupied origin")
			}
		})
	}
	f := setup(t)
	put(t, filepath.Join(f.project.Root, "archive"), []byte("not a directory"), time.Now().Add(-90*24*time.Hour))
	if _, err := f.manager.Plan(context.Background(), archive.Selection{Projects: []contextstore.Project{f.project}, OlderThan: time.Now()}); err == nil {
		t.Fatal("archive file conflict hidden")
	}
}

func TestCancellationAndLegacyProvenance(t *testing.T) {
	f := setup(t)
	old := time.Now().Add(-90 * 24 * time.Hour)
	put(t, filepath.Join(f.project.Root, "note"), []byte("original"), old)
	put(t, filepath.Join(f.project.Root, "archive", "loose.md"), []byte("legacy"), old)
	plan, err := f.manager.Plan(context.Background(), archive.Selection{Projects: []contextstore.Project{f.project}, OlderThan: time.Now().Add(-30 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan.Warnings, "\n"), "missing recorded origin") {
		t.Fatalf("warnings=%v", plan.Warnings)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.manager.Apply(ctx, plan); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, ".dotllm")); !os.IsNotExist(err) {
		t.Fatalf("cancelled apply wrote state: %v", err)
	}
	if _, err := f.manager.PlanRestore(context.Background(), "loose.md", nil); err == nil {
		t.Fatal("invented provenance")
	}
}

// Write an interrupted journal fixture through its durable on-disk contract,
// then exercise only Manager methods. The first move happened before its
// outcome write; the second still has only a source. No test hook is exported.
func interruptedFixture(t *testing.T) (fixture, archive.Plan, string) {
	t.Helper()
	f := setup(t)
	old := time.Now().Add(-90 * 24 * time.Hour)
	put(t, filepath.Join(f.project.Root, "a"), []byte("first"), old)
	put(t, filepath.Join(f.project.Root, "b"), []byte("second"), old)
	plan, err := f.manager.Plan(context.Background(), archive.Selection{Projects: []contextstore.Project{f.project}, OlderThan: time.Now().Add(-30 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(f.root, ".dotllm", "archive")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"version": 1, "plan": plan, "origins": map[string]string{"repo": f.project.Root}, "intents": []bool{true, false}, "result": archive.Result{PlanID: plan.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, plan.ID+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(f.project.Root, "archive", plan.ID, "items", "a")
	if err := os.MkdirAll(filepath.Dir(moved), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(f.project.Root, "a"), moved); err != nil {
		t.Fatal(err)
	}
	return f, plan, moved
}
func TestRecoverInterruptedMoveThenRetryExactPlan(t *testing.T) {
	f, plan, moved := interruptedFixture(t)
	ctx := context.Background()
	result, err := f.manager.Recover(ctx, plan.ID)
	var partial *archive.ApplyError
	if !errors.As(err, &partial) || result.Complete || len(result.Outcomes) != 2 || result.Outcomes[0].State != "moved" || result.Outcomes[1].State != "pending" {
		t.Fatalf("recovery=%+v err=%v", result, err)
	}
	if data, err := os.ReadFile(moved); err != nil || string(data) != "first" {
		t.Fatalf("moved bytes=%q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(f.project.Root, "b")); err != nil {
		t.Fatal("recover moved a pending entry")
	}
	result, err = f.manager.Apply(ctx, plan)
	if err != nil || !result.Complete {
		t.Fatalf("retry=%+v err=%v", result, err)
	}
	recovered, err := f.manager.Recover(ctx, plan.ID)
	if err != nil || !reflect.DeepEqual(recovered, result) {
		t.Fatalf("final recovery=%+v %v", recovered, err)
	}
	restore, err := f.manager.PlanRestore(ctx, plan.ID, []string{"b"})
	if err != nil || len(restore.Entries) != 1 {
		t.Fatalf("selected restore=%+v %v", restore, err)
	}
	if _, err := f.manager.Apply(ctx, restore); err != nil {
		t.Fatal(err)
	}
	retry, err := f.manager.Apply(ctx, plan)
	if err != nil || !retry.Complete {
		t.Fatalf("recorded completed retry=%+v %v", retry, err)
	}
	if data, err := os.ReadFile(filepath.Join(f.project.Root, "b")); err != nil || string(data) != "second" {
		t.Fatalf("old completed plan rearchived restored file: %q %v", data, err)
	}
}
func TestRecoveryConflictsNeverOverwriteOrGuess(t *testing.T) {
	for _, change := range []string{"both-present", "both-missing", "changed-destination", "symlink-parent", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			f, plan, moved := interruptedFixture(t)
			ctx := context.Background()
			switch change {
			case "both-present":
				put(t, filepath.Join(f.project.Root, "a"), []byte("replacement"), time.Now())
			case "both-missing":
				os.Remove(moved)
			case "changed-destination":
				put(t, moved, []byte("edited archive"), time.Now())
			case "symlink-parent":
				original := filepath.Dir(moved)
				relocated := filepath.Join(f.project.Root, "elsewhere")
				if err := os.Rename(original, relocated); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(relocated, original); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			result, err := f.manager.Recover(ctx, plan.ID)
			if err == nil || result.Complete {
				t.Fatalf("recovery hid interruption: %+v %v", result, err)
			}
			if data, err := os.ReadFile(filepath.Join(f.project.Root, "b")); err != nil || string(data) != "second" {
				t.Fatalf("unprocessed bytes=%q %v", data, err)
			}
			if change != "cancelled" {
				if _, err := f.manager.Apply(context.Background(), plan); err == nil {
					t.Fatal("retry accepted conflicting journal")
				}
			}
		})
	}
}

// Cancellation is triggered by an observable filesystem event rather than a
// timer or implementation call count. The context stays a normal cancellable
// context; its caller cancels as soon as the first rename is visible.
type cancelAfterMove struct {
	context.Context
	destination string
	cancel      context.CancelFunc
}

func (c cancelAfterMove) Err() error {
	if _, err := os.Lstat(c.destination); err == nil {
		c.cancel()
	}
	return c.Context.Err()
}
func TestCancellationAfterMoveReturnsRecoverableProgress(t *testing.T) {
	f := setup(t)
	old := time.Now().Add(-90 * 24 * time.Hour)
	for _, name := range []string{"a", "b"} {
		put(t, filepath.Join(f.project.Root, name), []byte(name), old)
	}
	plan, err := f.manager.Plan(context.Background(), archive.Selection{Projects: []contextstore.Project{f.project}, OlderThan: time.Now().Add(-30 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	moved := filepath.Join(f.project.Root, "archive", plan.ID, "items", "a")
	result, err := f.manager.Apply(cancelAfterMove{base, moved, cancel}, plan)
	var partial *archive.ApplyError
	if !errors.Is(err, context.Canceled) || !errors.As(err, &partial) || result.Complete {
		t.Fatalf("cancelled=%+v %v", result, err)
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatal("test did not interrupt after first actual move")
	}
	if _, err := os.Stat(filepath.Join(f.project.Root, "b")); err != nil {
		t.Fatal("cancellation moved a second entry")
	}
	recovered, err := f.manager.Recover(context.Background(), plan.ID)
	if err == nil || recovered.Outcomes[0].State != "moved" || recovered.Outcomes[1].State != "pending" {
		t.Fatalf("recovery=%+v %v", recovered, err)
	}
	result, err = f.manager.Apply(context.Background(), plan)
	if err != nil || !result.Complete {
		t.Fatalf("retry=%+v %v", result, err)
	}
}
func TestArchiveDestinationCollisionAndProjectSymlink(t *testing.T) {
	f := setup(t)
	old := time.Now().Add(-90 * 24 * time.Hour)
	put(t, filepath.Join(f.project.Root, "note"), []byte("source"), old)
	plan, err := f.manager.Plan(context.Background(), archive.Selection{Projects: []contextstore.Project{f.project}, OlderThan: time.Now().Add(-30 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	collision := filepath.Join(f.project.Root, "archive", plan.ID, "items", "note")
	put(t, collision, []byte("existing archive"), old)
	if _, err := f.manager.Apply(context.Background(), plan); err == nil {
		t.Fatal("accepted destination collision")
	}
	if data, err := os.ReadFile(collision); err != nil || string(data) != "existing archive" {
		t.Fatalf("collision=%q %v", data, err)
	}
	source := filepath.Join(f.root, "relocated")
	if err := os.Rename(f.project.Root, source); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, f.project.Root); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Plan(context.Background(), archive.Selection{Projects: []contextstore.Project{f.project}, OlderThan: time.Now()}); err == nil {
		t.Fatal("followed symlinked project")
	}
}

func TestRecoverAfterRestoreCannotRearmCompletedArchive(t *testing.T) {
	f, plan, _ := interruptedFixture(t)
	ctx := context.Background()
	if _, err := f.manager.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	restore, err := f.manager.PlanRestore(ctx, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Apply(ctx, restore); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Recover(ctx, plan.ID); err == nil {
		t.Fatal("expected archive locations changed after explicit restoration")
	}
	if _, err := f.manager.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if _, err := os.Stat(filepath.Join(f.project.Root, name)); err != nil {
			t.Fatalf("recovery rearmed completed archive for %s: %v", name, err)
		}
	}
}
func TestRetryExcludesNewlyRegisteredCandidateRoot(t *testing.T) {
	f := setup(t)
	old := time.Now().Add(-90 * 24 * time.Hour)
	dir := filepath.Join(f.project.Root, "nested")
	put(t, filepath.Join(dir, "data"), []byte("nested project bytes"), old)
	os.Chtimes(dir, old, old)
	plan, err := f.manager.Plan(context.Background(), archive.Selection{Projects: []contextstore.Project{f.project}, OlderThan: time.Now().Add(-30 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	journalPath := filepath.Join(f.root, ".dotllm", "archive", plan.ID+".json")
	if _, err := f.manager.Apply(cancelAfterMove{base, journalPath, cancel}, plan); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel after intent=%v", err)
	}
	if err := os.WriteFile(filepath.Join(f.root, ".projects"), []byte("repo\nrepo/nested\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Apply(context.Background(), plan); err == nil {
		t.Fatal("retry moved newly registered project root")
	}
	if data, err := os.ReadFile(filepath.Join(dir, "data")); err != nil || string(data) != "nested project bytes" {
		t.Fatalf("nested project moved: %q %v", data, err)
	}
}
func TestPartialRestoreKeepsMonotonicMoveHistory(t *testing.T) {
	f, plan, _ := interruptedFixture(t)
	ctx := context.Background()
	restore, err := f.manager.PlanRestore(ctx, plan.ID, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Apply(ctx, restore); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := f.manager.Recover(ctx, plan.ID); err == nil {
			t.Fatal("partial restore must remain visible as changed recorded locations")
		}
	}
	if _, err := f.manager.Apply(ctx, plan); err == nil {
		t.Fatal("partial retry rearchived a deliberately restored entry")
	}
	if data, err := os.ReadFile(filepath.Join(f.project.Root, "a")); err != nil || string(data) != "first" {
		t.Fatalf("restored data lost from origin: %q %v", data, err)
	}
}
func TestMultiProjectRestoreRequiresUnambiguousSelection(t *testing.T) {
	f := setup(t)
	other := filepath.Join(f.root, "other")
	if err := os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, ".projects"), []byte("repo\nother\n"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-90 * 24 * time.Hour)
	put(t, filepath.Join(f.project.Root, "note"), []byte("repo bytes"), old)
	put(t, filepath.Join(other, "note"), []byte("other bytes"), old)
	ctx := context.Background()
	projects, err := f.store.Projects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := f.manager.Plan(ctx, archive.Selection{Projects: projects, OlderThan: time.Now().Add(-30 * 24 * time.Hour)})
	if err != nil || len(plan.Entries) != 2 {
		t.Fatalf("plan=%+v %v", plan, err)
	}
	if _, err := f.manager.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.PlanRestore(ctx, plan.ID, []string{"note"}); err == nil {
		t.Fatal("ambiguous multi-project restore accepted")
	}
	for _, key := range []string{"repo", "other"} {
		restore, err := f.manager.PlanRestore(ctx, plan.ID, []string{key + "/note"})
		if err != nil || len(restore.Entries) != 1 {
			t.Fatalf("restore=%+v %v", restore, err)
		}
		if _, err := f.manager.Apply(ctx, restore); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"repo", "other"} {
		data, err := os.ReadFile(filepath.Join(f.root, key, "note"))
		if err != nil || string(data) != key+" bytes" {
			t.Fatalf("%s roundtrip=%q %v", key, data, err)
		}
	}
}
