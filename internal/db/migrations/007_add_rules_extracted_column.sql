-- Migration 007: add rules_extracted column to sessions
--
-- rules_extracted tracks the number of coding rules committed to AGENTS.md
-- by the reflect agent (--reflect flag). Previously this was only persisted
-- to JSON session files; this column brings it into the DB so stats and
-- dashboards can aggregate it without parsing JSON.
--
-- Default 0 so existing rows are valid without backfill.
ALTER TABLE sessions ADD COLUMN rules_extracted INTEGER DEFAULT 0;
