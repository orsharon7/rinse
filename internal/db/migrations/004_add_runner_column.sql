-- Migration 004: add runner column to sessions
-- The sessions table was created before the runner column was added to the
-- schema constant. This migration backfills it for existing installs.
-- ALTER TABLE is idempotent-safe: the Go migration runner checks applied state.

ALTER TABLE sessions ADD COLUMN runner TEXT;
