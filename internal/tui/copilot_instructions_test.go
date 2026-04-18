package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeReader simulates user input for testing.
type fakeReader struct {
	response string
}

func (f *fakeReader) ReadString(_ byte) (string, error) {
	return f.response + "\n", nil
}

func TestDetectPrimaryLanguage(t *testing.T) {
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir to tempdir: %v", err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Logf("chdir restore: %v", err)
		}
	}()

	// No marker files → unknown.
	if lang := detectPrimaryLanguage(); lang != langUnknown {
		t.Errorf("expected unknown, got %s", lang)
	}

	// go.mod → Go.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if lang := detectPrimaryLanguage(); lang != langGo {
		t.Errorf("expected go, got %s", lang)
	}

	// tsconfig.json beats package.json (checked first).
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write tsconfig.json: %v", err)
	}
	os.Remove(filepath.Join(dir, "go.mod"))
	if lang := detectPrimaryLanguage(); lang != langTypeScript {
		t.Errorf("expected typescript, got %s", lang)
	}
}

func TestCopilotInstructionsTemplate(t *testing.T) {
	cases := []struct {
		lang    detectedLanguage
		wantSub string
	}{
		{langGo, "Go-Specific"},
		{langTypeScript, "TypeScript-Specific"},
		{langJavaScript, "JavaScript-Specific"},
		{langPython, "Python-Specific"},
		{langRust, "Rust-Specific"},
		{langUnknown, "# Copilot Code Review Instructions"},
	}
	for _, c := range cases {
		tmpl := copilotInstructionsTemplate(c.lang)
		if !strings.Contains(tmpl, c.wantSub) {
			t.Errorf("lang %s: template missing %q", c.lang, c.wantSub)
		}
		if !strings.Contains(tmpl, "Focus Areas") {
			t.Errorf("lang %s: template missing shared Focus Areas section", c.lang)
		}
	}
}

func TestRunInitCopilotInstructions_UserAccepts(t *testing.T) {
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir to tempdir: %v", err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Logf("chdir restore: %v", err)
		}
	}()

	// Create go.mod so language detection returns Go.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	// Simulate user pressing enter (default = yes).
	if err := RunInitCopilotInstructions(&fakeReader{""}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, ".github", "copilot-instructions.md"))
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}

	if !strings.Contains(string(content), "Go-Specific") {
		t.Errorf("expected Go-Specific section in output, got:\n%s", content)
	}
}

func TestRunInitCopilotInstructions_UserDeclines(t *testing.T) {
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir to tempdir: %v", err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Logf("chdir restore: %v", err)
		}
	}()

	if err := RunInitCopilotInstructions(&fakeReader{"n"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(dir, ".github", "copilot-instructions.md")); !os.IsNotExist(statErr) {
		t.Error("file should not have been created when user declines")
	}
}

func TestRunInitCopilotInstructions_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir to tempdir: %v", err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Logf("chdir restore: %v", err)
		}
	}()

	// Pre-create the file.
	if err := os.MkdirAll(filepath.Join(dir, ".github"), 0o755); err != nil {
		t.Fatalf("mkdir .github: %v", err)
	}
	existing := filepath.Join(dir, ".github", "copilot-instructions.md")
	if err := os.WriteFile(existing, []byte("existing content"), 0o644); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	if err := RunInitCopilotInstructions(&fakeReader{""}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, _ := os.ReadFile(existing)
	if string(content) != "existing content" {
		t.Error("existing file should not have been overwritten")
	}
}
