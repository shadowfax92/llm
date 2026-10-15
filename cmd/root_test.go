package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"dotllm/internal/output"
)

// Exercise the command seam with an isolated home rather than process-global chdir.
func TestCommandsKeepJSONAndErrorsOnSeparateStreams(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DOTLLM_ROOT", "")
	cwd := filepath.Join(home, "code", "repo")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"init", "--scope", "cwd", "--json"}, 0},
		{[]string{"status", "--json"}, 0},
		{[]string{"list", "--json"}, 0},
		{[]string{"archive", "--json"}, 0},
		{[]string{"restore", "--json"}, 2},
		{[]string{"link", "--json"}, 2},
		{[]string{"status", "--json", "--unknown"}, 2},
	} {
		t.Run(tc.args[0]+tc.args[len(tc.args)-1], func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			code := Execute(tc.args, Dependencies{In: bytes.NewReader(nil), Out: &out, Err: &diagnostics, Cwd: cwd, Home: home})
			if code != tc.code {
				t.Fatalf("code=%d, want %d; stdout=%s stderr=%s", code, tc.code, &out, &diagnostics)
			}
			data := out.Bytes()
			if tc.code != 0 {
				if out.Len() != 0 {
					t.Fatalf("failure stdout=%s", &out)
				}
				data = diagnostics.Bytes()
			} else if diagnostics.Len() != 0 {
				t.Fatalf("success stderr=%s", &diagnostics)
			}
			var envelope map[string]any
			if err := json.Unmarshal(data, &envelope); err != nil {
				t.Fatalf("invalid single JSON object: %q: %v", data, err)
			}
			if envelope["version"] != float64(1) {
				t.Fatalf("version=%v", envelope["version"])
			}
		})
	}
	if _, err := os.Stat(filepath.Join(home, "llm", "code", "repo", "archive")); !os.IsNotExist(err) {
		t.Fatalf("archive stub wrote data: %v", err)
	}
}

func TestBareRootSummarizesEnsureAndExplicitPathKeepsItsContract(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "json"}[jsonMode], func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
			t.Setenv("DOTLLM_ROOT", "")
			repo := filepath.Join(home, "repo")
			contextRepo(t, repo)
			nested := filepath.Join(repo, "src", "deep")
			if err := os.MkdirAll(nested, 0755); err != nil {
				t.Fatal(err)
			}
			args := []string{"--color", "never"}
			if jsonMode {
				args = append(args, "--json")
			}
			for i := 0; i < 2; i++ {
				code, out, diagnostics := contextCommand(t, home, nested, args...)
				if code != 0 || len(diagnostics) != 0 {
					t.Fatalf("code=%d out=%s err=%s", code, out, diagnostics)
				}
				if jsonMode {
					var envelope struct {
						Version int
						Command string
						Data    output.LinkData
					}
					if err := json.Unmarshal(out, &envelope); err != nil {
						t.Fatal(err)
					}
					if envelope.Version != 1 || envelope.Command != "init" || envelope.Data.Changed != (i == 0) || envelope.Data.Binding.State != "managed" {
						t.Fatalf("root contract: %s", out)
					}
				} else {
					prefix := "Linked "
					if i > 0 {
						prefix = "Already linked:"
					}
					if !bytes.HasPrefix(out, []byte(prefix)) || !bytes.Contains(out, []byte(".llm → ")) {
						t.Fatalf("summary: %s", out)
					}
				}
			}
			target, err := os.Readlink(filepath.Join(repo, ".llm"))
			if err != nil {
				t.Fatal(err)
			}
			code, out, diagnostics := contextCommand(t, home, nested, "path", "--color", "always")
			if code != 0 || string(out) != target+"\n" || len(diagnostics) != 0 {
				t.Fatalf("plain path: %d %q %s", code, out, diagnostics)
			}
			code, out, diagnostics = contextCommand(t, home, nested, "path", "--json")
			// Compare the exact serialized PathData envelope with the real anchor,
			// rather than accepting an accidentally widened root LinkData shape.
			anchor, err := filepath.EvalSymlinks(repo)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(output.Envelope{Version: 1, Command: "path", Data: output.PathData{Path: target, Anchor: filepath.Join(anchor, ".llm"), Changed: false}, Warnings: []string{}})
			if code != 0 || !bytes.Equal(out, append(want, '\n')) || len(diagnostics) != 0 {
				t.Fatalf("JSON path: %d %s %s want %s", code, out, diagnostics, want)
			}
		})
	}
}

func TestNewRootsDoNotShareFlagOrWorkingDirectoryState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DOTLLM_ROOT", "")
	for _, project := range []string{"first", "second"} {
		cwd := filepath.Join(home, project)
		if err := os.MkdirAll(cwd, 0755); err != nil {
			t.Fatal(err)
		}
		var out, diagnostics bytes.Buffer
		if code := Execute([]string{"init", "--scope", "cwd", "--json", "-C", cwd}, Dependencies{Out: &out, Err: &diagnostics, Home: home, Cwd: home}); code != 0 {
			t.Fatalf("code=%d: %s", code, &diagnostics)
		}
		if _, err := os.Readlink(filepath.Join(cwd, ".llm")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadOnlyCommandsAndNonGitRootDoNotInitialize(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DOTLLM_ROOT", "")
	cwd := filepath.Join(home, "project")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"status", "--json"}, {"config", "--json"}, {"config", "--path", "--json"}, {"list", "--json"}, {"path", "--json"}, {"--json"}} {
		var out, diagnostics bytes.Buffer
		code := Execute(args, Dependencies{Home: home, Cwd: cwd, Out: &out, Err: &diagnostics})
		if (args[0] == "path" || args[0] == "--json") && code != 5 {
			t.Fatalf("non-Git code=%d: %s", code, &diagnostics)
		}
		if _, err := os.Lstat(filepath.Join(home, "llm")); !os.IsNotExist(err) {
			t.Fatalf("%v initialized store: %v", args, err)
		}
	}
}

func TestPlainLinkResultDisclosesBackupAndConflicts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DOTLLM_ROOT", "")
	cwd := filepath.Join(home, "checkout")
	target := filepath.Join(home, "llm", "new")
	for _, dir := range []string{filepath.Join(cwd, ".llm"), target} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string]string{filepath.Join(cwd, ".llm", "notes.md"): "old", filepath.Join(target, "notes.md"): "new"} {
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var out, diagnostics bytes.Buffer
	if code := Execute([]string{"link", "new"}, Dependencies{Home: home, Cwd: cwd, Out: &out, Err: &diagnostics}); code != 0 {
		t.Fatalf("code=%d err=%s", code, &diagnostics)
	}
	if !bytes.Contains(out.Bytes(), []byte(".llm.bak")) || !bytes.Contains(out.Bytes(), []byte("notes.md")) {
		t.Fatalf("backup/conflict hidden: %s", &out)
	}
}
