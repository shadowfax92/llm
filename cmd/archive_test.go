package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dotllm/internal/archive"
	"dotllm/internal/contextstore"
	"dotllm/internal/output"
)

func archiveCLI(t *testing.T) (Dependencies, string, string) {
	t.Helper()
	home := t.TempDir()
	home, canonicalErr := filepath.EvalSymlinks(home)
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DOTLLM_ROOT", "")
	cwd := filepath.Join(home, "checkout")
	root := filepath.Join(home, "llm")
	project := filepath.Join(root, "repo")
	for _, dir := range []string{cwd, project} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".projects"), []byte("repo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(project, filepath.Join(cwd, ".llm")); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-90 * 24 * time.Hour)
	if err := os.WriteFile(filepath.Join(project, "note"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(project, "note"), old, old); err != nil {
		t.Fatal(err)
	}
	return Dependencies{Home: home, Cwd: cwd}, root, project
}
func runArchiveCLI(t *testing.T, deps Dependencies, args ...string) (int, string, string) {
	t.Helper()
	var out, err bytes.Buffer
	deps.Out = &out
	deps.Err = &err
	code := Execute(args, deps)
	return code, out.String(), err.String()
}
func TestArchiveRestoreCLIExactPlanRoundtrip(t *testing.T) {
	deps, root, project := archiveCLI(t)
	planPath := filepath.Join(deps.Home, "plan.json")
	code, out, diagnostics := runArchiveCLI(t, deps, "archive", "--json", "--plan-out", planPath)
	if code != 0 || diagnostics != "" {
		t.Fatalf("plan code=%d stdout=%s stderr=%s", code, out, diagnostics)
	}
	var envelope struct {
		Data output.ArchiveData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Plan.Entries) != 1 {
		t.Fatalf("plan=%s", out)
	}
	if _, err := os.Stat(filepath.Join(project, "note")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".dotllm")); !os.IsNotExist(err) {
		t.Fatal("dry plan wrote store state")
	}
	if code, _, _ := runArchiveCLI(t, deps, "archive", "--json", "--plan-out", planPath); code == 0 {
		t.Fatal("overwrote reviewed plan")
	}
	code, out, diagnostics = runArchiveCLI(t, deps, "archive", "--apply", "--plan", planPath, "--json")
	if code != 0 || diagnostics != "" {
		t.Fatalf("apply=%d %s %s", code, out, diagnostics)
	}
	restorePath := filepath.Join(deps.Home, "restore.json")
	code, out, diagnostics = runArchiveCLI(t, deps, "restore", "--archive", envelope.Data.Plan.ID, "--entry", "note", "--plan-out", restorePath, "--json")
	if code != 0 || diagnostics != "" {
		t.Fatalf("restore plan=%d %s %s", code, out, diagnostics)
	}
	code, out, diagnostics = runArchiveCLI(t, deps, "restore", "--apply", "--plan", restorePath, "--json")
	if code != 0 || diagnostics != "" {
		t.Fatalf("restore apply=%d %s %s", code, out, diagnostics)
	}
	data, err := os.ReadFile(filepath.Join(project, "note"))
	if err != nil || string(data) != "original" {
		t.Fatalf("roundtrip=%q %v", data, err)
	}
}
func TestArchiveCLISelectorsAndInvalidOptionsNeverMove(t *testing.T) {
	deps, root, project := archiveCLI(t)
	for _, args := range [][]string{{"archive", "repo", "--list"}, {"archive", "--all", "--list"}, {"archive", "--days", "0"}} {
		if code, out, err := runArchiveCLI(t, deps, append(args, "--json")...); code != 0 {
			t.Fatalf("%v: %d %s %s", args, code, out, err)
		}
	}
	for _, args := range [][]string{{"archive", "--apply"}, {"archive", "--plan", "missing"}, {"archive", "--apply", "--plan", "missing", "--days", "1"}, {"archive", "repo", "--all"}, {"archive", "--pick"}, {"archive", "--days", "-1"}, {"archive", "--days", "365000"}, {"archive", "--days", "106752"}, {"restore"}, {"restore", "--apply"}, {"restore", "--archive", "legacy"}, {"restore", "--apply", "--plan", "missing", "--archive", "id"}} {
		code, out, err := runArchiveCLI(t, deps, append(args, "--json")...)
		if code != 2 || out != "" || !strings.Contains(err, "invalid_input") {
			t.Fatalf("%v: %d %s %s", args, code, out, err)
		}
	}
	if _, err := os.Stat(filepath.Join(project, "note")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".dotllm")); !os.IsNotExist(err) {
		t.Fatal("read-only CLI wrote state")
	}
}
func TestArchiveCLIPartialErrorsUseStderrAndRetainPaths(t *testing.T) {
	deps, root, project := archiveCLI(t)
	store, err := contextstore.Open(contextstore.Config{Home: deps.Home, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	manager := archive.New(store)
	plan, err := manager.Plan(context.Background(), archive.Selection{Projects: []contextstore.Project{{Key: "repo", Root: project}}, OlderThan: time.Now().Add(-30 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(root, ".dotllm", "archive")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	archived := filepath.Join(project, "archive", plan.ID, "items", "note")
	if err := os.MkdirAll(filepath.Dir(archived), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(project, "note"), archived); err != nil {
		t.Fatal(err)
	}
	// A changed destination models an interruption plus concurrent editing. The
	// error must carry the actual retained destination, without stdout success.
	if err := os.WriteFile(archived, []byte("edited archive"), 0600); err != nil {
		t.Fatal(err)
	}
	j := map[string]any{"version": 1, "plan": plan, "origins": map[string]string{"repo": project}, "intents": []bool{true}, "result": archive.Result{PlanID: plan.ID}}
	data, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, plan.ID+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	code, out, diagnostics := runArchiveCLI(t, deps, "archive", "--recover", plan.ID, "--json")
	if code != 4 || out != "" {
		t.Fatalf("partial=%d stdout=%s stderr=%s", code, out, diagnostics)
	}
	var envelope output.ErrorEnvelope
	if err := json.Unmarshal([]byte(diagnostics), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Partial == nil || envelope.Error.Partial.Complete || envelope.Error.Partial.Outcomes[0].Destination != archived {
		t.Fatalf("partial omitted: %+v", envelope)
	}
	code, out, diagnostics = runArchiveCLI(t, deps, "archive", "--recover", plan.ID)
	if code != 4 || out != "" || !strings.Contains(diagnostics, archived) || !strings.Contains(diagnostics, "conflict") {
		t.Fatalf("human partial hidden: %d %s %s", code, out, diagnostics)
	}
	if data, err := os.ReadFile(archived); err != nil || string(data) != "edited archive" {
		t.Fatalf("retained bytes=%q %v", data, err)
	}
}

// Archive uses contextstore's read-only identity resolution from both a nested
// main checkout and a linked worktree. A dry plan must reuse canonical context
// without publishing another .llm or registering a worktree-specific project.
func TestArchiveCLICanonicalNestedCurrentSelection(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("DOTLLM_ROOT", "")
	repo := filepath.Join(home, "code", "repo")
	nested := filepath.Join(repo, "src", "deep")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, output)
		}
	}
	git("init", "-q")
	git("-c", "user.name=Archive Fixture", "-c", "user.email=archive-fixture@example.test", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "fixture")
	deps := Dependencies{Home: home, Cwd: nested}
	code, out, diagnostics := runArchiveCLI(t, deps, "init", "--json")
	if code != 0 || diagnostics != "" {
		t.Fatalf("canonical init: %d %s %s", code, out, diagnostics)
	}
	var initialized struct {
		Data output.LinkData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &initialized); err != nil {
		t.Fatal(err)
	}
	project := initialized.Data.Binding.Project
	if project.Key != "code/repo" {
		t.Fatalf("canonical project=%+v", project)
	}
	note := filepath.Join(project.Root, "old-note")
	if err := os.WriteFile(note, []byte("canonical archived bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-90 * 24 * time.Hour)
	if err := os.Chtimes(note, old, old); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(home, "worktrees", "review")
	git("worktree", "add", "-q", "-b", "archive-fixture", worktree)
	worktreeNested := filepath.Join(worktree, "src", "deep")
	if err := os.MkdirAll(worktreeNested, 0700); err != nil {
		t.Fatal(err)
	}
	registry := filepath.Join(home, "llm", ".projects")
	before, err := os.ReadFile(registry)
	if err != nil {
		t.Fatal(err)
	}
	var fingerprint string
	for _, cwd := range []string{nested, worktreeNested} {
		current := deps
		current.Cwd = cwd
		code, out, diagnostics := runArchiveCLI(t, current, "archive", "--json", "--plan-out", "review.json")
		if code != 0 || diagnostics != "" {
			t.Fatalf("archive cwd=%s: %d %s %s", cwd, code, out, diagnostics)
		}
		var planned struct {
			Data output.ArchiveData `json:"data"`
		}
		if err := json.Unmarshal([]byte(out), &planned); err != nil {
			t.Fatal(err)
		}
		entries := planned.Data.Plan.Entries
		if len(entries) != 1 || entries[0].ProjectKey != "code/repo" || entries[0].RelativePath != "old-note" {
			t.Fatalf("cwd=%s plan=%+v", cwd, planned.Data.Plan)
		}
		if fingerprint != "" && entries[0].Fingerprint != fingerprint {
			t.Fatal("same canonical file acquired a different identity from linked worktree")
		}
		fingerprint = entries[0].Fingerprint
		if _, err := os.Stat(filepath.Join(cwd, "review.json")); err != nil {
			t.Fatalf("plan-out ignored selected cwd: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(cwd, ".llm")); !os.IsNotExist(err) {
			t.Fatalf("archive published nested binding: %v", err)
		}
	}
	if _, err := os.Lstat(filepath.Join(worktree, ".llm")); !os.IsNotExist(err) {
		t.Fatalf("archive published worktree root binding: %v", err)
	}
	after, err := os.ReadFile(registry)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("dry plan changed registry: %q %v", after, err)
	}
	if data, err := os.ReadFile(note); err != nil || string(data) != "canonical archived bytes" {
		t.Fatalf("dry plan moved bytes: %q %v", data, err)
	}
	// Explicit legacy nested context wins over the repo-wide canonical binding,
	// including from the corresponding path in a linked worktree.
	code, out, diagnostics = runArchiveCLI(t, deps, "init", "--scope", "cwd", "--project", "legacy/nested", "--json")
	if code != 0 || diagnostics != "" {
		t.Fatalf("nested init: %d %s %s", code, out, diagnostics)
	}
	if err := json.Unmarshal([]byte(out), &initialized); err != nil {
		t.Fatal(err)
	}
	nestedNote := filepath.Join(initialized.Data.Binding.Project.Root, "nested-note")
	if err := os.WriteFile(nestedNote, []byte("nested context bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(nestedNote, old, old); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{filepath.Join(nested, "inside"), filepath.Join(worktreeNested, "inside")} {
		if err := os.MkdirAll(cwd, 0700); err != nil {
			t.Fatal(err)
		}
		current := deps
		current.Cwd = cwd
		code, out, diagnostics := runArchiveCLI(t, current, "archive", "--json")
		if code != 0 || diagnostics != "" {
			t.Fatalf("nested archive cwd=%s: %d %s %s", cwd, code, out, diagnostics)
		}
		var planned struct {
			Data output.ArchiveData `json:"data"`
		}
		if err := json.Unmarshal([]byte(out), &planned); err != nil {
			t.Fatal(err)
		}
		entries := planned.Data.Plan.Entries
		if len(entries) != 1 || entries[0].ProjectKey != "legacy/nested" || entries[0].RelativePath != "nested-note" {
			t.Fatalf("nearest nested selection cwd=%s: %+v", cwd, planned.Data.Plan)
		}
		if _, err := os.Lstat(filepath.Join(cwd, ".llm")); !os.IsNotExist(err) {
			t.Fatalf("archive published deeper binding: %v", err)
		}
	}
}
