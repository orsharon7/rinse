-- Migration 005: composite index for FindSessionID lookup
-- Supports WHERE repo = ? AND pr_number = ? ORDER BY started_at DESC
-- The existing idx_sessions_repo covers repo-only queries; this one covers
-- the combined lookup used by FindSessionID and pattern backfills.

CREATE INDEX IF NOT EXISTS idx_sessions_repo_pr ON sessions(repo, pr_number);
