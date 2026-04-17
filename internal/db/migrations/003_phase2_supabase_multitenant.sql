-- Migration 003 (Phase 2, future): Supabase multi-tenant additions
-- DO NOT run against SQLite. Apply when migrating to Supabase.
--
-- Adds team_id to all Phase 1 tables and enables Row Level Security.
-- Requires auth.jwt() ->> 'team_id' claim from RINSE login JWT.

-- Teams table
CREATE TABLE IF NOT EXISTS teams (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name       TEXT NOT NULL,
  slug       TEXT NOT NULL UNIQUE,
  created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS team_members (
  team_id UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  user_id UUID NOT NULL,
  role    TEXT NOT NULL DEFAULT 'member' CHECK(role IN ('owner','admin','member')),
  PRIMARY KEY (team_id, user_id)
);

-- Add team_id to phase 1 tables
ALTER TABLE sessions       ADD COLUMN team_id UUID NOT NULL REFERENCES teams(id);
ALTER TABLE comment_events ADD COLUMN team_id UUID NOT NULL REFERENCES teams(id);
ALTER TABLE patterns       ADD COLUMN team_id UUID NOT NULL REFERENCES teams(id);

-- Indexes for team scans
CREATE INDEX ON sessions(team_id);
CREATE INDEX ON comment_events(team_id);
CREATE INDEX ON patterns(team_id);

-- Row Level Security
ALTER TABLE sessions       ENABLE ROW LEVEL SECURITY;
ALTER TABLE comment_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE patterns       ENABLE ROW LEVEL SECURITY;
ALTER TABLE teams          ENABLE ROW LEVEL SECURITY;
ALTER TABLE team_members   ENABLE ROW LEVEL SECURITY;

CREATE POLICY "team_isolation" ON sessions
  FOR ALL USING (team_id = (auth.jwt() ->> 'team_id')::uuid);

CREATE POLICY "team_isolation" ON comment_events
  FOR ALL USING (team_id = (auth.jwt() ->> 'team_id')::uuid);

CREATE POLICY "team_isolation" ON patterns
  FOR ALL USING (team_id = (auth.jwt() ->> 'team_id')::uuid);

CREATE POLICY "own_teams" ON teams
  FOR ALL USING (id = (auth.jwt() ->> 'team_id')::uuid);

CREATE POLICY "own_team_members" ON team_members
  FOR ALL USING (team_id = (auth.jwt() ->> 'team_id')::uuid);
