// Package db manages the RINSE local telemetry database (~/.rinse/rinse.db).
//
// Architecture: SQLite now, Supabase later. The schema is designed to be
// migrated additively — Phase 2 adds a team_id column and cloud sync.
//
// All writes are fire-and-forget from the caller's perspective: errors are
// returned but the runner continues regardless. A failed DB write must never
// abort a PR review cycle.
//
// Migration versioning: Open() applies the baseline schema then runs any
// pending numbered migrations from the migrations table. Each migration is
// recorded in the schema_migrations table so it runs at most once.
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
  outcome TEXT CHECK(outcome IN ('merged','closed','open','failed','approved','error','aborted','max_iterations')),
  rules_extracted               INTEGER DEFAULT 0,
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

CREATE TABLE IF NOT EXISTS schema_migrations (
  version     INTEGER PRIMARY KEY,
  name        TEXT NOT NULL,
  applied_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);
`

// migrations lists additive schema changes to apply after the baseline schema.
// Each entry is (version, name, SQL). Migrations run in version order and are
// recorded in schema_migrations so they execute at most once per install.
// All statements must be idempotent or guarded (e.g. ALTER TABLE ADD COLUMN
// is not idempotent in SQLite, so we check before applying).
var migrations = []migration{
	{
		Version: 4,
		Name:    "add_runner_column",
		Up: func(tx *sql.Tx) error {
			// SQLite does not support IF NOT EXISTS on ALTER TABLE ADD COLUMN.
			// Check if the column already exists first.
			rows, err := tx.Query(`PRAGMA table_info(sessions)`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var cid int
				var name, typ string
				var notnull, pk int
				var dflt sql.NullString
				if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
					return err
				}
				if name == "runner" {
					return nil // already exists
				}
			}
			_, err = tx.Exec(`ALTER TABLE sessions ADD COLUMN runner TEXT`)
			return err
		},
	},
	{
		Version: 5,
		Name:    "idx_sessions_repo_pr",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_repo_pr ON sessions(repo, pr_number)`)
			return err
		},
	},
	{
		// Version 6: expand_outcome_check — no-op migration.
		// The CHECK constraint in the schema constant was expanded to include
		// 'error','aborted','max_iterations' for fresh installs. Existing installs
		// have no CHECK on the live DB (CREATE TABLE IF NOT EXISTS cannot retrofit
		// constraints). This entry records the intent in schema_migrations.
		Version: 6,
		Name:    "expand_outcome_check",
		Up:      func(tx *sql.Tx) error { return nil },
	},
	{
		// Version 7: add rules_extracted column to sessions.
		// Populated by the reflect agent; previously only written to JSON files.
		Version: 7,
		Name:    "add_rules_extracted_column",
		Up: func(tx *sql.Tx) error {
			rows, err := tx.Query(`PRAGMA table_info(sessions)`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var cid int
				var name, typ string
				var notnull, pk int
				var dflt sql.NullString
				if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
					return err
				}
				if name == "rules_extracted" {
					return nil // already exists
				}
			}
			_, err = tx.Exec(`ALTER TABLE sessions ADD COLUMN rules_extracted INTEGER DEFAULT 0`)
			return err
		},
	},
}

type migration struct {
	Version int
	Name    string
	Up      func(tx *sql.Tx) error
}

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

	if err := applyMigrations(sqlDB); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("db: apply migrations: %w", err)
	}

	return &DB{sql: sqlDB}, nil
}

// applyMigrations runs any pending migrations in version order.
func applyMigrations(db *sql.DB) error {
	for _, m := range migrations {
		var count int
		err := db.QueryRow(
			`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.Version,
		).Scan(&count)
		if err != nil {
			return fmt.Errorf("migration %d: check applied: %w", m.Version, err)
		}
		if count > 0 {
			continue // already applied
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("migration %d: begin tx: %w", m.Version, err)
		}
		if err := m.Up(tx); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d (%s): %w", m.Version, m.Name, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`,
			m.Version, m.Name,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: record: %w", m.Version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %d: commit: %w", m.Version, err)
		}
	}
	return nil
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

