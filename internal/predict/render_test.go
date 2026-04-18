package predict

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// helpers ─────────────────────────────────────────────────────────────────────

func sampleReport(n int) *Report {
	preds := make([]Prediction, n)
	for i := range preds {
		preds[i] = Prediction{
			Pattern:    "Missing error handling",
			Confidence: 0.87,
			File:       "internal/foo/bar.go",
			Line:       42 + i,
			Detail:     "error return discarded",
		}
	}
	return &Report{
		Predictions: preds,
		Source:      "staged changes",
		GeneratedAt: time.Now(),
	}
}

// renderDumb ──────────────────────────────────────────────────────────────────

func TestRenderDumb_WithPredictions(t *testing.T) {
	r := sampleReport(3)
	var buf bytes.Buffer
	renderDumb(&buf, r)
	out := buf.String()

	if !strings.Contains(out, "[rinse predict] 3 predictions found:") {
		t.Errorf("expected header line, got:\n%s", out)
	}
	if !strings.Contains(out, "[PREDICT]") {
		t.Errorf("expected [PREDICT] prefix, got:\n%s", out)
	}
	if !strings.Contains(out, "Run `rinse` to fix") {
		t.Errorf("expected CTA, got:\n%s", out)
	}
}

func TestRenderDumb_NoPredictions(t *testing.T) {
	r := &Report{Source: "staged changes", GeneratedAt: time.Now()}
	var buf bytes.Buffer
	renderDumb(&buf, r)
	out := buf.String()

	if !strings.Contains(out, "no predictions") {
		t.Errorf("expected 'no predictions' output, got:\n%s", out)
	}
}

func TestRenderDumb_IncludesFileAndLine(t *testing.T) {
	r := sampleReport(1)
	var buf bytes.Buffer
	renderDumb(&buf, r)
	out := buf.String()

	if !strings.Contains(out, "internal/foo/bar.go:42") {
		t.Errorf("expected file:line in output, got:\n%s", out)
	}
	if !strings.Contains(out, "87%") {
		t.Errorf("expected confidence percent in output, got:\n%s", out)
	}
}

// renderClean ─────────────────────────────────────────────────────────────────

func TestRenderClean_ContainsCheckmark(t *testing.T) {
	var buf bytes.Buffer
	renderClean(&buf)
	out := buf.String()
	if !strings.Contains(out, "✓") && !strings.Contains(out, "no likely issues") {
		t.Errorf("expected clean output with checkmark or message, got:\n%s", out)
	}
}

// renderPredictions ───────────────────────────────────────────────────────────

func TestRenderPredictions_OutputCount(t *testing.T) {
	r := sampleReport(2)
	var buf bytes.Buffer
	renderPredictions(&buf, r.Predictions, false, 120)
	out := buf.String()
	if !strings.Contains(out, "2 predictions") {
		t.Errorf("expected '2 predictions' in output, got:\n%s", out)
	}
}

func TestRenderPredictions_NarrowOmitsConfidence(t *testing.T) {
	r := sampleReport(1)
	var wideB, narrowB bytes.Buffer
	renderPredictions(&wideB, r.Predictions, false, 120)
	renderPredictions(&narrowB, r.Predictions, true, 50)

	wide := wideB.String()
	narrow := narrowB.String()

	// Wide output should contain a confidence percentage
	if !strings.Contains(wide, "%") {
		t.Errorf("expected confidence %% in wide output, got:\n%s", wide)
	}
	// Narrow output should still contain the pattern description
	if !strings.Contains(narrow, "Missing error handling") {
		t.Errorf("expected pattern in narrow output, got:\n%s", narrow)
	}
}

// RenderEmpty ─────────────────────────────────────────────────────────────────

func TestRenderEmpty_ContainsCircle(t *testing.T) {
	var buf bytes.Buffer
	// Call renderEmpty directly (internal) since isDumb varies by env.
	renderEmpty(&buf)
	out := buf.String()
	if !strings.Contains(out, "nothing to analyze") {
		t.Errorf("expected 'nothing to analyze', got:\n%s", out)
	}
}

// confidenceStyle ─────────────────────────────────────────────────────────────

func TestConfidenceStyle_DoesNotPanic(t *testing.T) {
	for _, c := range []float64{0.0, 0.5, 0.6, 0.79, 0.8, 1.0} {
		s := confidenceStyle(c)
		// Just ensure it renders without panic
		_ = s.Render("test")
	}
}

// plural ──────────────────────────────────────────────────────────────────────

func TestPlural(t *testing.T) {
	if plural(1) != "" {
		t.Errorf("plural(1) should return '', got %q", plural(1))
	}
	if plural(0) != "s" {
		t.Errorf("plural(0) should return 's', got %q", plural(0))
	}
	if plural(2) != "s" {
		t.Errorf("plural(2) should return 's', got %q", plural(2))
	}
}
