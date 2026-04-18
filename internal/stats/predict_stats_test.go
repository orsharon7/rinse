package stats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writePredictEvent writes a predict_generated event JSON file into dir.
func writePredictEvent(t *testing.T, dir string, ev predictEvent) {
	t.Helper()
	data, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		t.Fatalf("writePredictEvent: marshal: %v", err)
	}
	// Build a safe filename from the source + timestamp.
	safe := "predict-test-" + ev.GeneratedAt
	sanitised := ""
	for _, c := range safe {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			sanitised += string(c)
		} else {
			sanitised += "_"
		}
	}
	sanitised += ".json"
	if err := os.WriteFile(filepath.Join(dir, sanitised), data, 0o644); err != nil {
		t.Fatalf("writePredictEvent: write: %v", err)
	}
}

func TestComputePredictHitRate_Empty(t *testing.T) {
	results := computePredictHitRate(nil, nil)
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestComputePredictHitRate_NoSessions(t *testing.T) {
	now := time.Now().UTC()
	events := []predictEvent{
		{
			EventType:   "predict_generated",
			Source:      "staged changes",
			GeneratedAt: now.Format(time.RFC3339),
			Predictions: []predictEventEntry{
				{PatternID: "missing_error_handling"},
				{PatternID: "todo_fixme_left_in_code"},
			},
		},
	}
	results := computePredictHitRate(events, nil)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Generated != 2 {
		t.Errorf("Generated: want 2, got %d", r.Generated)
	}
	if r.Matched != 0 {
		t.Errorf("Matched: want 0, got %d (no sessions to match against)", r.Matched)
	}
}

func TestComputePredictHitRate_MatchedSession(t *testing.T) {
	now := time.Now().UTC()
	events := []predictEvent{
		{
			EventType:   "predict_generated",
			Source:      "staged changes",
			GeneratedAt: now.Format(time.RFC3339),
			Predictions: []predictEventEntry{
				{PatternID: "missing_error_handling"},
				{PatternID: "todo_fixme_left_in_code"},
				{PatternID: "hardcoded_secret___credential"},
			},
		},
	}

	// Session started 2 min after the predict event, with 2 of 3 patterns.
	sessions := []Session{
		{
			StartedAt: now.Add(2 * time.Minute),
			Patterns:  []string{"Missing error handling", "TODO/FIXME left in code"},
		},
	}

	results := computePredictHitRate(events, sessions)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Generated != 3 {
		t.Errorf("Generated: want 3, got %d", r.Generated)
	}
	if r.Matched != 2 {
		t.Errorf("Matched: want 2, got %d", r.Matched)
	}
}

func TestComputePredictHitRate_SessionTooLate(t *testing.T) {
	now := time.Now().UTC()
	events := []predictEvent{
		{
			EventType:   "predict_generated",
			Source:      "staged changes",
			GeneratedAt: now.Format(time.RFC3339),
			Predictions: []predictEventEntry{
				{PatternID: "missing_error_handling"},
			},
		},
	}

	// Session started 20 min after — outside the 10-minute window.
	sessions := []Session{
		{
			StartedAt: now.Add(20 * time.Minute),
			Patterns:  []string{"Missing error handling"},
		},
	}

	results := computePredictHitRate(events, sessions)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Matched != 0 {
		t.Errorf("Matched: want 0 (session too late), got %d", results[0].Matched)
	}
}

func TestComputePredictHitRate_Rolling10(t *testing.T) {
	// 12 events, all with 1 prediction.
	// Events 0 and 1 have no matching session; events 2–11 each match.
	now := time.Now().UTC()
	events := make([]predictEvent, 12)
	sessions := make([]Session, 12)
	for i := range events {
		ts := now.Add(time.Duration(i) * time.Hour)
		events[i] = predictEvent{
			EventType:   "predict_generated",
			Source:      "staged changes",
			GeneratedAt: ts.Format(time.RFC3339),
			Predictions: []predictEventEntry{
				{PatternID: "missing_error_handling"},
			},
		}
		sessions[i] = Session{
			StartedAt: ts.Add(1 * time.Minute),
			Patterns:  []string{"Missing error handling"},
		}
	}
	// Place first two sessions outside the correlation window.
	sessions[0].StartedAt = now.Add(-1 * time.Hour)
	sessions[1].StartedAt = now.Add(-1 * time.Hour)

	results := computePredictHitRate(events, sessions)
	if len(results) != 12 {
		t.Fatalf("expected 12 results, got %d", len(results))
	}

	// Rolling 10 = last 10 results (indices 2–11), all matched.
	last10 := results[2:]
	var gen, match int
	for _, r := range last10 {
		gen += r.Generated
		match += r.Matched
	}
	if gen != 10 {
		t.Errorf("rolling 10 generated: want 10, got %d", gen)
	}
	if match != 10 {
		t.Errorf("rolling 10 matched: want 10, got %d", match)
	}
}

func TestPrintPredictStats_EmptyDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("RINSE_SESSIONS_DIR", filepath.Join(tmp, "sessions"))

	// Should not panic on empty/missing sessions dir.
	PrintPredictStats()
}

func TestLoadPredictEvents_Roundtrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("RINSE_SESSIONS_DIR", tmp)

	now := time.Now().UTC()
	ev := predictEvent{
		EventType:   "predict_generated",
		Source:      "test",
		GeneratedAt: now.Format(time.RFC3339),
		Predictions: []predictEventEntry{
			{PatternID: "missing_error_handling", Confidence: 0.88},
		},
	}
	writePredictEvent(t, tmp, ev)

	loaded, err := loadPredictEvents()
	if err != nil {
		t.Fatalf("loadPredictEvents: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 event, got %d", len(loaded))
	}
	if loaded[0].EventType != "predict_generated" {
		t.Errorf("EventType: want predict_generated, got %q", loaded[0].EventType)
	}
}

func TestPatternID(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Missing error handling", "missing_error_handling"},
		{"TODO/FIXME left in code", "todo_fixme_left_in_code"},
		{"Naked return in long function", "naked_return_in_long_function"},
	}
	for _, c := range cases {
		got := patternID(c.in)
		if got != c.want {
			t.Errorf("patternID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
