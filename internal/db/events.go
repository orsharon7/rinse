package db

import (
	"fmt"
	"time"
)

// CommentEventRow records a single Copilot review pass for a session.
type CommentEventRow struct {
	// ID is a UUID v4.
	ID          string
	SessionID   string
	Iteration   int
	CommentCount int
}

// InsertCommentEvent persists one per-iteration comment-count record.
func (d *DB) InsertCommentEvent(e CommentEventRow) error {
	const q = `
INSERT INTO comment_events (id, session_id, iteration, comment_count, recorded_at)
VALUES (?, ?, ?, ?, ?)`

	_, err := d.db.Exec(q,
		e.ID, e.SessionID, e.Iteration, e.CommentCount,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("db: insert comment_event session=%s iter=%d: %w",
			e.SessionID, e.Iteration, err)
	}
	return nil
}

// PatternRow records an extracted code pattern for a session.
type PatternRow struct {
	ID        string
	SessionID string
	Pattern   string
	Count     int
}

// InsertPattern persists a pattern extracted during the reflection phase.
func (d *DB) InsertPattern(p PatternRow) error {
	const q = `
INSERT INTO patterns (id, session_id, pattern, count)
VALUES (?, ?, ?, ?)`

	count := p.Count
	if count <= 0 {
		count = 1
	}
	_, err := d.db.Exec(q, p.ID, p.SessionID, p.Pattern, count)
	if err != nil {
		return fmt.Errorf("db: insert pattern session=%s pattern=%q: %w",
			p.SessionID, p.Pattern, err)
	}
	return nil
}

// LoadPatternsBySession returns all patterns recorded for a given session.
func (d *DB) LoadPatternsBySession(sessionID string) ([]PatternRow, error) {
	const q = `SELECT id, session_id, pattern, count FROM patterns WHERE session_id = ?`

	rows, err := d.db.Query(q, sessionID)
	if err != nil {
		return nil, fmt.Errorf("db: load patterns session=%s: %w", sessionID, err)
	}
	defer rows.Close()

	var out []PatternRow
	for rows.Next() {
		var p PatternRow
		if err := rows.Scan(&p.ID, &p.SessionID, &p.Pattern, &p.Count); err != nil {
			return nil, fmt.Errorf("db: scan pattern row: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
