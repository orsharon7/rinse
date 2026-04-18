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
	"fmt"
	"os"
	"path/filepath"

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
  outcome TEXT CHECK(outcome IN ('merged','closed','open','failed','approved')),
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
CREATE INDEX IF NOT EXISTS idx_sessions_outcome     ON sessions(outcome);
CREATE INDEX IF NOT EXISTS idx_comment_events_session ON comment_events(session_id);
CREATE INDEX IF NOT EXISTS idx_patterns_session      ON patterns(session_id);
`

// DB wraps a *sql.DB with RINSE-specific write methods.
type DB struct {
	sql *sql.DB
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

	return &DB{sql: sqlDB}, nil
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
	if d == nil || d.sql == nil {
		return nil
	}
	return d.sql.Close()
}


