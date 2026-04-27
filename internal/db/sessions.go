package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SessionRow is the data RINSE writes per review session.
type SessionRow struct {
	// ID is a UUID v4 that must match the stats.Session.SessionID.
	ID      string
	Repo    string
	PRNumber int
	PRTitle string
	Branch  string
	Runner  string

	// StartedAt is set when the session opens.
	StartedAt time.Time

	// CompletedAt, DurationSeconds, EstimatedTimeSavedSeconds, Iterations,
	// TotalCommentsFixed, and Outcome are stamped on completion.
	CompletedAt                 *time.Time
	DurationSeconds             *int
	EstimatedTimeSavedSeconds   *int
	Iterations                  int
	TotalCommentsFixed          int
	RulesExtracted              int    // number of coding rules committed by --reflect
	Outcome                     string // "open" while running
	Model                       string
	Patterns                    []string
}

// InsertSession writes a new session row with outcome="open".
// Call UpdateSession when the run finishes.
func (d *DB) InsertSession(s SessionRow) error {
	const q = `
INSERT INTO sessions
  (id, repo, pr_number, pr_title, branch, runner, started_at, iterations,
   total_comments_fixed, rules_extracted, outcome, model)
VALUES
  (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := d.sql.Exec(q,
		s.ID, s.Repo, s.PRNumber, nullString(s.PRTitle), nullString(s.Branch),
		nullString(s.Runner),
		s.StartedAt.UTC().Format(time.RFC3339),
		s.Iterations, s.TotalCommentsFixed, s.RulesExtracted,
		coalesceOutcome(s.Outcome, "open"),
		nullString(s.Model),
	)
	if err != nil {
		return fmt.Errorf("db: insert session %s: %w", s.ID, err)
	}
	return nil
}

// UpdateSession stamps the terminal fields on the row identified by s.ID.
func (d *DB) UpdateSession(s SessionRow) error {
	const q = `
UPDATE sessions SET
  completed_at                 = ?,
  duration_seconds             = ?,
  estimated_time_saved_seconds = ?,
  iterations                   = ?,
  total_comments_fixed         = ?,
  rules_extracted              = ?,
  outcome                      = ?
WHERE id = ?`

	var completedAt sql.NullString
	if s.CompletedAt != nil {
		completedAt = sql.NullString{String: s.CompletedAt.UTC().Format(time.RFC3339), Valid: true}
	}

	_, err := d.sql.Exec(q,
		completedAt,
		nullInt(s.DurationSeconds),
		nullInt(s.EstimatedTimeSavedSeconds),
		s.Iterations,
		s.TotalCommentsFixed,
		s.RulesExtracted,
		coalesceOutcome(s.Outcome, "open"),
		s.ID,
	)
	if err != nil {
		return fmt.Errorf("db: update session %s: %w", s.ID, err)
	}
	return nil
}

// FindSessionID returns the most recent session ID for a given repo and PR number.
// Returns ("", nil) if no matching session is found.
func (d *DB) FindSessionID(repo string, prNumber int) (string, error) {
	if d == nil {
		return "", nil
	}
	var id string
	err := d.sql.QueryRow(
		`SELECT id FROM sessions WHERE repo = ? AND pr_number = ? ORDER BY started_at DESC LIMIT 1`,
		repo, prNumber,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("db: find session id repo=%s pr=%d: %w", repo, prNumber, err)
	}
	return id, nil
}

// InsertPatterns persists zero or more pattern strings for a session.
// Each pattern is stored as a row in the patterns table.
// Duplicate patterns within the same session are silently skipped (INSERT OR IGNORE).
// Non-fatal: if d is nil or patterns is empty the call is a no-op.
func (d *DB) InsertPatterns(sessionID string, patterns []string) error {
	return d.SavePatterns(sessionID, patterns)
}

// SavePatterns inserts zero or more pattern strings for a session in a single
// transaction. Each pattern gets a fresh UUID. Empty strings are skipped.
// If d is nil or patterns is empty the call is a no-op.
func (d *DB) SavePatterns(sessionID string, patterns []string) error {
	if d == nil || len(patterns) == 0 {
		return nil
	}

	tx, err := d.sql.Begin()
	if err != nil {
		return fmt.Errorf("db: save patterns begin tx: %w", err)
	}

	const q = `INSERT OR IGNORE INTO patterns (id, session_id, pattern, count) VALUES (?, ?, ?, 1)`
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if _, err := tx.Exec(q, uuid.New().String(), sessionID, p); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("db: save pattern session=%s: %w", sessionID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: save patterns commit: %w", err)
	}
	return nil
}

// FinalizeSession updates the session row with completion data.
// This is a convenience wrapper over UpdateSession for the runner's fire-and-forget path.
func (d *DB) FinalizeSession(id string, completedAt time.Time, durationSec, commentCount, iterations int, outcome string) error {
	if d == nil {
		return nil
	}
	estimated := commentCount * 240 // 4 min per comment
	dur := durationSec
	est := estimated
	return d.UpdateSession(SessionRow{
		ID:                        id,
		CompletedAt:               &completedAt,
		DurationSeconds:           &dur,
		EstimatedTimeSavedSeconds: &est,
		TotalCommentsFixed:        commentCount,
		Iterations:                iterations,
		Outcome:                   outcome,
		// RulesExtracted is not available at finalize time in the runner path;
		// it is set via UpdateSession directly by callers that have the value
		// (e.g. TUI monitor after reflect agent runs).
	})
}

// LoadSessions returns all session rows ordered by started_at ascending.
// It is used by stats.Load() to build the in-memory Session slice.
func (d *DB) LoadSessions() ([]SessionRow, error) {
	const q = `
SELECT
  id, repo, pr_number, COALESCE(pr_title,''), COALESCE(branch,''),
  COALESCE(runner,''),
  started_at,
  completed_at, duration_seconds, estimated_time_saved_seconds,
  iterations, total_comments_fixed,
  COALESCE(rules_extracted,0),
  COALESCE(outcome,'open'), COALESCE(model,'')
FROM sessions
ORDER BY started_at ASC`

	rows, err := d.sql.Query(q)
	if err != nil {
		return nil, fmt.Errorf("db: load sessions: %w", err)
	}
	defer rows.Close()

	var out []SessionRow
	for rows.Next() {
		var s SessionRow
		var startedAtStr string
		var completedAtStr sql.NullString
		var durationSec, estSaved sql.NullInt64

		if err := rows.Scan(
			&s.ID, &s.Repo, &s.PRNumber, &s.PRTitle, &s.Branch,
			&s.Runner,
			&startedAtStr,
			&completedAtStr, &durationSec, &estSaved,
			&s.Iterations, &s.TotalCommentsFixed,
			&s.RulesExtracted,
			&s.Outcome, &s.Model,
		); err != nil {
			return nil, fmt.Errorf("db: scan session row: %w", err)
		}

		t, err := time.Parse(time.RFC3339, startedAtStr)
		if err != nil {
			return nil, fmt.Errorf("db: parse started_at %q for session %s: %w", startedAtStr, s.ID, err)
		}
		s.StartedAt = t

		if completedAtStr.Valid {
			t, err := time.Parse(time.RFC3339, completedAtStr.String)
			if err != nil {
				return nil, fmt.Errorf("db: parse completed_at %q for session %s: %w", completedAtStr.String, s.ID, err)
			}
			s.CompletedAt = &t
		}
		if durationSec.Valid {
			v := int(durationSec.Int64)
			s.DurationSeconds = &v
		}
		if estSaved.Valid {
			v := int(estSaved.Int64)
			s.EstimatedTimeSavedSeconds = &v
		}

		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate sessions: %w", err)
	}

	// Load patterns per session (best-effort).
	for i := range out {
		pats, err := loadPatternStrings(d, out[i].ID)
		if err != nil {
			continue
		}
		out[i].Patterns = pats
	}

	return out, nil
}

func loadPatternStrings(d *DB, sessionID string) ([]string, error) {
	rows, err := d.sql.Query(
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

// ── helpers ──────────────────────────────────────────────────────────────────

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nullInt(p *int) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*p), Valid: true}
}

func coalesceOutcome(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
