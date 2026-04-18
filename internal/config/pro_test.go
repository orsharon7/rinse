package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeProConfig writes a minimal ~/.rinse/config.json in a temp HOME dir.
func writeProConfig(t *testing.T, content map[string]interface{}) (cleanup func()) {
	t.Helper()
	dir := t.TempDir()
	rinseDir := filepath.Join(dir, ".rinse")
	if err := os.MkdirAll(rinseDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rinseDir, "config.json"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	orig := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	return func() { os.Setenv("HOME", orig) }
}

func TestIsPro_MissingFile(t *testing.T) {
	dir := t.TempDir()
	orig := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", orig)

	if IsPro() {
		t.Error("IsPro() = true, want false when config file is missing")
	}
}

func TestIsPro_ProFalse(t *testing.T) {
	cleanup := writeProConfig(t, map[string]interface{}{"pro": false})
	defer cleanup()

	if IsPro() {
		t.Error("IsPro() = true, want false when pro=false")
	}
}

func TestIsPro_ProAbsent(t *testing.T) {
	cleanup := writeProConfig(t, map[string]interface{}{"other_key": "value"})
	defer cleanup()

	if IsPro() {
		t.Error("IsPro() = true, want false when pro key is absent")
	}
}

func TestIsPro_ProTrue(t *testing.T) {
	cleanup := writeProConfig(t, map[string]interface{}{"pro": true})
	defer cleanup()

	if !IsPro() {
		t.Error("IsPro() = false, want true when pro=true")
	}
}

func TestIsPro_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	rinseDir := filepath.Join(dir, ".rinse")
	_ = os.MkdirAll(rinseDir, 0o755)
	_ = os.WriteFile(filepath.Join(rinseDir, "config.json"), []byte("not-json"), 0o644)
	orig := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", orig)

	if IsPro() {
		t.Error("IsPro() = true, want false on invalid JSON")
	}
}
