-- Migration 002: indexes for query performance
-- Added after initial schema to support stats/filtering queries.

CREATE INDEX IF NOT EXISTS idx_sessions_repo          ON sessions(repo);
CREATE INDEX IF NOT EXISTS idx_sessions_started_at    ON sessions(started_at);
CREATE INDEX IF NOT EXISTS idx_sessions_outcome       ON sessions(outcome);
CREATE INDEX IF NOT EXISTS idx_comment_events_session ON comment_events(session_id);
CREATE INDEX IF NOT EXISTS idx_patterns_session       ON patterns(session_id);
