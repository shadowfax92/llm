package config_test

import (
	"dotllm/internal/config"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigIsReadOnlyAndRejectsMalformedOrUnversionedFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "settings"))
	cfg, err := config.Load(home)
	if err != nil || cfg.StoreRoot != filepath.Join(home, "llm") {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
	path := config.Path(home)
	if _, err := os.Lstat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("load created config directory: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{"{", `{}`, `{"version":2}`, `{"version":1,"store_root":"relative"}`} {
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := config.Load(home); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	data := `{"version":1,"store_root":"/tmp/dotllm-test-store"}`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load(home)
	if err != nil || cfg.StoreRoot != "/tmp/dotllm-test-store" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}
