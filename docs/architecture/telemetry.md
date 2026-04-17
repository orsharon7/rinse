# RINSE Telemetry Architecture

## Phase 1: Current Local Storage

The currently implemented telemetry writes newline-delimited JSON (JSON Lines) to `~/.rinse/stats.json`.

This is the current on-disk format used by the telemetry scripts in this PR. SQLite is a future design target, not the current Phase 1 implementation.

## Roadmap: Local SQLite Schema

Planned future local database path: `~/.rinse/rinse.db`.

```sql
-- Wash sessions (one per rinse run)
CREATE TABLE sessions (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  started_at  TIMESTAMP NOT NULL,
  ended_at    TIMESTAMP,
  duration_s  INTEGER,
  cycle_count INTEGER DEFAULT 0
);

-- Individual wash cycles within a session
CREATE TABLE cycles (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  session_id  UUID NOT NULL REFERENCES sessions(id),
  started_at  TIMESTAMP NOT NULL,
  ended_at    TIMESTAMP,
  duration_s  INTEGER,
  phase       TEXT  -- wash, rinse, spin, dry
);

-- Config snapshot captured at session start
CREATE TABLE config_snapshots (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  session_id  UUID NOT NULL REFERENCES sessions(id),
  captured_at TIMESTAMP NOT NULL,
  config_json JSONB NOT NULL
);
```

## Phase 2: Supabase + Multi-Tenant Schema Diff

### New tables

```sql
CREATE TABLE teams (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name       TEXT NOT NULL,
  slug       TEXT NOT NULL UNIQUE,
  created_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE TABLE team_members (
  team_id UUID NOT NULL REFERENCES teams(id),
  user_id UUID NOT NULL REFERENCES auth.users(id),
  role    TEXT NOT NULL DEFAULT 'member', -- owner | admin | member
  PRIMARY KEY (team_id, user_id)
);
```

### Phase 1 table additions

Add `team_id` to every Phase 1 table:

```sql
ALTER TABLE sessions        ADD COLUMN team_id UUID NOT NULL REFERENCES teams(id);
ALTER TABLE cycles          ADD COLUMN team_id UUID NOT NULL REFERENCES teams(id);
ALTER TABLE config_snapshots ADD COLUMN team_id UUID NOT NULL REFERENCES teams(id);
```

## Row Level Security

Enable RLS and isolate rows by team via the JWT `team_id` claim:

```sql
ALTER TABLE sessions         ENABLE ROW LEVEL SECURITY;
ALTER TABLE cycles           ENABLE ROW LEVEL SECURITY;
ALTER TABLE config_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE teams            ENABLE ROW LEVEL SECURITY;
ALTER TABLE team_members     ENABLE ROW LEVEL SECURITY;

-- Sessions
CREATE POLICY team_isolation ON sessions
  FOR ALL USING (team_id = (auth.jwt() ->> 'team_id')::uuid);

-- Cycles
CREATE POLICY team_isolation ON cycles
  FOR ALL USING (team_id = (auth.jwt() ->> 'team_id')::uuid);

-- Config snapshots
CREATE POLICY team_isolation ON config_snapshots
  FOR ALL USING (team_id = (auth.jwt() ->> 'team_id')::uuid);

-- Teams: members can see their own team only
CREATE POLICY team_self ON teams
  FOR SELECT USING (
    id IN (SELECT team_id FROM team_members WHERE user_id = auth.uid())
  );

-- Team members: visible within team
CREATE POLICY team_members_isolation ON team_members
  FOR ALL USING (team_id = (auth.jwt() ->> 'team_id')::uuid);
```

## Auth Flow

1. User runs `rinse login`
2. Supabase OAuth flow opens in browser
3. On success, Supabase returns a JWT containing a custom `team_id` claim (set via a database hook on `auth.users` insert)
4. JWT stored at `~/.rinse/token`
5. All subsequent API calls include `Authorization: Bearer <token>`
6. RLS policies enforce team isolation server-side

## Sync Strategy

Local-first: the CLI always writes to `~/.rinse/rinse.db` (SQLite) first.

After a session ends, `rinse sync` upserts to Supabase:

```
rinse session end
  -> write to SQLite
  -> upsert sessions, cycles, config_snapshots to Supabase (by UUID PK)
```

Conflict resolution: **last-write-wins by UUID PK**. Because session and cycle IDs are UUIDs generated locally, two devices cannot produce the same ID, so conflicts are structurally impossible. A re-upload simply overwrites the same row idempotently.

## Local Dev Setup

```bash
brew install supabase/tap/supabase
cd /path/to/rinse
supabase init
supabase start
# Studio:    http://localhost:54323
# DB:        postgresql://postgres:postgres@localhost:54322/postgres
# API URL:   http://localhost:54321
# Anon key:  printed by `supabase start`
```

Set in `.env.local`:
```
SUPABASE_URL=http://localhost:54321
SUPABASE_ANON_KEY=<printed above>
```

## Open Questions (Phase 3 scope)

- Offline-first conflict window: what happens if two devices sync the same session after editing local SQLite?
- Retention policy: how long do we keep raw cycle data in Supabase?
- Dashboard read path: direct Supabase client or dedicated API layer?
- Team invite flow: how does a new user get assigned to a `team_id` at OAuth time?
