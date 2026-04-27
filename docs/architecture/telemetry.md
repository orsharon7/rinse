# RINSE Telemetry Architecture

Local-first telemetry: all data is written to SQLite on the developer's machine.
Phase 2 (not yet built) adds a Supabase cloud sync path with multi-tenant RLS.

---

## Phase 1: Local SQLite (Current)

### Database location

```
~/.rinse/rinse.db
```

Opened via `db.OpenDefault()` in `internal/db/db.go`. Created on first run.
WAL mode enabled; single writer, safe for concurrent readers.

### Schema

```sql
CREATE TABLE sessions (
  id                            TEXT PRIMARY KEY,   -- UUID v4
  repo                          TEXT NOT NULL,       -- "owner/repo"
  pr_number                     INTEGER NOT NULL,
  pr_title                      TEXT,
  branch                        TEXT,
  runner                        TEXT,               -- "opencode" | "claude"
  model                         TEXT,
  started_at                    DATETIME NOT NULL,
  completed_at                  DATETIME,
  duration_seconds              INTEGER,
  estimated_time_saved_seconds  INTEGER,            -- heuristic: 4 min × comments
  iterations                    INTEGER DEFAULT 0,
  total_comments_fixed          INTEGER DEFAULT 0,
  outcome TEXT CHECK(outcome IN ('merged','closed','open','failed','approved')),
  created_at                    DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE comment_events (
  id            TEXT PRIMARY KEY,
  session_id    TEXT REFERENCES sessions(id) ON DELETE CASCADE,
  iteration     INTEGER NOT NULL,
  comment_count INTEGER NOT NULL,
  recorded_at   DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE patterns (
  id         TEXT PRIMARY KEY,
  session_id TEXT REFERENCES sessions(id) ON DELETE CASCADE,
  pattern    TEXT NOT NULL,
  count      INTEGER DEFAULT 1
);

-- Migration versioning (added in migration 004+)
CREATE TABLE schema_migrations (
  version     INTEGER PRIMARY KEY,
  name        TEXT NOT NULL,
  applied_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

### Indexes

```sql
CREATE INDEX idx_sessions_repo        ON sessions(repo);
CREATE INDEX idx_sessions_started_at  ON sessions(started_at);
CREATE INDEX idx_sessions_outcome     ON sessions(outcome);
CREATE INDEX idx_sessions_repo_pr     ON sessions(repo, pr_number);  -- FindSessionID
CREATE INDEX idx_comment_events_session ON comment_events(session_id);
CREATE INDEX idx_patterns_session       ON patterns(session_id);
```

### Write path

**Runner path** (`internal/runner/runner.go`):
1. `db.InsertSession(...)` — outcome=`"open"` on start
2. `db.InsertCommentEvent(...)` — one row per iteration
3. `db.FinalizeSession(...)` — stamps `completed_at`, `outcome`, `duration_seconds`
4. `db.InsertPatterns(...)` — pattern strings extracted from reflect output

**TUI monitor path** (`internal/tui/monitor.go`):
- On session end: `db.FindSessionID(repo, prNumber)` → `db.SavePatterns(sessionID, patterns)`
- Patterns are extracted from reflect-phase output lines

### Outcome values

**DB (SQLite CHECK constraint on `sessions.outcome`)** — these are the only values the
runner ever writes to SQLite:

| Value | Set by | Meaning |
|-------|--------|---------|
| `open` | `db.InsertSession` | session in progress (initial insert) |
| `approved` | `db.FinalizeSession` | Copilot approved the PR |
| `failed` | `db.FinalizeSession` | runner error **or** max iterations reached |
| `closed` | `db.FinalizeSession` | PR was closed without approval |
| `merged` | legacy path | PR was merged (pre-runner JSON sessions backfilled into DB) |

**`stats.go` in-memory constants** (`internal/stats/stats.go`) — used for JSON session
files (`~/.rinse/sessions/*.json`) and as typed aliases when reading sessions.  These
constants extend the vocabulary beyond the DB CHECK set:

| Constant | Value | Meaning |
|----------|-------|---------|
| `OutcomeApproved` | `"approved"` | Copilot approved |
| `OutcomeMerged` | `"merged"` | PR merged (legacy) |
| `OutcomeClosed` | `"closed"` | PR closed without approval |
| `OutcomeMaxIter` | `"max_iterations"` | hit iteration cap (JSON sessions only) |
| `OutcomeError` | `"error"` | runner error (JSON sessions only) |
| `OutcomeAborted` | `"aborted"` | run aborted by user (JSON sessions only) |

> **Note:** `"max_iterations"`, `"error"`, and `"aborted"` are **not** in the DB CHECK
> constraint. Fresh runner sessions map both errors and max-iterations to `"failed"` in
> SQLite. The richer constants exist for legacy JSON session compatibility and future
> per-cause reporting (see Known Gaps below).

### Migration versioning

Migrations are tracked in `schema_migrations`. `db.Open()` calls `applyMigrations()`
on every open — each migration runs at most once per install.

| Version | Name | SQL |
|---------|------|-----|
| 001–003 | Initial schema + indexes + Phase 2 Supabase spec | `migrations/001_initial_schema.sql` |
| 004 | Add `runner` column to `sessions` | `migrations/004_add_runner_column.sql` |
| 005 | Composite index `idx_sessions_repo_pr` | `migrations/005_idx_sessions_repo_pr.sql` |

### Stats integration

`internal/stats/stats.go` reads from both SQLite (preferred) and `~/.rinse/sessions/*.json`
(legacy fallback). Sessions are deduped by `(repo, pr_number, started_at truncated to minute)`.
DB sessions take precedence over JSON sessions for the same fingerprint.

`session.Approved` is derived from `outcome IN ('approved', 'merged')`.

---

## Phase 2: Supabase Multi-Tenant (Design — Not Yet Built)

### Local dev setup

```bash
brew install supabase/tap/supabase
cd /path/to/rinse
supabase init      # creates supabase/ config dir
supabase start     # starts local Supabase stack via Docker
# Studio:    http://localhost:54323
# DB:        postgresql://postgres:postgres@localhost:54322/postgres
# API URL:   http://localhost:54321
# Anon key:  printed by `supabase start`
```

### Schema additions (from migration 003)

New tables:

```sql
CREATE TABLE teams (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name       TEXT NOT NULL,
  slug       TEXT NOT NULL UNIQUE,
  created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE team_members (
  team_id UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  user_id UUID NOT NULL,
  role    TEXT NOT NULL DEFAULT 'member' CHECK(role IN ('owner','admin','member')),
  PRIMARY KEY (team_id, user_id)
);
```

Column additions to all Phase 1 tables:

```sql
ALTER TABLE sessions       ADD COLUMN team_id UUID NOT NULL REFERENCES teams(id);
ALTER TABLE comment_events ADD COLUMN team_id UUID NOT NULL REFERENCES teams(id);
ALTER TABLE patterns       ADD COLUMN team_id UUID NOT NULL REFERENCES teams(id);
```

### Row-Level Security

```sql
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
```

### Auth flow

```
rinse login  [not yet implemented]
  └─> opens browser → Supabase OAuth (GitHub / email)
  └─> Supabase issues JWT:
        { "sub": "<user_id>", "team_id": "<team_uuid>", "role": "member" }
  └─> JWT stored at ~/.rinse/token  (file mode 600)

Each request:
  Authorization: Bearer <JWT>
  → RLS automatically enforces team_id isolation
```

JWT refresh: check `exp` before each sync; refresh silently if within 5 min of expiry.

### Sync strategy

Local-first. SQLite is the source of truth; Supabase is the sync target.

```
rinse sync  [not yet implemented]
  └─> read rows WHERE synced_at IS NULL
  └─> upsert to Supabase (ON CONFLICT (id) DO UPDATE)
  └─> mark synced_at = now() in local DB
```

Conflict resolution: last-write-wins by UUID primary key. No cross-device collisions
because UUIDs are generated client-side. Offline: accumulate unsynced rows, push on
next successful sync.

```sql
-- Supabase upsert (via PostgREST Prefer: resolution=merge-duplicates)
INSERT INTO sessions (...) VALUES (...)
ON CONFLICT (id) DO UPDATE SET
  completed_at                 = EXCLUDED.completed_at,
  duration_seconds             = EXCLUDED.duration_seconds,
  total_comments_fixed         = EXCLUDED.total_comments_fixed,
  outcome                      = EXCLUDED.outcome;
```

A `synced_at TIMESTAMPTZ` column is needed in all Phase 1 tables (not yet added —
add as migration 006 when sync is being implemented).

---

## Known gaps / future migrations

| # | Gap | Resolution |
|---|-----|------------|
| Live DB has no `outcome` CHECK constraint | `CREATE TABLE IF NOT EXISTS` can't retrofit constraints; fresh installs get it | Acceptable — runner only writes valid values |
| DB CHECK constraint vs runner outcome vocabulary mismatch | DB allows `('merged','closed','open','failed','approved')`; runner writes only `"failed"` for both errors and max-iterations; `stats.go` defines richer constants (`"error"`, `"aborted"`, `"max_iterations"`) not in the constraint — these are JSON-session-only values | Add `"error"`, `"aborted"`, `"max_iterations"` to DB CHECK and differentiate in `FinalizeSession` call sites when per-cause reporting is needed |
| `synced_at` column missing | Needed for Phase 2 sync tracking | Add as migration 006 when sync ships |
