// Package config loads effective store configuration without creating files or
// migrating data. Configuration editing is an explicit, future command.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	Version   int    `json:"version"`
	StoreRoot string `json:"store_root"`
}

func Path(home string) string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "dotllm", "config.json")
}
func Load(home string) (Config, error) {
	cfg := Config{Version: 1, StoreRoot: filepath.Join(home, "llm")}
	data, err := os.ReadFile(Path(home))
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	var stored Config
	if err := json.Unmarshal(data, &stored); err != nil {
		return cfg, fmt.Errorf("read config %s: %w", Path(home), err)
	}
	if stored.Version != 1 {
		return cfg, fmt.Errorf("unsupported config version %d in %s", stored.Version, Path(home))
	}
	if stored.StoreRoot != "" {
		cfg.StoreRoot = stored.StoreRoot
	}
	if !filepath.IsAbs(cfg.StoreRoot) {
		return cfg, fmt.Errorf("store_root must be an absolute directory in %s", Path(home))
	}
	return cfg, nil
}
