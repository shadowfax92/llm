package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func contextCommand(t *testing.T, home, cwd string, args ...string) (int, []byte, []byte) {
	t.Helper()
	var out, diagnostics bytes.Buffer
	code := Execute(args, Dependencies{Home: home, Cwd: cwd, Out: &out, Err: &diagnostics})
	return code, out.Bytes(), diagnostics.Bytes()
}
func contextRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("git", "init", "-q", path)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git: %v %s", err, out)
	}
}
func TestCanonicalCommandEntryPointsFromNestedDirectory(t *testing.T) {
	for _, args := range [][]string{{"--json"}, {"path", "--json"}, {"init", "--json"}, {"link", "--auto", "--json"}} {
		t.Run(args[0], func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
			t.Setenv("DOTLLM_ROOT", "")
			repo := filepath.Join(home, "code", "repo")
			contextRepo(t, repo)
			nested := filepath.Join(repo, "src", "deep")
			if err := os.MkdirAll(nested, 0755); err != nil {
				t.Fatal(err)
			}
			code, out, diagnostics := contextCommand(t, home, nested, args...)
			if code != 0 || len(diagnostics) != 0 {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, out, diagnostics)
			}
			var envelope struct {
				Version int `json:"version"`
			}
			if err := json.Unmarshal(out, &envelope); err != nil || envelope.Version != 1 {
				t.Fatalf("json=%s err=%v", out, err)
			}
			target, err := filepath.EvalSymlinks(filepath.Join(repo, ".llm"))
			if err != nil {
				t.Fatal(err)
			}
			want, err := filepath.EvalSymlinks(filepath.Join(home, "llm", "code", "repo"))
			if err != nil || target != want {
				t.Fatalf("target=%s want=%s err=%v", target, want, err)
			}
			if _, err := os.Lstat(filepath.Join(nested, ".llm")); !os.IsNotExist(err) {
				t.Fatalf("nested link: %v", err)
			}
			code, out, diagnostics = contextCommand(t, home, nested, args...)
			if code != 0 || len(diagnostics) != 0 {
				t.Fatalf("repeat=%d %s %s", code, out, diagnostics)
			}
		})
	}
}
func TestDiagnosticsAndInteractiveGuardsNeverCreateContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("DOTLLM_ROOT", "")
	repo := filepath.Join(home, "code", "repo")
	contextRepo(t, repo)
	nested := filepath.Join(repo, "src")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"status", "--json"}, {"list", "--json"}, {"config", "--json"}, {"config", "--path", "--json"}, {"link", "--pick", "--json"}, {"link", "--pick", "--no-input"}, {"config", "edit", "--json"}, {"link", "--auto", "chosen", "--json"}} {
		code, out, diagnostics := contextCommand(t, home, nested, args...)
		if code == 0 {
			if !json.Valid(out) || len(diagnostics) != 0 {
				t.Fatalf("invalid result %v: %s %s", args, out, diagnostics)
			}
		} else if len(out) != 0 {
			t.Fatalf("failure stdout %v: %s", args, out)
		}
		if _, err := os.Lstat(filepath.Join(home, "llm")); !os.IsNotExist(err) {
			t.Fatalf("%v created store: %v", args, err)
		}
		if _, err := os.Lstat(filepath.Join(repo, ".llm")); !os.IsNotExist(err) {
			t.Fatalf("%v created binding: %v", args, err)
		}
	}
}
func TestConfigEffectivePrecedenceAndCurrentAncestorLink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "settings"))
	t.Setenv("DOTLLM_ROOT", "")
	cwd := filepath.Join(home, "project")
	if err := os.MkdirAll(filepath.Join(cwd, "deep"), 0755); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(home, "settings", "dotllm")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.json")
	configStore := filepath.Join(home, "configured")
	data, _ := json.Marshal(map[string]any{"version": 1, "store_root": configStore})
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ env, flag, want, source string }{{"", "", configStore, "config"}, {filepath.Join(home, "env"), "", filepath.Join(home, "env"), "DOTLLM_ROOT"}, {filepath.Join(home, "env"), filepath.Join(home, "flag"), filepath.Join(home, "flag"), "--store"}} {
		t.Setenv("DOTLLM_ROOT", tc.env)
		args := []string{"config", "--json"}
		if tc.flag != "" {
			args = append(args, "--store", tc.flag)
		}
		code, out, errOut := contextCommand(t, home, filepath.Join(cwd, "deep"), args...)
		if code != 0 {
			t.Fatalf("code=%d %s", code, errOut)
		}
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(out, &envelope); err != nil {
			t.Fatal(err)
		}
		var effective struct {
			Path      string `json:"path"`
			StoreRoot string `json:"store_root"`
			Source    string `json:"source"`
		}
		if err := json.Unmarshal(envelope["data"], &effective); err != nil {
			t.Fatal(err)
		}
		if effective.Path != configPath || effective.StoreRoot != tc.want || effective.Source != tc.source {
			t.Fatalf("effective=%+v", effective)
		}
	}
	t.Setenv("DOTLLM_ROOT", "")
	code, _, errOut := contextCommand(t, home, cwd, "init", "--scope", "cwd", "--json")
	if code != 0 {
		t.Fatalf("init %d %s", code, errOut)
	}
	code, out, errOut := contextCommand(t, home, filepath.Join(cwd, "deep"), "status", "--json")
	if code != 0 || !bytes.Contains(out, []byte(`"state":"managed"`)) {
		t.Fatalf("status %d %s %s", code, out, errOut)
	}
}

func TestConfigPathAndExplicitEditorRecoverMalformedConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "settings"))
	t.Setenv("DOTLLM_ROOT", "")
	dir := filepath.Join(home, "settings", "dotllm")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := contextCommand(t, home, home, "config", "--path", "--json")
	if code != 0 || !json.Valid(out) || len(errOut) != 0 {
		t.Fatalf("path %d %s %s", code, out, errOut)
	}
	editor := filepath.Join(home, "fix-config")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf '{\"version\":1}\\n' > \"$1\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", editor)
	t.Setenv("VISUAL", "")
	var stdout, stderr bytes.Buffer
	code = Execute([]string{"config", "edit"}, Dependencies{Home: home, Cwd: home, IsTTY: true, Out: &stdout, Err: &stderr})
	if code != 0 {
		t.Fatalf("edit %d %s", code, &stderr)
	}
	if _, err := os.Lstat(filepath.Join(home, "llm")); !os.IsNotExist(err) {
		t.Fatalf("editor initialized store: %v", err)
	}
}
