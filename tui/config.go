package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config stores the last-used wizard settings so repeated runs need fewer keystrokes.
type Config struct {
	LastRepo      string `json:"last_repo"`
	LastPath      string `json:"last_path"`
	LastRunner    int    `json:"last_runner"`
	LastModel     string `json:"last_model"`
	LastReflect   bool   `json:"last_reflect"`
	LastBranch    string `json:"last_reflect_branch"`
	LastAutoMerge bool   `json:"last_auto_merge"`
}

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.Getenv("HOME")
	}
	return filepath.Join(dir, "pr-review", "config.json")
}

// LoadConfig reads the saved config. Returns a zero-value Config on any error.
func LoadConfig() Config {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return Config{}
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}
	}
	// Guard against out-of-range runner index
	if cfg.LastRunner < 0 || cfg.LastRunner >= len(runners) {
		cfg.LastRunner = 0
	}
	return cfg
}

// SaveConfig writes the config atomically (temp file → rename).
func SaveConfig(cfg Config) {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}
