package predict

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ─── parseDiff ────────────────────────────────────────────────────────────────

func TestParseDiff_ExtractsAddedLines(t *testing.T) {
	diff := `diff --git a/foo.go b/foo.go
index 0000000..1111111 100644
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,5 @@
 package foo
+
+func Hello() string {
+	return "hello"
+}
`
	chunks := parseDiff(diff)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].file != "foo.go" {
		t.Errorf("expected file foo.go, got %q", chunks[0].file)
	}
	if !chunks[0].isGo {
		t.Error("expected isGo=true for .go file")
	}
	// 4 added lines
	if len(chunks[0].lines) != 4 {
		t.Errorf("expected 4 added lines, got %d", len(chunks[0].lines))
	}
}

func TestParseDiff_MultipleFiles(t *testing.T) {
	diff := `+++ b/a.go
+line1
+++ b/b.py
+line2
`
	chunks := parseDiff(diff)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if chunks[0].file != "a.go" || !chunks[0].isGo {
		t.Error("first chunk should be a.go (Go)")
	}
	if chunks[1].file != "b.py" || chunks[1].isGo {
		t.Error("second chunk should be b.py (non-Go)")
	}
}

// ─── detectHardcodedSecrets ───────────────────────────────────────────────────

func TestDetectHardcodedSecrets_Flags(t *testing.T) {
	chunk := &diffChunk{
		file:  "config.go",
		lines: []string{`api_key = "sk-supersecret123"`},
	}
	preds := detectHardcodedSecrets(chunk)
	if len(preds) == 0 {
		t.Fatal("expected at least one prediction for hardcoded secret")
	}
	if preds[0].Pattern != "Hardcoded secret / credential" {
		t.Errorf("unexpected pattern: %q", preds[0].Pattern)
	}
	if preds[0].Confidence < 0.9 {
		t.Errorf("expected high confidence, got %.2f", preds[0].Confidence)
	}
}

func TestDetectHardcodedSecrets_SkipsComments(t *testing.T) {
	chunk := &diffChunk{
		file:  "config.go",
		lines: []string{`// password = "hardcoded" — do not do this`},
	}
	preds := detectHardcodedSecrets(chunk)
	if len(preds) != 0 {
		t.Errorf("expected 0 predictions for comment line, got %d", len(preds))
	}
}

// ─── detectTODOsAndHacks ──────────────────────────────────────────────────────

func TestDetectTODOs(t *testing.T) {
	chunk := &diffChunk{
		file:  "main.go",
		lines: []string{"// TODO: fix this later", "x := 1"},
	}
	preds := detectTODOsAndHacks(chunk)
	if len(preds) != 1 {
		t.Fatalf("expected 1 prediction, got %d", len(preds))
	}
	if preds[0].Pattern != "TODO/FIXME left in code" {
		t.Errorf("unexpected pattern: %q", preds[0].Pattern)
	}
}

// ─── detectLongFunctions ──────────────────────────────────────────────────────

func TestDetectLongFunctions_Triggers(t *testing.T) {
	lines := make([]string, 65)
	for i := range lines {
		lines[i] = "x := 1"
	}
	chunk := &diffChunk{file: "big.go", lines: lines}
	preds := detectLongFunctions(chunk)
	if len(preds) == 0 {
		t.Fatal("expected prediction for long diff chunk")
	}
}

func TestDetectLongFunctions_BelowThreshold(t *testing.T) {
	lines := make([]string, 30)
	chunk := &diffChunk{file: "small.go", lines: lines}
	preds := detectLongFunctions(chunk)
	if len(preds) != 0 {
		t.Errorf("expected 0 predictions for short diff, got %d", len(preds))
	}
}

// ─── detectMissingErrorHandling (text heuristic) ──────────────────────────────

func TestDetectMissingErrorHandling_BlankIdent(t *testing.T) {
	src := `package p
func f() {
	_, _ = doSomethingWithErr()
}
`
	preds := detectMissingErrorHandling("p.go", src)
	// The regex looks for ",_" discarding an err-like return.
	// This test just checks the function does not panic and returns slice.
	_ = preds
}

// ─── detectNakedReturns ───────────────────────────────────────────────────────

func TestDetectNakedReturns_Flags(t *testing.T) {
	src := `package p

func compute() (result int, err error) {
	result = 42
	return
}
`
	preds := detectNakedReturns("p.go", src)
	if len(preds) == 0 {
		t.Fatal("expected naked return prediction")
	}
	if preds[0].Pattern != "Naked return in long function" {
		t.Errorf("unexpected pattern: %q", preds[0].Pattern)
	}
}

func TestDetectNakedReturns_NoFlag_ExplicitReturn(t *testing.T) {
	src := `package p

func compute() (result int, err error) {
	return 42, nil
}
`
	preds := detectNakedReturns("p.go", src)
	if len(preds) != 0 {
		t.Errorf("expected 0 predictions for explicit return, got %d", len(preds))
	}
}

// ─── sortByConfidence ────────────────────────────────────────────────────────

func TestSortByConfidence(t *testing.T) {
	preds := []Prediction{
		{Pattern: "A", Confidence: 0.5},
		{Pattern: "B", Confidence: 0.9},
		{Pattern: "C", Confidence: 0.7},
	}
	sortByConfidence(preds)
	if preds[0].Confidence < preds[1].Confidence || preds[1].Confidence < preds[2].Confidence {
		t.Errorf("predictions not sorted descending: %.2f %.2f %.2f",
			preds[0].Confidence, preds[1].Confidence, preds[2].Confidence)
	}
}

// ─── LogEvent ─────────────────────────────────────────────────────────────────

func TestLogEvent_WritesToSessionsDir(t *testing.T) {
	// Override the home dir used by LogEvent by setting HOME temporarily.
	tmp := t.TempDir()
	orig := os.Getenv("HOME")
	os.Setenv("HOME", tmp)
	defer os.Setenv("HOME", orig)

	report := &Report{
		Source:      "staged changes",
		GeneratedAt: time.Now(),
		Predictions: []Prediction{
			{Pattern: "Hardcoded secret / credential", Confidence: 0.93, File: "cfg.go", Line: 5,
				Detail: "Move to env vars."},
		},
	}
	if err := LogEvent(report); err != nil {
		t.Fatalf("LogEvent error: %v", err)
	}

	// The event must land in ~/.rinse/sessions/ as a JSON file.
	sessDir := tmp + "/.rinse/sessions"
	entries, err := os.ReadDir(sessDir)
	if err != nil {
		t.Fatalf("sessions dir not created: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 session file, got %d", len(entries))
	}
	name := entries[0].Name()
	if !strings.HasPrefix(name, "predict-") || !strings.HasSuffix(name, ".json") {
		t.Errorf("unexpected session file name: %s", name)
	}
	data, err := os.ReadFile(sessDir + "/" + name)
	if err != nil {
		t.Fatalf("cannot read session file: %v", err)
	}
	if !strings.Contains(string(data), "predict_generated") {
		t.Errorf("session file missing event_type; got: %s", data)
	}
	if !strings.Contains(string(data), "hardcoded_secret") {
		t.Errorf("session file missing pattern_id; got: %s", data)
	}
	if !strings.Contains(string(data), "cfg.go") {
		t.Errorf("session file missing file reference; got: %s", data)
	}
}

func TestLogEvent_FallsBackToFlatLogWhenSessionsDirFails(t *testing.T) {
	// Point HOME at a read-only location to force sessions dir creation to fail,
	// which should trigger the fallback to predict-events.log.
	tmp := t.TempDir()
	orig := os.Getenv("HOME")
	os.Setenv("HOME", tmp)
	defer os.Setenv("HOME", orig)

	// Pre-create ~/.rinse/sessions as a *file* (not a dir) so MkdirAll fails.
	rinseDir := tmp + "/.rinse"
	if err := os.MkdirAll(rinseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessPath := rinseDir + "/sessions"
	if err := os.WriteFile(sessPath, []byte("blocker"), 0o644); err != nil {
		t.Fatal(err)
	}

	report := &Report{
		Source:      "staged changes",
		GeneratedAt: time.Now(),
		Predictions: []Prediction{
			{Pattern: "TODO/FIXME left in code", Confidence: 0.65, File: "main.go", Line: 12},
		},
	}
	// Should not return an error — fallback is silent best-effort.
	if err := LogEvent(report); err != nil {
		t.Fatalf("LogEvent fallback returned error: %v", err)
	}

	// Fallback log must exist and contain the event.
	logPath := rinseDir + "/predict-events.log"
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("fallback log not created: %v", err)
	}
	if !strings.Contains(string(data), "TODO") {
		t.Errorf("fallback log missing pattern; got: %s", data)
	}
}

func TestLogEvent_NilReport(t *testing.T) {
	if err := LogEvent(nil); err != nil {
		t.Errorf("LogEvent(nil) should return nil, got: %v", err)
	}
}

// ─── No-mutation contract ────────────────────────────────────────────────────

// TestRun_NoMutationContract verifies that predict.Run() is strictly read-only
// with respect to the working tree. It creates a temp directory with a fake
// git repo containing a staged Go file, records a SHA-256 snapshot of every
// file in the tree before calling Run, then asserts the snapshot is identical
// after Run returns.
//
// This test enforces the invariant documented on Run() and the QA requirement
// from RIN-171: "No code mutation in v0.3 — Report Mode predicts only. Zero
// writes to the working tree."
func TestRun_NoMutationContract(t *testing.T) {
	// Build a minimal git repo in a temp dir so Run(0, "") can call
	// git diff --cached without error.
	dir := t.TempDir()

	// git init
	if err := runCmd(dir, "git", "init", "-b", "main"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	// Configure identity so git is happy.
	_ = runCmd(dir, "git", "config", "user.email", "test@rinse.test")
	_ = runCmd(dir, "git", "config", "user.name", "Test")

	// Write a Go file with a deliberate issue (ignored error) and stage it.
	goFile := filepath.Join(dir, "main.go")
	goSrc := `package main

import "os"

func main() {
	_, _ = os.Open("/tmp/x") // ignored error
}
`
	if err := os.WriteFile(goFile, []byte(goSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runCmd(dir, "git", "add", "main.go"); err != nil {
		t.Fatal(err)
	}

	// Snapshot the directory before Run.
	snapshotBefore := dirSnapshot(t, dir)

	// Run predict in staged-diff mode from inside the temp repo.
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	// Run should succeed (or return an error — either way, no writes).
	report, runErr := Run(0, "")
	_ = runErr  // non-zero exit is acceptable; what matters is the working tree
	_ = report

	// Restore cwd before snapshot to ensure consistent paths.
	if err := os.Chdir(orig); err != nil {
		t.Fatal(err)
	}

	// Snapshot the directory after Run.
	snapshotAfter := dirSnapshot(t, dir)

	// Compare — the working tree must be byte-for-byte identical.
	if len(snapshotBefore) != len(snapshotAfter) {
		t.Fatalf("predict.Run() mutated the working tree: file count changed (%d → %d)", len(snapshotBefore), len(snapshotAfter))
	}
	for path, hashBefore := range snapshotBefore {
		hashAfter, ok := snapshotAfter[path]
		if !ok {
			t.Errorf("predict.Run() deleted file: %s", path)
			continue
		}
		if hashBefore != hashAfter {
			t.Errorf("predict.Run() modified file: %s", path)
		}
	}
	for path := range snapshotAfter {
		if _, ok := snapshotBefore[path]; !ok {
			t.Errorf("predict.Run() created unexpected file: %s", path)
		}
	}
}

// dirSnapshot returns a map[relPath]sha256hex for every file under root,
// excluding .git internals that git itself mutates (e.g. FETCH_HEAD, index).
func dirSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snap := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		// Skip git internal files that git itself rewrites during operations.
		if strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		h := sha256.Sum256(data)
		snap[rel] = fmt.Sprintf("%x", h)
		return nil
	})
	if err != nil {
		t.Fatalf("dirSnapshot: %v", err)
	}
	return snap
}

// runCmd executes a command in dir, returning any error.
func runCmd(dir string, name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Dir = dir
	return c.Run()
}
