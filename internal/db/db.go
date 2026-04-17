// Package db manages the RINSE local telemetry database (~/.rinse/rinse.db).
//
// Architecture: SQLite now, Supabase later. The schema is designed to be
// migrated additively — Phase 2 adds a team_id column and cloud sync.
//
// All writes are fire-and-forget from the caller's perspective: errors are
// returned but the runner continues regardless. A failed DB write must never
// abort a PR review cycle.
package db

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // register the sqlite driver
)

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
  id                            TEXT PRIMARY KEY,
  repo                          TEXT NOT NULL,
  pr_number                     INTEGER NOT NULL,
  pr_title                      TEXT,
  branch                        TEXT,
  runner                        TEXT,
  model                         TEXT,
  started_at                    DATETIME NOT NULL,
  completed_at                  DATETIME,
  duration_seconds              INTEGER,
  estimated_time_saved_seconds  INTEGER,
  iterations                    INTEGER DEFAULT 0,
  total_comments_fixed          INTEGER DEFAULT 0,
  outcome TEXT CHECK(outcome IN ('merged','closed','open','failed')),
  created_at                    DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS comment_events (
  id            TEXT PRIMARY KEY,
  session_id    TEXT REFERENCES sessions(id) ON DELETE CASCADE,
  iteration     INTEGER NOT NULL,
  comment_count INTEGER NOT NULL,
  recorded_at   DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS patterns (
  id         TEXT PRIMARY KEY,
  session_id TEXT REFERENCES sessions(id) ON DELETE CASCADE,
  pattern    TEXT NOT NULL,
  count      INTEGER DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_sessions_repo        ON sessions(repo);
CREATE INDEX IF NOT EXISTS idx_sessions_started_at  ON sessions(started_at);
CREATE INDEX IF NOT EXISTS idx_comment_events_session ON comment_events(session_id);
CREATE INDEX IF NOT EXISTS idx_patterns_session      ON patterns(session_id);
`

// DB wraps a *sql.DB with RINSE-specific write methods.
type DB struct {
	db *sql.DB
}

// Path returns the default path for the RINSE telemetry database.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("db: cannot determine home dir: %w", err)
	}
	return filepath.Join(home, ".rinse", "rinse.db"), nil
}

// Open opens (or creates) the RINSE telemetry database at the given path.
// The parent directory is created if it does not exist.
// Callers should defer db.Close().
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("db: mkdir %s: %w", filepath.Dir(path), err)
	}

	sqlDB, err := sql.Open("sqlite", path+"?_foreign_keys=on&_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("db: open %s: %w", path, err)
	}

	// Single writer; safe for concurrent readers via WAL.
	sqlDB.SetMaxOpenConns(1)

	if _, err := sqlDB.Exec(schema); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("db: apply schema: %w", err)
	}

	return &DB{db: sqlDB}, nil
}

// OpenDefault opens the database at the default path (~/.rinse/rinse.db).
func OpenDefault() (*DB, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return Open(path)
}

// Close closes the underlying database connection.
func (d *DB) Close() error {
	if d == nil || d.db == nil {
		return nil
	}
	return d.db.Close()
}

// ── Session write path ────────────────────────────────────────────────────────

// SessionRow represents a row in the sessions table.
type SessionRow struct {
	ID       string
	Repo     string
	PRNumber int
	PRTitle  string
	Branch   string
	Runner   string
	Model    string

	StartedAt   time.Time
	CompletedAt *time.Time

	DurationSeconds             *int
	EstimatedTimeSavedSeconds   *int
	Iterations                  int
	TotalCommentsFixed          int
	Outcome                     string // "open" | "merged" | "closed" | "failed"
}

// InsertSession inserts a new session row. Call this at the start of a run
// with Outcome="open".
func (d *DB) InsertSession(s SessionRow) error {
	if d == nil {
		return nil
	}
	_, err := d.db.Exec(`
		INSERT INTO sessions
		  (id, repo, pr_number, pr_title, branch, runner, model,
		   started_at, outcome, iterations, total_comments_fixed)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Repo, s.PRNumber, s.PRTitle, s.Branch, s.Runner, s.Model,
		s.StartedAt.UTC().Format(time.RFC3339),
		"open",
		s.Iterations,
		s.TotalCommentsFixed,
	)
	if err != nil {
		return fmt.Errorf("db: insert session %s: %w", s.ID, err)
	}
	return nil
}

// FinalizeSession updates the session row with completion data.
func (d *DB) FinalizeSession(id string, completedAt time.Time, durationSec, commentCount, iterations int, outcome string) error {
	if d == nil {
		return nil
	}
	estimated := commentCount * 240 // 4 min per comment
	_, err := d.db.Exec(`
		UPDATE sessions
		SET completed_at                 = ?,
		    duration_seconds             = ?,
		    estimated_time_saved_seconds = ?,
		    total_comments_fixed         = ?,
		    iterations                   = ?,
		    outcome                      = ?
		WHERE id = ?`,
		completedAt.UTC().Format(time.RFC3339),
		durationSec,
		estimated,
		commentCount,
		iterations,
		outcome,
		id,
	)
	if err != nil {
		return fmt.Errorf("db: finalize session %s: %w", id, err)
	}
	return nil
}

// InsertCommentEvent records a Copilot comment batch for a given iteration.
func (d *DB) InsertCommentEvent(eventID, sessionID string, iteration, commentCount int) error {
	if d == nil {
		return nil
	}
	_, err := d.db.Exec(`
		INSERT INTO comment_events (id, session_id, iteration, comment_count)
		VALUES (?, ?, ?, ?)`,
		eventID, sessionID, iteration, commentCount,
	)
	if err != nil {
		return fmt.Errorf("db: insert comment event: %w", err)
	}
	return nil
}

// InsertPattern records a detected code pattern for a session.
func (d *DB) InsertPattern(patternID, sessionID, pattern string, count int) error {
	if d == nil {
		return nil
	}
	_, err := d.db.Exec(`
		INSERT INTO patterns (id, session_id, pattern, count)
		VALUES (?, ?, ?, ?)`,
		patternID, sessionID, pattern, count,
	)
	if err != nil {
		return fmt.Errorf("db: insert pattern: %w", err)
	}
	return nil
}

// ── Query path ────────────────────────────────────────────────────────────────

// SessionSummaryRow is a denormalised row used by the stats command.
type SessionSummaryRow struct {
	ID                          string
	Repo                        string
	PRNumber                    int
	PRTitle                     string
	Runner                      string
	Model                       string
	StartedAt                   time.Time
	CompletedAt                 *time.Time
	DurationSeconds             int
	EstimatedTimeSavedSeconds   int
	Iterations                  int
	TotalCommentsFixed          int
	Outcome                     string
	Patterns                    []string
}

// LoadSessions returns all session rows from the DB ordered oldest-first.
// Patterns are loaded per session in a second query.
func (d *DB) LoadSessions() ([]SessionSummaryRow, error) {
	if d == nil {
		return nil, errors.New("db: not open")
	}

	rows, err := d.db.Query(`
		SELECT id, repo, pr_number, COALESCE(pr_title,''), COALESCE(runner,''), COALESCE(model,''),
		       started_at, completed_at,
		       COALESCE(duration_seconds,0), COALESCE(estimated_time_saved_seconds,0),
		       COALESCE(iterations,0), COALESCE(total_comments_fixed,0),
		       COALESCE(outcome,'open')
		FROM sessions
		ORDER BY started_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("db: query sessions: %w", err)
	}
	defer rows.Close()

	var sessions []SessionSummaryRow
	for rows.Next() {
		var s SessionSummaryRow
		var startedStr string
		var completedStr *string
		if err := rows.Scan(
			&s.ID, &s.Repo, &s.PRNumber, &s.PRTitle, &s.Runner, &s.Model,
			&startedStr, &completedStr,
			&s.DurationSeconds, &s.EstimatedTimeSavedSeconds,
			&s.Iterations, &s.TotalCommentsFixed,
			&s.Outcome,
		); err != nil {
			return nil, fmt.Errorf("db: scan session: %w", err)
		}
		if t, err := time.Parse(time.RFC3339, startedStr); err == nil {
			s.StartedAt = t
		}
		if completedStr != nil {
			if t, err := time.Parse(time.RFC3339, *completedStr); err == nil {
				s.CompletedAt = &t
			}
		}
		sessions = append(sessions, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate sessions: %w", err)
	}

	// Load patterns per session.
	for i := range sessions {
		pats, err := d.loadPatterns(sessions[i].ID)
		if err != nil {
			// Non-fatal: missing patterns don't break stats.
			continue
		}
		sessions[i].Patterns = pats
	}

	return sessions, nil
}

func (d *DB) loadPatterns(sessionID string) ([]string, error) {
	rows, err := d.db.Query(
		`SELECT pattern FROM patterns WHERE session_id = ? ORDER BY count DESC`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pats []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		pats = append(pats, p)
	}
	return pats, rows.Err()
}
