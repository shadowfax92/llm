// Package config loads effective store configuration without creating files or
// migrating data. Only explicit Create prepares a file for the command editor.
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
	if base == "" || !filepath.IsAbs(base) {
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

// Create prepares an absent versioned config for an explicit editor invocation.
// Exclusive creation preserves an existing file, including a raced writer; it
// never changes store roots or migrates data as a side effect of loading.
func Create(home string) (string, error) {
	path := Path(home)
	if _, err := os.Lstat(path); err == nil {
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(Config{Version: 1, StoreRoot: filepath.Join(home, "llm")}, "", "  ")
	if err == nil {
		_, err = f.Write(append(data, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	return path, err
}
