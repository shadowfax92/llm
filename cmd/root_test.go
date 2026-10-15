package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
		{[]string{"init", "--json"}, 0},
		{[]string{"status", "--json"}, 0},
		{[]string{"list", "--json"}, 0},
		{[]string{"archive", "--json"}, 5},
		{[]string{"restore", "--json"}, 5},
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
		if code := Execute([]string{"init", "--json", "-C", cwd}, Dependencies{Out: &out, Err: &diagnostics, Home: home, Cwd: home}); code != 0 {
			t.Fatalf("code=%d: %s", code, &diagnostics)
		}
		if _, err := os.Readlink(filepath.Join(cwd, ".llm")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadOnlyCommandsAndUnavailableRootDoNotInitialize(t *testing.T) {
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
			t.Fatalf("stub code=%d: %s", code, &diagnostics)
		}
		if _, err := os.Lstat(filepath.Join(home, "llm")); !os.IsNotExist(err) {
			t.Fatalf("%v initialized store: %v", args, err)
		}
	}
}
