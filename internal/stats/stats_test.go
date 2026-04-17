package stats_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/orsharon7/rinse/internal/stats"
)

// writeSession writes a raw JSON file directly into dir without going through
// Save (which requires opt-in / TTY checks).
func writeSession(t *testing.T, dir string, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatalf("writeSession: %v", err)
	}
}

// overrideSessionsDir temporarily overrides the sessions directory used by Load
// by monkey-patching HOME so that SessionsDir() resolves to our temp dir.
// It returns the sessions directory path and a cleanup function.
func overrideSessionsDir(t *testing.T) (string, func()) {
	t.Helper()
	tmpHome := t.TempDir()
	sessDir := filepath.Join(tmpHome, ".rinse", "sessions")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	return sessDir, func() { os.Setenv("HOME", origHome) }
}

// ── Load() migration tests ────────────────────────────────────────────────────

func TestLoad_MigratesLegacyApprovedTrue(t *testing.T) {
	dir, cleanup := overrideSessionsDir(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	raw := map[string]interface{}{
		"started_at": now.Format(time.RFC3339),
		"ended_at":   now.Add(time.Minute).Format(time.RFC3339),
		"repo":       "owner/repo",
		"pr":         "1",
		"approved":   true,
		// no "outcome" field
	}
	data, _ := json.Marshal(raw)
	writeSession(t, dir, "20060102-150405-1-owner-repo-PR1.json", data)

	sessions, err := stats.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if got := sessions[0].Outcome; got != stats.OutcomeApproved {
		t.Errorf("legacy approved=true: want OutcomeApproved, got %q", got)
	}
}

func TestLoad_MigratesLegacyApprovedFalse(t *testing.T) {
	dir, cleanup := overrideSessionsDir(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	raw := map[string]interface{}{
		"started_at": now.Format(time.RFC3339),
		"ended_at":   now.Add(time.Minute).Format(time.RFC3339),
		"repo":       "owner/repo",
		"pr":         "2",
		"approved":   false,
		// no "outcome" field
	}
	data, _ := json.Marshal(raw)
	writeSession(t, dir, "20060102-150405-2-owner-repo-PR2.json", data)

	sessions, err := stats.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if got := sessions[0].Outcome; got != stats.OutcomeClean {
		t.Errorf("legacy approved=false: want OutcomeClean, got %q", got)
	}
}

func TestLoad_MigratesNoLegacyField(t *testing.T) {
	dir, cleanup := overrideSessionsDir(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	raw := map[string]interface{}{
		"started_at": now.Format(time.RFC3339),
		"ended_at":   now.Add(time.Minute).Format(time.RFC3339),
		"repo":       "owner/repo",
		"pr":         "3",
		// no "outcome" and no "approved" field
	}
	data, _ := json.Marshal(raw)
	writeSession(t, dir, "20060102-150405-3-owner-repo-PR3.json", data)

	sessions, err := stats.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if got := sessions[0].Outcome; got != stats.OutcomeClean {
		t.Errorf("no outcome/approved: want OutcomeClean, got %q", got)
	}
}

// ── Summarize() Last30Days / OutcomeCounts tests ──────────────────────────────

func makeSess(outcome stats.Outcome, daysAgo int, comments, iters int) stats.Session {
	start := time.Now().AddDate(0, 0, -daysAgo)
	return stats.Session{
		StartedAt:     start,
		EndedAt:       start.Add(10 * time.Minute),
		Outcome:       outcome,
		TotalComments: comments,
		Iterations:    iters,
	}
}

func TestSummarize_Last30DaysOnlyIncludesRecent(t *testing.T) {
	sessions := []stats.Session{
		makeSess(stats.OutcomeApproved, 10, 5, 2),  // within 30 days
		makeSess(stats.OutcomeClean, 20, 3, 1),      // within 30 days
		makeSess(stats.OutcomeError, 31, 2, 1),      // older than 30 days — excluded
		makeSess(stats.OutcomeMerged, 60, 10, 4),    // much older — excluded
	}

	sum := stats.Summarize(sessions)

	// All-time should include all 4.
	if sum.TotalSessions != 4 {
		t.Errorf("all-time TotalSessions: want 4, got %d", sum.TotalSessions)
	}

	// Last30Days should include only the first 2.
	l30 := sum.Last30Days
	if l30.TotalSessions != 2 {
		t.Errorf("Last30Days TotalSessions: want 2, got %d", l30.TotalSessions)
	}
	if l30.TotalComments != 8 {
		t.Errorf("Last30Days TotalComments: want 8, got %d", l30.TotalComments)
	}
	if l30.TotalIterations != 3 {
		t.Errorf("Last30Days TotalIterations: want 3, got %d", l30.TotalIterations)
	}
}

func TestSummarize_OutcomeCounts(t *testing.T) {
	sessions := []stats.Session{
		makeSess(stats.OutcomeApproved, 1, 0, 0),
		makeSess(stats.OutcomeApproved, 2, 0, 0),
		makeSess(stats.OutcomeClean, 3, 0, 0),
		makeSess(stats.OutcomeError, 4, 0, 0),
	}

	sum := stats.Summarize(sessions)

	tests := []struct {
		outcome stats.Outcome
		want    int
	}{
		{stats.OutcomeApproved, 2},
		{stats.OutcomeClean, 1},
		{stats.OutcomeError, 1},
	}
	for _, tc := range tests {
		if got := sum.OutcomeCounts[tc.outcome]; got != tc.want {
			t.Errorf("OutcomeCounts[%q]: want %d, got %d", tc.outcome, tc.want, got)
		}
	}
	if sum.ApprovedSessions != 2 {
		t.Errorf("ApprovedSessions: want 2, got %d", sum.ApprovedSessions)
	}
}

func TestSummarize_CutoffBoundaryExact30Days(t *testing.T) {
	// A session exactly 30 days ago (just after the cutoff) should NOT be in Last30Days.
	// A session 29 days ago should be included.
	sessions := []stats.Session{
		makeSess(stats.OutcomeClean, 29, 1, 1), // included
		makeSess(stats.OutcomeClean, 31, 1, 1), // excluded
	}
	sum := stats.Summarize(sessions)
	if sum.Last30Days.TotalSessions != 1 {
		t.Errorf("boundary: want 1 session in Last30Days, got %d", sum.Last30Days.TotalSessions)
	}
}
