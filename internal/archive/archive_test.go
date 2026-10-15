package archive_test

import (
	"context"
	"dotllm/internal/archive"
	"dotllm/internal/contextstore"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUnimplementedLifecycleNeverMovesFixtureData(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "llm")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(root, "old.md")
	if err := os.WriteFile(fixture, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	store, err := contextstore.Open(contextstore.Config{Home: home, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	manager := archive.New(store)
	ctx := context.Background()
	_, planErr := manager.Plan(ctx, archive.Selection{})
	_, restoreErr := manager.PlanRestore(ctx, "id", nil)
	_, applyErr := manager.Apply(ctx, archive.Plan{})
	_, recoverErr := manager.Recover(ctx, "id")
	for _, err := range []error{planErr, restoreErr, applyErr, recoverErr} {
		var domain *contextstore.Error
		if !errors.As(err, &domain) || domain.Code != "unavailable" {
			t.Fatalf("unexpected lifecycle result: %v", err)
		}
	}
	data, err := os.ReadFile(fixture)
	if err != nil || string(data) != "keep" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("stub wrote extra state: %v %v", entries, err)
	}
}
