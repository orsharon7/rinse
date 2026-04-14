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

// RepoConfig stores per-repository settings so switching repos restores the right values.
type RepoConfig struct {
	Path      string `json:"path"`
	Runner    int    `json:"runner"`
	Model     string `json:"model"`
	Reflect   bool   `json:"reflect"`
	Branch    string `json:"reflect_branch"`
	AutoMerge bool   `json:"auto_merge"`
}

// FullConfig is the on-disk structure combining global + per-repo settings.
type FullConfig struct {
	LastRepo string                `json:"last_repo"`
	Repos    map[string]RepoConfig `json:"repos,omitempty"`
}

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.Getenv("HOME")
	}
	return filepath.Join(dir, "pr-review", "config.json")
}

// LoadConfig reads the saved config. Returns a zero-value Config on any error.
// Supports both the new per-repo format and the old flat format; SaveConfig writes the new format.
func LoadConfig() Config {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return Config{}
	}

	// Try new format first
	var full FullConfig
	if err := json.Unmarshal(data, &full); err == nil && full.Repos != nil {
		cfg := Config{LastRepo: full.LastRepo}
		if rc, ok := full.Repos[full.LastRepo]; ok {
			cfg.LastPath = rc.Path
			cfg.LastRunner = rc.Runner
			cfg.LastModel = rc.Model
			cfg.LastReflect = rc.Reflect
			cfg.LastBranch = rc.Branch
			cfg.LastAutoMerge = rc.AutoMerge
		}
		// Guard against out-of-range runner index
		if cfg.LastRunner < 0 || cfg.LastRunner >= len(runners) {
			cfg.LastRunner = 0
		}
		return cfg
	}

	// Fall back to old flat format (migration path)
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}
	}
	if cfg.LastRunner < 0 || cfg.LastRunner >= len(runners) {
		cfg.LastRunner = 0
	}
	return cfg
}

// LoadRepoConfig loads settings for a specific repo from the per-repo store.
func LoadRepoConfig(repo string) (RepoConfig, bool) {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return RepoConfig{}, false
	}
	var full FullConfig
	if err := json.Unmarshal(data, &full); err != nil {
		return RepoConfig{}, false
	}
	if full.Repos == nil {
		return RepoConfig{}, false
	}
	rc, ok := full.Repos[repo]
	return rc, ok
}

// SaveConfig writes the config atomically, storing per-repo settings keyed by repo name.
func SaveConfig(cfg Config) {
	// Don't write a per-repo entry when no repo has been detected.
	if cfg.LastRepo == "" {
		return
	}
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}

	// Load existing full config to preserve other repos' settings
	var full FullConfig
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &full)
	}
	if full.Repos == nil {
		full.Repos = make(map[string]RepoConfig)
	}

	full.LastRepo = cfg.LastRepo
	full.Repos[cfg.LastRepo] = RepoConfig{
		Path:      cfg.LastPath,
		Runner:    cfg.LastRunner,
		Model:     cfg.LastModel,
		Reflect:   cfg.LastReflect,
		Branch:    cfg.LastBranch,
		AutoMerge: cfg.LastAutoMerge,
	}

	data, err := json.MarshalIndent(full, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}
