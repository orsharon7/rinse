package db

import (
	"database/sql"
	"fmt"
	"time"
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
	Outcome                     string // "open" while running
	Model                       string
}

// InsertSession writes a new session row with outcome="open".
// Call UpdateSession when the run finishes.
func (d *DB) InsertSession(s SessionRow) error {
	const q = `
INSERT INTO sessions
  (id, repo, pr_number, pr_title, branch, runner, started_at, iterations,
   total_comments_fixed, outcome, model)
VALUES
  (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := d.db.Exec(q,
		s.ID, s.Repo, s.PRNumber, nullString(s.PRTitle), nullString(s.Branch),
		nullString(s.Runner),
		s.StartedAt.UTC().Format(time.RFC3339),
		s.Iterations, s.TotalCommentsFixed,
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
  outcome                      = ?
WHERE id = ?`

	var completedAt sql.NullString
	if s.CompletedAt != nil {
		completedAt = sql.NullString{String: s.CompletedAt.UTC().Format(time.RFC3339), Valid: true}
	}

	_, err := d.db.Exec(q,
		completedAt,
		nullInt(s.DurationSeconds),
		nullInt(s.EstimatedTimeSavedSeconds),
		s.Iterations,
		s.TotalCommentsFixed,
		coalesceOutcome(s.Outcome, "open"),
		s.ID,
	)
	if err != nil {
		return fmt.Errorf("db: update session %s: %w", s.ID, err)
	}
	return nil
}

// LoadSessionRows returns all session rows ordered by started_at ascending.
// It is used by stats.Load() to build the in-memory Session slice.
func (d *DB) LoadSessionRows() ([]SessionRow, error) {
	const q = `
SELECT
  id, repo, pr_number, COALESCE(pr_title,''), COALESCE(branch,''), COALESCE(runner,''),
  started_at,
  completed_at, duration_seconds, estimated_time_saved_seconds,
  iterations, total_comments_fixed,
  COALESCE(outcome,'open'), COALESCE(model,'')
FROM sessions
ORDER BY started_at ASC`

	rows, err := d.db.Query(q)
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
			&s.ID, &s.Repo, &s.PRNumber, &s.PRTitle, &s.Branch, &s.Runner,
			&startedAtStr,
			&completedAtStr, &durationSec, &estSaved,
			&s.Iterations, &s.TotalCommentsFixed,
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
	return out, rows.Err()
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
