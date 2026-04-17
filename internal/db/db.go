// Package db provides the RINSE telemetry database — a local SQLite store at
// ~/.rinse/rinse.db that records every review session, per-iteration comment
// counts, and extracted code patterns.
//
// Architecture: SQLite now, Supabase later.  The schema is designed to accept
// an additive migration (team_id column + RLS) when the cloud sync tier is
// built in Phase 2.  Nothing in this package changes for Phase 2 callers.
package db

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	// Pure-Go SQLite driver — no CGO required.
	_ "modernc.org/sqlite"
)

// DB wraps a SQLite connection and exposes the RINSE write/read surface.
type DB struct {
	sql *sql.DB
}

// NewUUID generates a random UUID v4 string.
// Exported so callers constructing row structs can use it without pulling in
// an external uuid package.
func NewUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant bits
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Open opens (or creates) ~/.rinse/rinse.db and runs the schema migration.
// The caller must call Close() when done.
func Open() (*DB, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("db: cannot determine home dir: %w", err)
	}
	dir := filepath.Join(home, ".rinse")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("db: cannot create %s: %w", dir, err)
	}

	path := filepath.Join(dir, "rinse.db")
	sqldb, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("db: open sqlite at %s: %w", path, err)
	}

	// Single writer, many readers — the default WAL mode prevents lock contention.
	if _, err := sqldb.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("db: set WAL mode: %w", err)
	}

	d := &DB{sql: sqldb}
	if err := d.migrate(); err != nil {
		_ = sqldb.Close()
		return nil, err
	}
	return d, nil
}

// Close releases the underlying database connection.
func (d *DB) Close() error {
	return d.sql.Close()
}

// migrate creates tables that do not yet exist.  All statements use
// CREATE TABLE IF NOT EXISTS so the migration is idempotent and safe to
// run on every startup.
func (d *DB) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS sessions (
  id                           TEXT PRIMARY KEY,
  repo                         TEXT NOT NULL,
  pr_number                    INTEGER NOT NULL,
  pr_title                     TEXT,
  branch                       TEXT,
  started_at                   DATETIME NOT NULL,
  completed_at                 DATETIME,
  duration_seconds             INTEGER,
  estimated_time_saved_seconds INTEGER,
  iterations                   INTEGER DEFAULT 0,
  total_comments_fixed         INTEGER DEFAULT 0,
  outcome                      TEXT CHECK(outcome IN ('approved','merged','closed','open','failed','max_iterations','error','aborted')),
  model                        TEXT,
  created_at                   DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS comment_events (
  id           TEXT PRIMARY KEY,
  session_id   TEXT REFERENCES sessions(id),
  iteration    INTEGER,
  comment_count INTEGER,
  recorded_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS patterns (
  id         TEXT PRIMARY KEY,
  session_id TEXT REFERENCES sessions(id),
  pattern    TEXT NOT NULL,
  count      INTEGER DEFAULT 1
);
`
	if _, err := d.sql.Exec(schema); err != nil {
		return fmt.Errorf("db: migrate schema: %w", err)
	}
	return nil
}
