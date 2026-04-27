package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// realSessionsDir holds the original sessionsDir implementation so tests
// that exercise the env-var path can restore it without the withTempSessions
// override in place.
var realSessionsDir = sessionsDir

// withTempSessions redirects sessionsDir to a temp directory for the test.
func withTempSessions(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := sessionsDir
	sessionsDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { sessionsDir = orig })
	return dir
}

func makeSession(pr string, started time.Time, approved bool, comments int) Session {
	return Session{
		PR:            pr,
		Repo:          "owner/repo",
		RunnerName:    "test",
		StartedAt:     started,
		EndedAt:       started.Add(5 * time.Minute),
		Approved:      approved,
		TotalComments: comments,
		Iterations:    1,
	}
}

// TestSave_AtomicWrite verifies that Save writes atomically (no .tmp left
// behind) and that the persisted content round-trips correctly.
func TestSave_AtomicWrite(t *testing.T) {
	dir := withTempSessions(t)

	s := makeSession("42", time.Now().UTC().Truncate(time.Second), true, 3)
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// No temp file should linger.
	tmpFiles, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(tmpFiles) > 0 {
		t.Errorf("temp file(s) left behind: %v", tmpFiles)
	}

	// The session file must exist and decode correctly.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var jsonFiles []os.DirEntry
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			jsonFiles = append(jsonFiles, e)
		}
	}
	if len(jsonFiles) != 1 {
		t.Fatalf("expected 1 json file, got %d", len(jsonFiles))
	}

	data, err := os.ReadFile(filepath.Join(dir, jsonFiles[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var got Session
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.PR != s.PR || got.TotalComments != s.TotalComments {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, s)
	}
}

// TestLoadAll_SkipsCorruptFiles verifies that LoadAll silently skips files
// containing invalid JSON and still returns valid sessions.
func TestLoadAll_SkipsCorruptFiles(t *testing.T) {
	dir := withTempSessions(t)

	good := makeSession("10", time.Now().UTC(), true, 2)
	if err := good.Save(); err != nil {
		t.Fatalf("Save good: %v", err)
	}

	// Write a corrupt file directly into the sessions dir.
	if err := os.WriteFile(filepath.Join(dir, "corrupt.json"), []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("WriteFile corrupt: %v", err)
	}

	sessions, err := LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("expected 1 session (corrupt skipped), got %d", len(sessions))
	}
	if sessions[0].PR != good.PR {
		t.Errorf("got PR %q, want %q", sessions[0].PR, good.PR)
	}
}

// TestLoadAll_SortOrder verifies that LoadAll returns sessions ordered
// oldest StartedAt first.
func TestLoadAll_SortOrder(t *testing.T) {
	withTempSessions(t)

	now := time.Now().UTC().Truncate(time.Second)
	older := makeSession("1", now.Add(-2*time.Hour), false, 1)
	newer := makeSession("2", now, true, 5)

	// Save in reverse chronological order to confirm sorting is applied.
	for _, s := range []Session{newer, older} {
		if err := s.Save(); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	sessions, err := LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	if !sessions[0].StartedAt.Before(sessions[1].StartedAt) {
		t.Errorf("sessions not sorted oldest-first: [0]=%v [1]=%v",
			sessions[0].StartedAt, sessions[1].StartedAt)
	}
}

// TestLoadAll_MissingDir verifies that LoadAll returns nil (not an error)
// when the sessions directory does not yet exist.
func TestLoadAll_MissingDir(t *testing.T) {
	tmp := t.TempDir()
	missing := filepath.Join(tmp, "nonexistent")
	orig := sessionsDir
	sessionsDir = func() (string, error) { return missing, nil }
	t.Cleanup(func() { sessionsDir = orig })

	sessions, err := LoadAll()
	if err != nil {
		t.Fatalf("LoadAll on missing dir: %v", err)
	}
	if sessions != nil {
		t.Errorf("expected nil sessions for missing dir, got %v", sessions)
	}
}

// TestSessionsDir_EnvVar verifies that RINSE_SESSIONS_DIR is honoured by the
// real sessionsDir resolver (not the test override).
func TestSessionsDir_EnvVar(t *testing.T) {
	customDir := filepath.Join(t.TempDir(), "custom-sessions")
	t.Setenv("RINSE_SESSIONS_DIR", customDir)

	// Restore the real resolver so the env var is actually exercised.
	orig := sessionsDir
	sessionsDir = realSessionsDir
	t.Cleanup(func() { sessionsDir = orig })

	got, err := sessionsDir()
	if err != nil {
		t.Fatalf("sessionsDir: %v", err)
	}
	if got != customDir {
		t.Errorf("sessionsDir() = %q, want %q", got, customDir)
	}
}

// TestSave_EnvVarDirCreated verifies that Save creates the directory pointed
// to by RINSE_SESSIONS_DIR when it does not yet exist.
func TestSave_EnvVarDirCreated(t *testing.T) {
	customDir := filepath.Join(t.TempDir(), "env-sessions")
	t.Setenv("RINSE_SESSIONS_DIR", customDir)

	// Restore real resolver so the env var takes effect.
	orig := sessionsDir
	sessionsDir = realSessionsDir
	t.Cleanup(func() { sessionsDir = orig })

	s := makeSession("99", time.Now().UTC().Truncate(time.Second), true, 1)
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, err := os.ReadDir(customDir)
	if err != nil {
		t.Fatalf("ReadDir %q: %v", customDir, err)
	}
	var jsonFiles int
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			jsonFiles++
		}
	}
	if jsonFiles != 1 {
		t.Errorf("expected 1 json file in %q, got %d", customDir, jsonFiles)
	}
}
