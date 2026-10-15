//go:build darwin || linux

package archive_test

import (
	"context"
	"dotllm/internal/archive"
	"dotllm/internal/contextstore"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPlanRejectsSpecialFilesWithoutOpeningThem(t *testing.T) {
	f := setup(t)
	old := time.Now().Add(-90 * 24 * time.Hour)
	for _, name := range []string{"pipe", "tree/pipe"} {
		p := filepath.Join(f.project.Root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(p, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(filepath.Join(f.project.Root, "tree"), old, old); err != nil {
		t.Fatal(err)
	}
	plan, err := f.manager.Plan(context.Background(), archive.Selection{Projects: []contextstore.Project{f.project}, OlderThan: time.Now().Add(-30 * 24 * time.Hour)})
	if err != nil || len(plan.Entries) != 0 || !strings.Contains(strings.Join(plan.Warnings, "\n"), "special file") {
		t.Fatalf("plan=%+v %v", plan, err)
	}
}
