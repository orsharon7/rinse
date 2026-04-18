## RINSE — Copilot Code Review Instructions

### What RINSE is
RINSE is a Go CLI/TUI application that drives AI coding agents in a loop to fix GitHub Copilot PR review comments until the PR is approved. Stack: Go 1.24+, Bubble Tea (TUI), Lip Gloss (styling), SQLite (telemetry), `gh` CLI (GitHub API). Module: `github.com/orsharon7/rinse`.

Key packages:
- `internal/cli/` — `--help`, `rinse start`, `rinse status`, dependency/auth checks (`deps.go`)
- `internal/tui/` — PR picker, onboarding wizard, monitor, edge-case screens, wizard
- `internal/notify/` — best-effort desktop notifications (`osascript` / `notify-send`); errors must always be silenced
- `internal/config/` — per-repo and user config persistence
- `internal/session/` — session JSON persistence under `~/.rinse/sessions/`
- `internal/stats/` — session history aggregation and stats printing
- `internal/db/` — SQLite telemetry at `~/.rinse/rinse.db`
- `internal/engine/` — runner dispatch, lock, state machine
- `internal/onboarding/` — first-run state, cycles API, TOML config

### Review focus — flag ONLY these
- Bugs, logic errors, off-by-one errors, incorrect control flow
- Missing error handling: unchecked error returns, ignored errors with `_`, nil dereferences
- Security issues: hardcoded secrets, path traversal, unsafe exec, SQL injection
- Race conditions, goroutine leaks, missing context cancellation
- Data loss risks: SQLite writes without transactions where atomicity is needed
- Breaking changes to public CLI flags or API contracts

### Skip entirely — do NOT comment on
- Code style, formatting, naming conventions (gofmt handles this)
- Import ordering or grouping
- Line length or whitespace
- Missing comments or documentation on unexported functions
- TODO / FIXME comments
- Test file style (only flag correctness in tests)
- Minor refactoring suggestions when code is correct
- Bubble Tea Model/Update/View boilerplate patterns — these are idiomatic

### Notification rules (`internal/notify/`)
- Flag: any `notify.*` call that can propagate an error to the caller — errors MUST be silenced
- Flag: notification logic that would block the main goroutine (must be fire-and-forget)
- Skip: platform-specific notification style choices (osascript AppleScript body text, etc.)

### Go-specific rules
- Flag: `err` returned but not checked
- Flag: goroutines started without `context.Context` or cancellation path
- Flag: `defer` inside a loop
- Flag: integer overflow risks in time calculations
- Skip: gofmt-style issues (handled by CI)
- Skip: debates about struct field ordering

### SQLite / database rules
- Flag: writes outside a transaction when atomicity matters
- Flag: missing index on frequently queried columns
- Flag: SQL queries with user-controlled input without parameterization
- Skip: minor query style preferences

### Review style
- Be concise and specific. One sentence per issue.
- Say what's wrong and what to do instead.
- Skip issues you're not confident about — false positives waste cycle time.
