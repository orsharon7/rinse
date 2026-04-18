package predict

import (
	"os"
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

func TestLogEvent_WritesToFile(t *testing.T) {
	// Override the home dir used by LogEvent by setting HOME temporarily.
	tmp := t.TempDir()
	orig := os.Getenv("HOME")
	os.Setenv("HOME", tmp)
	defer os.Setenv("HOME", orig)

	report := &Report{
		Source:      "staged changes",
		GeneratedAt: time.Now(),
		Predictions: []Prediction{
			{Pattern: "Hardcoded secret / credential", Confidence: 0.93, File: "cfg.go", Line: 5},
		},
	}
	if err := LogEvent(report); err != nil {
		t.Fatalf("LogEvent error: %v", err)
	}

	logPath := tmp + "/.rinse/predict-events.log"
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("log file not created: %v", err)
	}
	if !strings.Contains(string(data), "Hardcoded secret") {
		t.Errorf("log does not contain expected pattern; got: %s", data)
	}
}

func TestLogEvent_NilReport(t *testing.T) {
	if err := LogEvent(nil); err != nil {
		t.Errorf("LogEvent(nil) should return nil, got: %v", err)
	}
}
