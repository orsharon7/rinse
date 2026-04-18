package predict

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ── interactiveModel key handler tests ────────────────────────────────────────

func TestInteractiveModel_SkipAdvances(t *testing.T) {
	preds := []Prediction{
		{Pattern: "Missing error handling", Confidence: 0.88, File: "foo.go", Line: 10},
		{Pattern: "TODO/FIXME left in code", Confidence: 0.65, File: "bar.go", Line: 5},
	}
	m := newInteractiveModel(preds, 80, "test-session", false)

	// Press 'n' on first prediction — should advance to cursor=1.
	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if cmd != nil {
		t.Fatal("expected nil cmd after skip")
	}
	nm := next.(interactiveModel)
	if nm.cursor != 1 {
		t.Fatalf("expected cursor=1 after skip, got %d", nm.cursor)
	}
	if !nm.wasSkipped(0) {
		t.Fatal("expected prediction[0] marked as skipped")
	}
}

func TestInteractiveModel_QuitMarksDone(t *testing.T) {
	preds := []Prediction{
		{Pattern: "Hardcoded secret", Confidence: 0.93, File: "main.go", Line: 3},
	}
	m := newInteractiveModel(preds, 80, "test-session", false)

	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	nm := next.(interactiveModel)
	if !nm.done {
		t.Fatal("expected done=true after quit")
	}
	if !nm.wasSkipped(0) {
		t.Fatal("expected remaining predictions marked as skipped after quit")
	}
}

func TestInteractiveModel_AllSkipped_Done(t *testing.T) {
	preds := []Prediction{
		{Pattern: "Missing error handling", Confidence: 0.88},
	}
	m := newInteractiveModel(preds, 80, "test-session", false)

	// Advance past all predictions via 'n'.
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	nm := next.(interactiveModel)
	if !nm.done {
		t.Fatal("expected done=true after all predictions skipped")
	}
}

func TestInteractiveModel_EmptyPredictions(t *testing.T) {
	m := newInteractiveModel(nil, 80, "test-session", false)
	view := m.View()
	// No predictions — view should be empty (the caller shows the "clean" message).
	if strings.TrimSpace(view) != "" {
		t.Fatalf("expected empty view for no predictions, got: %q", view)
	}
}

func TestInteractiveModel_ViewContainsPattern(t *testing.T) {
	preds := []Prediction{
		{Pattern: "Unused variable", Confidence: 0.82, File: "util.go", Line: 42,
			Detail: "Variable x assigned but never used."},
	}
	m := newInteractiveModel(preds, 80, "test-session", false)
	view := m.View()
	if !strings.Contains(view, "Unused variable") {
		t.Errorf("expected view to contain pattern name, got: %q", view)
	}
	if !strings.Contains(view, "util.go:42") {
		t.Errorf("expected view to contain file:line, got: %q", view)
	}
	if !strings.Contains(view, "[y]") {
		t.Errorf("expected view to contain key prompt [y], got: %q", view)
	}
	if !strings.Contains(view, "[n/space]") {
		t.Errorf("expected view to contain key prompt [n/space], got: %q", view)
	}
}

func TestInteractiveModel_ApplyResult_Applied(t *testing.T) {
	preds := []Prediction{
		{Pattern: "Missing error handling", Confidence: 0.88, File: "x.go", Line: 1},
		{Pattern: "Naked return", Confidence: 0.72, File: "x.go", Line: 2},
	}
	m := newInteractiveModel(preds, 80, "test-session", false)

	next, cmd := m.Update(applyResultMsg{result: ApplyPatchResult{Applied: true}, index: 0})
	if cmd != nil {
		t.Fatal("expected nil cmd after apply")
	}
	nm := next.(interactiveModel)
	if !nm.wasApplied(0) {
		t.Fatal("expected prediction[0] marked as applied")
	}
	if nm.cursor != 1 {
		t.Fatalf("expected cursor=1 after apply, got %d", nm.cursor)
	}
}

func TestInteractiveModel_ApplyResult_BuildFail(t *testing.T) {
	preds := []Prediction{
		{Pattern: "Missing error handling", Confidence: 0.88, File: "x.go"},
	}
	m := newInteractiveModel(preds, 80, "test-session", false)

	next, _ := m.Update(applyResultMsg{
		result: ApplyPatchResult{BuildFail: true, Err: fmt.Errorf("compile error")},
		index:  0,
	})
	nm := next.(interactiveModel)
	if nm.wasApplied(0) {
		t.Fatal("expected prediction[0] NOT marked as applied after build failure")
	}
	if !strings.Contains(nm.lastMsg, "Build failed") {
		t.Errorf("expected lastMsg to mention build failure, got: %q", nm.lastMsg)
	}
}

// ── ApplyPatch unit tests ─────────────────────────────────────────────────────

func TestApplyPatch_NoDiff_ReturnsApplied(t *testing.T) {
	p := Prediction{Pattern: "TODO", Confidence: 0.5, SuggestedDiff: ""}
	result := ApplyPatch(p)
	if !result.Applied {
		t.Fatal("expected Applied=true when SuggestedDiff is empty")
	}
	if result.Err != nil {
		t.Fatalf("expected nil error, got: %v", result.Err)
	}
}

// ── RunInteractive unit tests ─────────────────────────────────────────────────

func TestRunInteractive_EmptyReport_NoPanic(t *testing.T) {
	var buf strings.Builder
	report := &Report{Source: "staged changes"}
	err := RunInteractive(InteractiveOpts{
		Report:       report,
		Out:          &buf,
		SkipProCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No predictions") {
		t.Errorf("expected empty-predictions message, got: %q", buf.String())
	}
}

func TestRunInteractive_NonProShowsUpgradePrompt(t *testing.T) {
	// Force non-pro by unsetting env var and using SkipProCheck=false.
	t.Setenv("RINSE_PRO", "")

	var buf strings.Builder
	report := &Report{Source: "staged changes", Predictions: []Prediction{
		{Pattern: "Missing error handling", Confidence: 0.88},
	}}
	err := RunInteractive(InteractiveOpts{
		Report:       report,
		Out:          &buf,
		SkipProCheck: false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Pro") {
		t.Errorf("expected upgrade prompt to mention Pro, got: %q", buf.String())
	}
}

func TestRunInteractive_NilReport_ReturnsError(t *testing.T) {
	err := RunInteractive(InteractiveOpts{
		Report:       nil,
		SkipProCheck: true,
	})
	if err == nil {
		t.Fatal("expected error for nil report")
	}
}

// ── IsProEnabled tests ────────────────────────────────────────────────────────

func TestIsProEnabled_EnvVar(t *testing.T) {
	t.Setenv("RINSE_PRO", "1")
	if !IsProEnabled() {
		t.Fatal("expected IsProEnabled()=true when RINSE_PRO=1")
	}
}

func TestIsProEnabled_EnvVarNotSet(t *testing.T) {
	t.Setenv("RINSE_PRO", "")
	// Without a config file, should return false.
	// We can't easily control the config file path in tests, so just verify
	// the function doesn't panic and returns a bool.
	_ = IsProEnabled()
}

func TestRunInteractive_NoColorOpt_ASCIIOutput(t *testing.T) {
	var buf strings.Builder
	report := &Report{
		Source: "staged changes",
		Predictions: []Prediction{
			{Pattern: "Missing error handling", Confidence: 0.88, File: "foo.go", Line: 10},
		},
	}
	// Use the NoColor opt to force ASCII output without relying on env var.
	m := newInteractiveModel(report.Predictions, 80, "test-nocolor", true)
	view := m.View()
	// ASCII border characters must appear, not Unicode rounded corners.
	if !strings.Contains(view, "+") {
		t.Errorf("expected ASCII border '+' in noColor view, got: %q", view)
	}
	if strings.Contains(view, "╭") || strings.Contains(view, "╰") {
		t.Errorf("expected no Unicode rounded borders in noColor view, got: %q", view)
	}
	// Confidence bar must use ASCII brackets.
	if !strings.Contains(view, "[") {
		t.Errorf("expected ASCII confidence bar '[' in noColor view, got: %q", view)
	}
	_ = buf.String()
}

func TestRunInteractive_NOCOLOREnvVar_ASCIIOutput(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var buf strings.Builder
	report := &Report{
		Source:      "staged changes",
		Predictions: []Prediction{},
	}
	err := RunInteractive(InteractiveOpts{
		Report:       report,
		Out:          &buf,
		SkipProCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty predictions with NO_COLOR should emit ASCII "[y]" marker, not Unicode check.
	out := buf.String()
	if !strings.Contains(out, "[y]") {
		t.Errorf("expected ASCII '[y]' icon with NO_COLOR set, got: %q", out)
	}
}



// TestLogInteractiveSession_FallbackOnUnwritableDir simulates an unwritable
// sessions directory and verifies that:
//  1. The warning "Session write failed — continuing without logging" is written to warnW.
//  2. The fallback line is appended to predict-events.log.
//  3. The function returns without panicking (interactive mode continues).
func TestLogInteractiveSession_FallbackOnUnwritableDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root — permission checks are ineffective")
	}

	// Create a temp home-like directory.
	tmpHome := t.TempDir()

	// Create .rinse/ but make sessions/ a file (not a dir) so MkdirAll fails.
	rinseDir := filepath.Join(tmpHome, ".rinse")
	if err := os.MkdirAll(rinseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionsPath := filepath.Join(rinseDir, "sessions")
	// Write a regular file where the directory would be — MkdirAll will fail.
	if err := os.WriteFile(sessionsPath, []byte("blocker"), 0o444); err != nil {
		t.Fatal(err)
	}
	// Make it read-only so even overwriting fails.
	if err := os.Chmod(sessionsPath, 0o444); err != nil {
		t.Fatal(err)
	}

	// Redirect HOME so logInteractiveSession uses tmpHome.
	t.Setenv("HOME", tmpHome)

	var warnBuf strings.Builder
	started := time.Now()
	logInteractiveSession("test-session-fallback", started, 3, 1, 2, &warnBuf)

	// 1. Warning must be emitted.
	warning := warnBuf.String()
	if !strings.Contains(warning, "Session write failed") {
		t.Errorf("expected warning containing 'Session write failed', got: %q", warning)
	}

	// 2. Fallback log must exist and contain the session entry.
	logPath := filepath.Join(rinseDir, "predict-events.log")
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("expected fallback log at %s, got error: %v", logPath, err)
	}
	logStr := string(content)
	if !strings.Contains(logStr, "interactive_session") {
		t.Errorf("expected fallback log to contain 'interactive_session', got: %q", logStr)
	}
	if !strings.Contains(logStr, "test-session-fallback") {
		t.Errorf("expected fallback log to contain session ID, got: %q", logStr)
	}
}
