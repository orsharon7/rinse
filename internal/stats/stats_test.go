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

// overrideSessionsDir redirects the sessions directory used by Load by
// pointing HOME at a temporary directory and creating the expected
// <HOME>/.rinse/sessions path.
// It returns the sessions directory path.
func overrideSessionsDir(t *testing.T) string {
	t.Helper()
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	sessDir := filepath.Join(tempHome, ".rinse", "sessions")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return sessDir
}

// ── Load() migration tests ────────────────────────────────────────────────────

func TestLoad_MigratesLegacyApprovedTrue(t *testing.T) {
	dir := overrideSessionsDir(t)

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
	dir := overrideSessionsDir(t)

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
	dir := overrideSessionsDir(t)

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

// ── IsOptedIn / SetOptIn / Save opt-out tests ─────────────────────────────────

// overrideHome redirects both home and config directory lookups to a temp
// directory so that configDir() and SessionsDir() resolve to isolated paths on
// all supported platforms, including Windows.
func overrideHome(t *testing.T) string {
	t.Helper()
	tmpHome := t.TempDir()

	// Unix/XDG-style home/config resolution.
	t.Setenv("HOME", tmpHome)
	// Clear XDG_CONFIG_HOME so we don't accidentally write to the real config dir.
	t.Setenv("XDG_CONFIG_HOME", "")

	// Windows-style home/config resolution used by os.UserHomeDir/UserConfigDir.
	appData := filepath.Join(tmpHome, "AppData", "Roaming")
	localAppData := filepath.Join(tmpHome, "AppData", "Local")
	if err := os.MkdirAll(appData, 0o755); err != nil {
		t.Fatalf("mkdir appdata: %v", err)
	}
	if err := os.MkdirAll(localAppData, 0o755); err != nil {
		t.Fatalf("mkdir local appdata: %v", err)
	}
	t.Setenv("APPDATA", appData)
	t.Setenv("LOCALAPPDATA", localAppData)
	t.Setenv("USERPROFILE", tmpHome)

	vol := filepath.VolumeName(tmpHome)
	t.Setenv("HOMEDRIVE", vol)
	if vol != "" {
		t.Setenv("HOMEPATH", tmpHome[len(vol):])
	} else {
		t.Setenv("HOMEPATH", tmpHome)
	}

	return tmpHome
}

func TestIsOptedIn_UnsetPreference(t *testing.T) {
	overrideHome(t)
	// No stats.json exists — expect (false, nil).
	got, err := stats.IsOptedIn()
	if err != nil {
		t.Fatalf("IsOptedIn() unexpected error: %v", err)
	}
	if got {
		t.Errorf("IsOptedIn() with no preference set: want false, got true")
	}
}

func TestSetOptIn_RoundTrip(t *testing.T) {
	overrideHome(t)
	for _, optIn := range []bool{true, false} {
		if err := stats.SetOptIn(optIn); err != nil {
			t.Fatalf("SetOptIn(%v) error: %v", optIn, err)
		}
		got, err := stats.IsOptedIn()
		if err != nil {
			t.Fatalf("IsOptedIn() after SetOptIn(%v) error: %v", optIn, err)
		}
		if got != optIn {
			t.Errorf("IsOptedIn() after SetOptIn(%v): want %v, got %v", optIn, optIn, got)
		}
	}
}

func TestSave_SkipsWhenOptedOut(t *testing.T) {
	overrideHome(t)
	// Opt out explicitly.
	if err := stats.SetOptIn(false); err != nil {
		t.Fatalf("SetOptIn(false): %v", err)
	}

	sess := stats.Session{
		StartedAt:     time.Now(),
		EndedAt:       time.Now().Add(time.Minute),
		Repo:          "owner/repo",
		PR:            "42",
		TotalComments: 3,
		Iterations:    1,
		Outcome:       stats.OutcomeApproved,
	}
	if err := stats.Save(sess); err != nil {
		t.Fatalf("Save() with opted-out user returned error: %v", err)
	}

	// No session files should have been written.
	sessions, err := stats.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("Save() with opted-out user: expected 0 sessions on disk, got %d", len(sessions))
	}
}

func TestSummarize_CutoffBoundaryExact30Days(t *testing.T) {
	// A session at the 30-day cutoff should NOT be in Last30Days because
	// Summarize uses StartedAt.After(cutoff). A session 29 days ago should
	// be included.
	base := time.Now().UTC()

	atCutoff := makeSess(stats.OutcomeClean, 0, 1, 1)
	atCutoff.StartedAt = base.AddDate(0, 0, -30)

	withinWindow := makeSess(stats.OutcomeClean, 0, 1, 1)
	withinWindow.StartedAt = base.AddDate(0, 0, -29)

	sessions := []stats.Session{
		withinWindow, // included
		atCutoff,     // excluded
	}
	sum := stats.Summarize(sessions)
	if sum.Last30Days.TotalSessions != 1 {
		t.Errorf("boundary: want 1 session in Last30Days, got %d", sum.Last30Days.TotalSessions)
	}
}

// TestSave_HappyPath verifies that Save writes a session file when the user
// has opted in, and that the written file can be re-loaded with Load() with
// SchemaVersion and Outcome preserved.
func TestSave_HappyPath(t *testing.T) {
	overrideHome(t)

	// Opt in so Save proceeds to write a file.
	if err := stats.SetOptIn(true); err != nil {
		t.Fatalf("SetOptIn(true): %v", err)
	}

	sess := stats.Session{
		StartedAt:     time.Now().UTC().Truncate(time.Second),
		EndedAt:       time.Now().UTC().Truncate(time.Second).Add(2 * time.Minute),
		Repo:          "owner/repo",
		PR:            "99",
		TotalComments: 5,
		Iterations:    2,
		Outcome:       stats.OutcomeApproved,
	}

	if err := stats.Save(sess); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := stats.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 session on disk, got %d", len(loaded))
	}
	got := loaded[0]
	if got.SchemaVersion != stats.SchemaVersion {
		t.Errorf("SchemaVersion: want %d, got %d", stats.SchemaVersion, got.SchemaVersion)
	}
	if got.Outcome != stats.OutcomeApproved {
		t.Errorf("Outcome: want %q, got %q", stats.OutcomeApproved, got.Outcome)
	}
	if got.TotalComments != sess.TotalComments {
		t.Errorf("TotalComments: want %d, got %d", sess.TotalComments, got.TotalComments)
	}
}
