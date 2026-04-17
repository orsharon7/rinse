# RINSE Telemetry Architecture

> **This document is a forward-looking specification, not a description of current behavior.**
> Phase 1 (local SQLite) → Phase 2 (Supabase + multi-tenant). Paths, schemas, and commands below are proposals; none are implemented yet unless noted otherwise.

---

## Phase 1: Local SQLite Schema (Proposed)

> **Not yet implemented.** RINSE currently stores session history as JSON files under `~/.rinse/sessions/`. There is no SQLite database in the current codebase. The schema below is a proposal for future structured persistence.

```sql
CREATE TABLE sessions (
  id          TEXT PRIMARY KEY,      -- UUID
  started_at  DATETIME NOT NULL,
  ended_at    DATETIME,
  duration_s  INTEGER,
  cycle_count INTEGER DEFAULT 0,     -- PR review iterations
  notes       TEXT
);

CREATE TABLE cycles (
  id          TEXT PRIMARY KEY,      -- UUID
  session_id  TEXT NOT NULL REFERENCES sessions(id),
  type        TEXT NOT NULL,         -- e.g. 'fix_iteration' | 'review_wait'
  started_at  DATETIME NOT NULL,
  ended_at    DATETIME,
  duration_s  INTEGER,
  completed   BOOLEAN DEFAULT FALSE
);

CREATE TABLE config_snapshots (
  id          TEXT PRIMARY KEY,
  session_id  TEXT NOT NULL REFERENCES sessions(id),
  runner      TEXT,                  -- e.g. 'opencode' | 'claude'
  model       TEXT,
  captured_at DATETIME NOT NULL
);
```

---

## Phase 2: Schema Diff (Supabase + Multi-Tenancy)

### New tables

```sql
CREATE TABLE teams (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name       TEXT NOT NULL,
  slug       TEXT NOT NULL UNIQUE,
  created_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE team_members (
  team_id UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  user_id UUID NOT NULL,             -- Supabase auth.users.id
  role    TEXT NOT NULL DEFAULT 'member', -- 'owner' | 'member'
  PRIMARY KEY (team_id, user_id)
);
```

### Additions to Phase 1 tables

Every Phase 1 table gets `team_id uuid NOT NULL REFERENCES teams(id)`:

```sql
ALTER TABLE sessions         ADD COLUMN team_id UUID NOT NULL REFERENCES teams(id);
ALTER TABLE cycles           ADD COLUMN team_id UUID NOT NULL REFERENCES teams(id);
ALTER TABLE config_snapshots ADD COLUMN team_id UUID NOT NULL REFERENCES teams(id);
```

Index for common query patterns:

```sql
CREATE INDEX idx_sessions_team    ON sessions(team_id, started_at DESC);
CREATE INDEX idx_cycles_team      ON cycles(team_id, started_at DESC);
```

---

## Row-Level Security (RLS)

All tables have RLS enabled. Policy: a row is visible only when `team_id` matches the `team_id` claim in the caller's JWT.

```sql
-- Enable RLS
ALTER TABLE sessions         ENABLE ROW LEVEL SECURITY;
ALTER TABLE cycles           ENABLE ROW LEVEL SECURITY;
ALTER TABLE config_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE teams            ENABLE ROW LEVEL SECURITY;
ALTER TABLE team_members     ENABLE ROW LEVEL SECURITY;

-- Isolation policy (applied to each table)
CREATE POLICY "team_isolation" ON sessions
  FOR ALL USING (team_id = (auth.jwt() ->> 'team_id')::uuid);

CREATE POLICY "team_isolation" ON cycles
  FOR ALL USING (team_id = (auth.jwt() ->> 'team_id')::uuid);

CREATE POLICY "team_isolation" ON config_snapshots
  FOR ALL USING (team_id = (auth.jwt() ->> 'team_id')::uuid);

-- Teams: visible to members only
CREATE POLICY "team_self" ON teams
  FOR SELECT USING (
    id IN (SELECT team_id FROM team_members WHERE user_id = auth.uid())
  );

-- Team members: visible to members of same team
CREATE POLICY "team_members_visible" ON team_members
  FOR SELECT USING (
    team_id IN (SELECT team_id FROM team_members WHERE user_id = auth.uid())
  );
```

---

## Auth Flow (Proposed — Not Yet Implemented)

> **Not yet implemented.** The `rinse login` command, JWT storage path, and refresh-token logic below are proposals for Phase 2 and do not exist in the current codebase.

```
rinse login  [proposed]
  └─> opens browser → Supabase OAuth (GitHub / email)
  └─> Supabase issues JWT with claims:
        { "sub": "<user_id>", "team_id": "<team_uuid>", "role": "member" }
  └─> JWT stored in ~/.rinse/token (file-mode 600)  [proposed path]

Every sync request:
  Authorization: Bearer <JWT>
  → Supabase validates signature + expiry
  → RLS policies enforce team_id isolation automatically
```

JWT refresh: `rinse` checks `exp` before each sync. If within 5 min of expiry, refreshes silently using the stored refresh token. (Proposed — not yet implemented.)

---

## Sync Strategy (Proposed — Not Yet Implemented)

> **Not yet implemented.** The `rinse sync` command and the sync strategy below are proposals for Phase 2.

Local-first. Data is always written to SQLite first. Cloud sync is best-effort.

```
rinse stop  (or auto-trigger on session end)  [proposed]
  └─> write session + cycles to local SQLite
  └─> rinse sync  [proposed command]
        └─> read unsynced rows (WHERE synced_at IS NULL)
        └─> upsert to Supabase via REST API (idempotent by primary key UUID)
        └─> mark rows synced_at = now() in local DB

rinse sync --force  → re-upsert all rows regardless of synced_at  [proposed]
```

Conflict resolution: **last-write-wins by `id` (UUID)**. Because IDs are generated client-side as UUIDs, there are no key collisions across devices. If the same session is pushed twice, `ON CONFLICT (id) DO UPDATE` overwrites with the latest values — safe because a session is complete before sync.

```sql
-- Example upsert (Supabase PostgREST handles this via Prefer: resolution=merge-duplicates)
INSERT INTO sessions (...) VALUES (...)
ON CONFLICT (id) DO UPDATE SET
  ended_at    = EXCLUDED.ended_at,
  duration_s  = EXCLUDED.duration_s,
  cycle_count = EXCLUDED.cycle_count;
```

Offline behaviour: `rinse sync` exits 0 with a warning if Supabase is unreachable. Unsynced rows accumulate and are pushed on next successful sync.

---

## Local Dev Setup

```bash
brew install supabase/tap/supabase
cd /path/to/rinse
supabase init
supabase start
# Studio:    http://localhost:54323
# DB:        postgresql://postgres:postgres@localhost:54322/postgres
# API URL:   http://localhost:54321
# Anon key:  printed by supabase start
```

Environment config for local dev. RINSE currently stores config as JSON in your user config directory (on Linux, typically `~/.config/rinse/config.json`). The TOML format below is a proposed future alternative:

```toml
# Proposed future config format
[supabase]
url     = "http://localhost:54321"
anon_key = "<local-anon-key>"
```

---

## Open Questions / Phase 3 Notes

- Dashboard UI: separate web app or embedded in `rinse tui`? (not scoped to Phase 2)
- Team provisioning: self-serve signup or invite-only? (affects `teams` insert policy)
- Rate limiting on `rinse sync`: consider exponential backoff on 429/503
