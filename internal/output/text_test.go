package output

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"dotllm/internal/archive"
	"dotllm/internal/contextstore"
)

// Renderer tests cross the same seam commands use: an Envelope in, terminal
// text out. Expected text is written out literally from the UX contract so a
// formatting regression shows up as a readable diff.

var fixedNow = time.Date(2026, 10, 14, 21, 0, 0, 0, time.UTC)

func render(t *testing.T, command string, data any, color bool) string {
	t.Helper()
	t.Setenv("HOME", "/Users/me")
	previousNow, previousLocal := now, time.Local
	now = func() time.Time { return fixedNow }
	time.Local = time.FixedZone("PDT", -7*60*60)
	t.Cleanup(func() { now, time.Local = previousNow, previousLocal })

	var out bytes.Buffer
	if err := WriteText(&out, Envelope{Version: 1, Command: command, Data: data}, Options{Color: color, TerminalWidth: 80}); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	return out.String()
}

func assertText(t *testing.T, got, want string) {
	t.Helper()
	want = strings.TrimPrefix(want, "\n")
	if got != want {
		t.Fatalf("rendered text mismatch\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func managedBinding() contextstore.Binding {
	return contextstore.Binding{
		Project:       contextstore.Project{Key: "code/docs", Root: "/Users/me/llm/code/docs"},
		Anchor:        "/Users/me/code/docs/.wt/feat/ui/.llm",
		WorktreeRoot:  "/Users/me/code/docs/.wt/feat/ui",
		CanonicalRoot: "/Users/me/code/docs",
		State:         "managed",
	}
}

func TestPathOutputIsExactlyThePath(t *testing.T) {
	// A trailing space is legal in a directory name and must survive
	// `cd "$(dotllm path)"`; colour and ~ would both corrupt it.
	got := render(t, "path", PathData{Path: "/Users/me/llm/code/odd name "}, true)
	if got != "/Users/me/llm/code/odd name \n" {
		t.Fatalf("path output = %q", got)
	}
}

func TestPathWithLineBreakIsRefused(t *testing.T) {
	var out bytes.Buffer
	err := WriteText(&out, Envelope{Command: "path", Data: PathData{Path: "/tmp/a\nb"}}, Options{})
	if err == nil || out.Len() != 0 {
		t.Fatalf("err=%v out=%q; want refusal and no output", err, out.String())
	}
}

func TestStatusShowsAnchorStoreAndCheckoutIdentity(t *testing.T) {
	assertText(t, render(t, "status", StatusData{Binding: managedBinding()}, false), `
code/docs  [linked]
├── anchor     ~/code/docs/.wt/feat/ui/.llm
├── store      ~/llm/code/docs
├── checkout   ~/code/docs/.wt/feat/ui       worktree
└── canonical  ~/code/docs
`)
}

func TestStatusEndsEveryUnhealthyStateWithANextCommand(t *testing.T) {
	missing := contextstore.Binding{Project: contextstore.Project{Key: "code/foo", Root: "/Users/me/llm/code/foo"}, Anchor: "/Users/me/code/foo/.llm", State: "missing"}
	assertText(t, render(t, "status", StatusData{Binding: missing}, false), `
code/foo  [no .llm]
├── anchor  ~/code/foo/.llm  missing
└── store   ~/llm/code/foo   not linked
next: dotllm -C ~/code/foo init   # create the store if needed and link .llm
`)

	// Resolve reports no project for a dangling link, so the header falls back
	// to the checkout. Commands name the exact (quoted) anchor and directory, so
	// they act on that checkout even when status ran with -C from elsewhere.
	dangling := contextstore.Binding{Anchor: "/Users/me/code/my repo/.llm", State: "dangling"}
	assertText(t, render(t, "status", StatusData{Binding: dangling}, false), `
~/code/my repo  [dangling]
└── anchor  ~/code/my repo/.llm  symlink to a directory that no longer exists
next: dotllm -C '/Users/me/code/my repo' link <project>                             # point .llm at an existing store (see dotllm list)
      rm '/Users/me/code/my repo/.llm' && dotllm -C '/Users/me/code/my repo' init   # or replace the broken link with a fresh store
`)

	for _, state := range []string{"local", "conflict"} {
		got := render(t, "status", StatusData{Binding: contextstore.Binding{Anchor: "/Users/me/code/foo/.llm", State: state}}, false)
		if !strings.Contains(got, "\nnext: ") {
			t.Fatalf("%s status has no next command:\n%s", state, got)
		}
	}
	// The conflict anchor is a file we know nothing about: the suggested move
	// must refuse to clobber an existing .llm.aside.
	conflict := render(t, "status", StatusData{Binding: contextstore.Binding{Anchor: "/Users/me/code/foo/.llm", State: "conflict"}}, false)
	if !strings.Contains(conflict, "next: mv -n ~/code/foo/.llm ~/code/foo/.llm.aside && dotllm -C ~/code/foo init") {
		t.Fatalf("conflict guidance must not clobber:\n%s", conflict)
	}
}

func TestLinkMigrationShowsBackupCopiedAndKeptEntries(t *testing.T) {
	binding := contextstore.Binding{Project: contextstore.Project{Key: "code/foo", Root: "/Users/me/llm/code/foo"}, Anchor: "/Users/me/code/foo/.llm", State: "managed"}
	result := LinkData{
		Binding:    binding,
		Changed:    true,
		BackupPath: "/Users/me/code/foo/.llm.bak",
		Added: []contextstore.Change{
			{Path: "/Users/me/llm/code/foo/notes.md", PreservedAt: "/Users/me/code/foo/.llm.bak/notes.md", Reason: "copied"},
			{Path: "/Users/me/llm/code/foo/plans/2026-10.md", PreservedAt: "/Users/me/code/foo/.llm.bak/plans/2026-10.md", Reason: "copied"},
		},
		Conflicts: []contextstore.Change{
			{Path: "/Users/me/llm/code/foo/README.md", PreservedAt: "/Users/me/code/foo/.llm.bak/README.md", Reason: "existing destination or unsupported entry preserved"},
		},
	}
	assertText(t, render(t, "link", result, false), `
Moved the existing .llm directory aside to ~/code/foo/.llm.bak
Copied 2 files into ~/llm/code/foo
  + notes.md
  + plans/2026-10.md
Kept 1 entry where it was (the store already has that path, or it is not a regular file)
  ~ README.md  kept at ~/code/foo/.llm.bak/README.md
Linked ~/code/foo/.llm → ~/llm/code/foo
next: diff -r ~/code/foo/.llm.bak ~/llm/code/foo   # review what was not copied; nothing was overwritten
`)
}

func TestLinkWithoutChangesSaysAlreadyLinked(t *testing.T) {
	binding := contextstore.Binding{Project: contextstore.Project{Key: "code/foo", Root: "/Users/me/llm/code/foo"}, Anchor: "/Users/me/code/foo/.llm", State: "managed"}
	assertText(t, render(t, "link", LinkData{Binding: binding}, false), `
Already linked: ~/code/foo/.llm → ~/llm/code/foo
`)
}

// A name that merely starts with ".." is still inside the store: the kept
// entry must keep its review command and never claim everything was copied.
func TestKeptDotDotNamedEntryStillGetsReviewGuidance(t *testing.T) {
	binding := contextstore.Binding{Project: contextstore.Project{Key: "code/foo", Root: "/Users/me/llm/code/foo"}, Anchor: "/Users/me/code/foo/.llm", State: "managed"}
	result := LinkData{Binding: binding, Changed: true, BackupPath: "/Users/me/code/foo/.llm.bak", Conflicts: []contextstore.Change{
		{Path: "/Users/me/llm/code/foo/..notes.md", PreservedAt: "/Users/me/code/foo/.llm.bak/..notes.md"},
	}}
	got := render(t, "link", result, false)
	if !strings.Contains(got, "  ~ ..notes.md  kept at ~/code/foo/.llm.bak/..notes.md\n") || !strings.Contains(got, "next: diff -r ~/code/foo/.llm.bak ~/llm/code/foo") {
		t.Fatalf("kept ..-prefixed entry lost its guidance:\n%s", got)
	}
	if strings.Contains(got, "every file is in the store") {
		t.Fatalf("claims everything was copied while an entry was kept:\n%s", got)
	}
}

func TestLinkBoundsCopiedListButNeverKeptList(t *testing.T) {
	binding := contextstore.Binding{Project: contextstore.Project{Key: "k", Root: "/s/k"}, Anchor: "/c/.llm", State: "managed"}
	result := LinkData{Binding: binding, Changed: true}
	for name := range strings.SplitSeq("a b c d e f g h i j k l", " ") {
		result.Added = append(result.Added, contextstore.Change{Path: "/s/k/" + name})
		result.Conflicts = append(result.Conflicts, contextstore.Change{Path: "/s/k/" + name, PreservedAt: "/old/" + name})
	}
	got := render(t, "link", result, false)
	if !strings.Contains(got, "  … 2 more (--json lists every file)\n") || strings.Contains(got, "+ k\n") {
		t.Fatalf("copied list not bounded:\n%s", got)
	}
	if !strings.Contains(got, "  ~ l  kept at /old/l\n") {
		t.Fatalf("kept entries must all be listed with their location:\n%s", got)
	}
}

func TestListNamesStoreAndMarksCurrentProject(t *testing.T) {
	data := ListData{Current: "code/docs", Projects: []contextstore.Project{
		{Key: "code/browseros", Root: "/Users/me/llm/code/browseros"},
		{Key: "code/docs", Root: "/Users/me/llm/code/docs"},
		{Key: "notes", Root: "/Users/me/llm/notes"},
	}}
	assertText(t, render(t, "list", data, false), `
~/llm  3 projects
├── code/browseros
├── code/docs       ← here
└── notes
`)
	assertText(t, render(t, "list", ListData{}, false), `
No projects in the store yet.
next: dotllm init   # run inside a project directory
`)
}

func TestConfigShowsEffectiveValueSourceAndLink(t *testing.T) {
	data := ConfigData{Path: "/Users/me/.config/dotllm/config.json", StoreRoot: "/Volumes/ctx", Source: "DOTLLM_ROOT", Binding: managedBinding()}
	data.Binding.Project.Root = "/Volumes/ctx/code/docs"
	assertText(t, render(t, "config", data, false), `
dotllm config
├── store root   /Volumes/ctx                                           from $DOTLLM_ROOT
├── config file  ~/.config/dotllm/config.json                           not in use
└── this .llm    ~/code/docs/.wt/feat/ui/.llm → /Volumes/ctx/code/docs  [linked]
order: --store, $DOTLLM_ROOT, config file, ~/llm
`)
}

func TestArchivePlanIsAPreviewWithExplicitApplySteps(t *testing.T) {
	plan := archive.Plan{Version: 1, ID: "7f3c9a2e", Operation: "archive", Cutoff: time.Date(2026, 7, 16, 4, 16, 0, 0, time.UTC), Entries: []archive.Entry{
		{ProjectKey: "code/browseros", RelativePath: "2026-05-plan.md", SizeBytes: 4096, NewestModTime: fixedNow.Add(-142 * 24 * time.Hour)},
		{ProjectKey: "code/browseros", RelativePath: "research", SizeBytes: 1153434, NewestModTime: fixedNow.Add(-98 * 24 * time.Hour)},
		{ProjectKey: "code/browseros", RelativePath: "scratch.md", SizeBytes: 812, NewestModTime: fixedNow.Add(-91 * 24 * time.Hour)},
	}}
	assertText(t, render(t, "archive", ArchiveData{Plan: plan}, false), `
Would archive 3 entries from code/browseros (nothing moved)
├── 2026-05-plan.md  4.0 KB  modified 142d ago
├── research         1.1 MB  modified 98d ago
└── scratch.md       812 B   modified 91d ago
plan 7f3c9a2e · last modified before 2026-07-15 21:16 PDT
next: dotllm archive <same arguments> --plan-out <file>   # save this exact plan for review
      dotllm archive --apply --plan <file>                # then apply only what that file lists
`)
}

func TestArchivePlanAcrossProjectsGroupsByProject(t *testing.T) {
	plan := archive.Plan{ID: "p1", Operation: "archive", Entries: []archive.Entry{
		{ProjectKey: "code/a", RelativePath: "old.md", SizeBytes: 10},
		{ProjectKey: "code/b", RelativePath: "older.md", SizeBytes: 20},
		{ProjectKey: "code/a", RelativePath: "notes", SizeBytes: 30},
	}}
	assertText(t, render(t, "archive", ArchiveData{Plan: plan}, false), `
Would archive 3 entries from 2 projects (nothing moved)
code/a
  ├── old.md  10 B
  └── notes   30 B
code/b
  └── older.md  20 B
plan p1
next: dotllm archive <same arguments> --plan-out <file>   # save this exact plan for review
      dotllm archive --apply --plan <file>                # then apply only what that file lists
`)
}

func TestRestorePlanPreview(t *testing.T) {
	plan := archive.Plan{ID: "r1", Operation: "restore", Entries: []archive.Entry{{ProjectKey: "code/a", RelativePath: "old.md", SizeBytes: 10}}}
	got := render(t, "restore", ArchiveData{Plan: plan}, false)
	if !strings.HasPrefix(got, "Would restore 1 entry into code/a (nothing moved)\n") || !strings.Contains(got, "next: dotllm restore --apply --plan <file>") {
		t.Fatalf("restore preview:\n%s", got)
	}
	// restore defines no --plan-out; guidance must not send users to a flag
	// that fails with "unknown flag".
	if strings.Contains(got, "--plan-out") {
		t.Fatalf("restore guidance names an undefined flag:\n%s", got)
	}
}

// After a partial apply the rendering is the recovery map: which entries
// moved where, which failed and why, and which were never attempted.
func TestPartialArchiveListsEveryOutcomeWithErrorsAndRecovery(t *testing.T) {
	result := archive.Result{PlanID: "7f3c9a2e", Complete: false, Outcomes: []archive.Outcome{
		{ProjectKey: "code/browseros", RelativePath: "2026-05-plan.md", Destination: "/Users/me/llm/code/browseros/archive/7f3c9a2e/items/2026-05-plan.md", State: "moved"},
		{ProjectKey: "code/browseros", RelativePath: "research", Destination: "/Users/me/llm/code/browseros/archive/7f3c9a2e/items/research", State: "failed", Error: "rename research: cross-device link"},
		{ProjectKey: "code/browseros", RelativePath: "scratch.md", State: "pending"},
	}}
	assertText(t, render(t, "archive", ApplyData{Result: result}, false), `
Archive incomplete · code/browseros · plan 7f3c9a2e · 1 moved, 1 failed, 1 pending, 1 error
├── moved    2026-05-plan.md  → ~/llm/code/browseros/archive/7f3c9a2e/items/2026-05-plan.md
├── failed   research
│            error: rename research: cross-device link
│            destination: ~/llm/code/browseros/archive/7f3c9a2e/items/research
└── pending  scratch.md
next: dotllm restore --archive 7f3c9a2e   # preview putting back what this plan moved
      dotllm archive code/browseros       # plan again for entries that did not move
`)
}

func TestPartialRestoreAcrossProjectsKeepsEveryOutcome(t *testing.T) {
	result := archive.Result{PlanID: "r9", Complete: false, Outcomes: []archive.Outcome{
		{ProjectKey: "code/a", RelativePath: "notes.md", Destination: "/Users/me/llm/code/a/notes.md", State: "restored"},
		{ProjectKey: "code/b", RelativePath: "plan.md", Destination: "/Users/me/llm/code/b/archive/r9/items/plan.md", State: "conflict", Error: "destination exists; archived copy kept"},
	}}
	assertText(t, render(t, "restore", ApplyData{Result: result}, false), `
Restore incomplete · 2 projects · plan r9 · 1 restored, 1 conflict, 1 error
code/a
  └── restored  notes.md  → ~/llm/code/a/notes.md
code/b
  └── conflict  plan.md
                error: destination exists; archived copy kept
                destination: ~/llm/code/b/archive/r9/items/plan.md
next: dotllm restore --archive r9   # preview what is still archived
`)
}

func TestCompleteArchiveOffersUndoPreview(t *testing.T) {
	result := archive.Result{PlanID: "7f3c9a2e", Complete: true, Outcomes: []archive.Outcome{
		{ProjectKey: "code/a", RelativePath: "old.md", Destination: "/Users/me/llm/code/a/archive/7f3c9a2e/items/old.md", State: "moved"},
	}}
	assertText(t, render(t, "archive", ApplyData{Result: result}, false), `
Archive complete · code/a · plan 7f3c9a2e · 1 moved
└── moved  old.md  → ~/llm/code/a/archive/7f3c9a2e/items/old.md
undo: dotllm restore --archive 7f3c9a2e   # previews the restore; nothing moves until --apply
`)
}

func TestColorFollowsOptionsOnly(t *testing.T) {
	fixtures := map[string]any{
		"status":  StatusData{Binding: managedBinding()},
		"link":    LinkData{Binding: managedBinding(), Changed: true},
		"list":    ListData{Projects: []contextstore.Project{{Key: "a", Root: "/s/a"}}},
		"config":  ConfigData{Binding: managedBinding()},
		"archive": ArchiveData{Plan: archive.Plan{Entries: []archive.Entry{{ProjectKey: "a", RelativePath: "x"}}}},
		"restore": ApplyData{Result: archive.Result{Outcomes: []archive.Outcome{{ProjectKey: "a", State: "failed", Error: "boom"}}}},
	}
	for command, data := range fixtures {
		if plain := render(t, command, data, false); strings.Contains(plain, "\x1b[") {
			t.Fatalf("%s: escape codes without Color:\n%q", command, plain)
		}
		if colored := render(t, command, data, true); !strings.Contains(colored, "\x1b[") {
			t.Fatalf("%s: no escape codes with Color", command)
		}
	}
	colored := render(t, "status", StatusData{Binding: managedBinding()}, true)
	if !strings.HasPrefix(colored, "\x1b[1;36mcode/docs\x1b[0m  \x1b[32m[linked]\x1b[0m\n") {
		t.Fatalf("status header not in Grove heading/branch colours: %q", colored)
	}
}

func TestUnknownDataIsAnError(t *testing.T) {
	var out bytes.Buffer
	if err := WriteText(&out, Envelope{Data: struct{}{}}, Options{}); err == nil || out.Len() != 0 {
		t.Fatalf("err=%v out=%q", err, out.String())
	}
}
