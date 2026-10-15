package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Exercise the process adapter, where injected IsTTY tests cannot catch an
// incorrect descriptor check. /dev/null is a character device, but never a TTY.
func TestBinaryNeverStartsEditorOrPickerForNullDevices(t *testing.T) {
	home := t.TempDir()
	binary := filepath.Join(home, "dotllm")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	binDir := filepath.Join(home, "bin")
	if err := os.Mkdir(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(home, "interactive-started")
	script := []byte("#!/bin/sh\nprintf invoked > \"$DOTLLM_TEST_SENTINEL\"\n")
	for _, name := range []string{"editor", "fzf"} {
		if err := os.WriteFile(filepath.Join(binDir, name), script, 0700); err != nil {
			t.Fatal(err)
		}
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	for _, args := range [][]string{{"config", "edit"}, {"config", "edit", "--no-input"}, {"config", "edit", "--json"}, {"link", "--pick"}, {"link", "--pick", "--no-input"}} {
		var diagnostics bytes.Buffer
		child := exec.Command(binary, args...)
		child.Dir = home
		child.Stdin = null
		child.Stdout = null
		child.Stderr = &diagnostics
		child.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, "settings"), "DOTLLM_ROOT=", "VISUAL=", "EDITOR="+filepath.Join(binDir, "editor"), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"), "DOTLLM_TEST_SENTINEL="+sentinel)
		err := child.Run()
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 2 {
			t.Fatalf("%v: err=%v stderr=%s", args, err, &diagnostics)
		}
		if _, err := os.Lstat(sentinel); !os.IsNotExist(err) {
			t.Fatalf("%v started an interactive child: %v", args, err)
		}
		for _, path := range []string{filepath.Join(home, "llm"), filepath.Join(home, "settings")} {
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("%v created %s: %v", args, path, err)
			}
		}
	}
}
