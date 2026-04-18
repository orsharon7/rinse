-- Migration 001: initial schema
-- Phase 1: local SQLite telemetry for RINSE

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
