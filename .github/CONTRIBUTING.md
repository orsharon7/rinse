# Contributing to RINSE

Thanks for your interest in contributing. RINSE is a Go TUI application that
drives GitHub Copilot through PR review loops automatically. This guide covers
everything you need to go from zero to merged PR.

---

## Prerequisites

| Tool | Minimum version | Purpose |
|------|----------------|---------|
| Go | 1.24 | Build the `rinse` binary |
| gh | 2.88 | GitHub CLI — used by all runners |
| jq | any | Required by shell runner scripts |
| git | any | Required by the reflection agent |

---

## Get the code

```bash
git clone https://github.com/orsharon7/rinse.git
cd rinse
```

---

## Build

```bash
# Build for the current platform (outputs ./rinse)
make build

# Build and install to ~/.local/bin/rinse
make install

# Build for all platforms into dist/
make cross

# Remove build artifacts
make clean
```

Without `-ldflags`, the binary reports `rinse dev`. The Makefile injects the
current git tag automatically via `-X main.version=...`.

To build manually (when you need to override the install path):

```bash
mkdir -p ~/.local/bin
go build -ldflags "-X main.version=$(git describe --tags --always)" \
  -o ~/.local/bin/rinse .
```

---

## Runner scripts

`make install` installs the `rinse` binary only. The runner scripts in
`scripts/` are not installed automatically. To use RINSE after installation:

- Copy `scripts/` to a directory next to the installed binary, **or**
- Set `RINSE_SCRIPT_DIR` to point to your local `scripts/` directory.

```bash
export RINSE_SCRIPT_DIR=/path/to/rinse/scripts
```

---

## Run tests

```bash
go test ./...
```

Shell scripts can be syntax-checked with:

```bash
bash -n scripts/pr-review-opencode.sh
bash -n scripts/pr-review-claude-v2.sh
```

---

## Test your changes manually

After building the binary (`make build`), use these workflows to validate your changes:

**Test `--help` text:**
```bash
./rinse --help
./rinse help
```

**Test the CLI subcommands without a live GitHub PR:**
```bash
# Check status of PR #1 (adjust to a real PR in a repo you have access to)
./rinse status 1 --repo owner/repo --json

# Check predict with no PR (reads staged git diff)
./rinse predict --json
```

**Test predict Pro-gated flags (Free tier should show upgrade prompt):**
```bash
# Without pro:true in ~/.rinse/config.json — should show upgrade prompt and exit 0
./rinse predict --interactive
./rinse predict --doc-drift

# With Pro enabled:
# echo '{"pro":true}' > ~/.rinse/config.json
# ./rinse predict --doc-drift   # runs doc-drift detector
```

**Test rinse stats --predict (hit-rate dashboard):**
```bash
# Run at least one predict first so events are recorded
./rinse predict --json
# Then view the dashboard
./rinse stats --predict
```

**Test the native Go runner (rinse run):**
```bash
# Dry-run against a real PR — streams NDJSON to stdout
./rinse run 42 --repo owner/repo --json
```

**Test the first-run onboarding wizard:**
```bash
# Delete onboarding state to trigger the wizard on next launch
# macOS:
rm -f ~/Library/Application\ Support/rinse/onboarding-state.json
# Linux:
rm -f ~/.config/rinse/onboarding-state.json

./rinse  # wizard runs on next launch
```

**Test stats collection:**
```bash
./rinse opt-in      # enable session recording
./rinse stats       # show 30-day rolling summary (requires at least one session)
./rinse opt-out     # disable
```

---

## Project layout

```
rinse/
├── main.go                     # Entry point, CLI arg dispatch (help/version/stats/report handled here)
├── Makefile                    # build / install / cross / clean targets
├── go.mod                      # Module: github.com/orsharon7/rinse
├── internal/
│   ├── cli/                    # CLI orchestration layer (subcommands, --help, deps check)
│   ├── db/                     # Local session storage (SQLite)
│   ├── engine/                 # Review cycle engine
│   │   └── lock/               # Atomic lock primitives
│   ├── notify/                 # Desktop notification helper (macOS/Linux)
│   ├── onboarding/             # First-run wizard state, cycles API, TOML config
│   ├── predict/                # Pattern prediction (pre-run review signal)
│   │                           # predict.go — AST/text heuristics, LogEvent
│   │                           # doc_drift.go — LLM documentation drift (--doc-drift, Pro)
│   ├── quality/                # Code quality delta measurement
│   ├── reflect/                # Reflection agent (AGENTS.md / CLAUDE.md updates)
│   ├── runner/                 # PR review loop runner
│   ├── session/                # Per-run session JSON persistence
│   ├── stats/                  # Stats + trends commands
│   ├── theme/                  # TUI colour palette and shared styles
│   └── tui/                    # Interactive TUI (PR picker, wizard, monitor)
├── scripts/
│   ├── pr-review-opencode.sh   # opencode runner (GitHub Copilot, default)
│   ├── pr-review-claude-v2.sh  # claude runner (requires Anthropic API key)
│   └── pr-review-session.sh    # Session wrapper (labels, locking)
└── .github/
    ├── CONTRIBUTING.md         # This file
    └── workflows/              # CI workflows
```

---

## Environment variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `RINSE_SCRIPT_DIR` | binary dir | Directory containing runner scripts |
| `PR_REVIEW_SCRIPT_DIR` | — | Legacy alias for `RINSE_SCRIPT_DIR` |
| `RINSE_API_URL` | `http://localhost:7433` | Override the local RINSE backend URL |
| `NO_COLOR` | — | Disable ANSI colour output (follows no-color.org) |

---

## Submitting a PR

### Branch naming

```
<type>/<short-slug>[-ISSUE-ID]

Examples:
  feat/reflect-agent-RIN-42
  fix/lock-race-on-sigterm
  docs/contributing-RIN-111
  copy/onboarding-wizard-RIN-107
```

### Commit style

Follow Conventional Commits. Subject ≤ 72 characters.

```
feat: add rinse report command with daily dashboard
fix: clamp negative duration_seconds to 0 in opencode runner
docs: document rinse init and rinse report in --help
copy: fix first-run onboarding wizard — replace laundry placeholders
```

### PR checklist

- [ ] `go build ./...` passes (no compile errors)
- [ ] `go test ./...` passes
- [ ] Shell scripts pass `bash -n` syntax check if modified
- [ ] `rinse --help` is up to date if new subcommands were added
- [ ] PR description includes: what changed, why, and how to test it

---

## Label system

RINSE uses a two-tier label system. Understanding which tier a label belongs
to matters because **tier-1 labels are machine-managed** — touching them
during an active cycle can break the runner's lock logic.

**Tier 1 — RINSE workflow labels** are applied and removed automatically by
the runner scripts during review cycles. They are a product surface: when a
developer sees `rinse:running` on their PR in GitHub's UI, that is RINSE
communicating its state. Do not manually remove these labels while a cycle
is active.

**Tier 2 — Human labels** are applied manually by contributors and
maintainers for triage, routing, and milestone tracking.

### Tier 1 — RINSE workflow labels (auto-applied by scripts)

> **`rinse:running` is a lock signal.** Removing it manually will not stop
> the running cycle and may cause race conditions. Wait for the cycle to
> complete, or kill the RINSE process first.

| Label | Color | When applied | When removed |
|-------|-------|-------------|--------------|
| `rinse:running` | `#8B5CF6` (purple) | Cycle starts — lock acquired | Cycle ends (any outcome) |
| `rinse:approved` | `#10B981` (green) | Copilot approves the PR | Next cycle starts on same PR |
| `rinse:needs-work` | `#F59E0B` (amber) | Cycle ends with unresolved comments | Next cycle starts on same PR |

The brand colors (purple/green/amber) are intentional — `rinse:*` labels are
a product surface in GitHub's UI and should be visually distinct from
human-applied labels.

### Tier 2 — Human labels

#### Component labels (which part of the codebase)

Use these to route issues and PRs to the right area. Apply one or more.

| Label | Color | Area |
|-------|-------|------|
| `cli` | `#0075ca` | CLI subcommands, flag parsing, `--help` text |
| `db` | `#0052CC` | SQLite session storage (`internal/db`) |
| `docs` | `#0075ca` | Documentation files (README, CONTRIBUTING, --help copy) |
| `engine` | `#0075ca` | Review cycle engine and runner scripts |
| `predict` | `#7057ff` | Pattern prediction, doc-drift detection, hit-rate tracking (`internal/predict`) |
| `tui` | `#1d76db` | Interactive TUI (PR picker, onboarding wizard, monitor) |
| `brand` | `#8B5CF6` | Brand assets, visual identity |
| `legal` | `#cfd3d7` | License, legal notices |

> **`docs` vs `documentation`:** `docs` is a component label — use it for
> issues about documentation files. `documentation` is the standard GitHub
> label with a broader meaning ("improvements or additions to documentation").
> Either is fine; `docs` is preferred for routing to the dev-advocate queue.

#### Priority labels (how urgent)

| Label | Color | Meaning |
|-------|-------|---------|
| `p0-critical` | `#b60205` | Blocks a release or breaks existing users — fix immediately |
| `p1-important` | `#d93f0b` | Should land in the next release cycle |
| `p2-nice` | `#e4e669` | Improvement, not blocking anything |

#### Milestone labels (which release)

| Label | Color | Meaning |
|-------|-------|---------|
| `milestone:v0.2` | `#c5def5` | Target: v0.2 release |
| `milestone:v0.3` | `#c5def5` | Target: v0.3 release |
| `milestone:v0.4` | `#c5def5` | Target: v0.4 release (shipped: `--doc-drift`, `--interactive`, `stats --predict`) |
| `milestone:v1.0` | `#c5def5` | Target: v1.0 release |

#### Standard GitHub labels

These are GitHub's built-in labels, kept as-is:

| Label | Meaning |
|-------|---------|
| `bug` | Something isn't working |
| `documentation` | Improvements or additions to documentation |
| `duplicate` | This issue or pull request already exists |
| `enhancement` | New feature or request |
| `good first issue` | Good for newcomers |
| `help wanted` | Extra attention is needed |
| `invalid` | This doesn't seem right |
| `wontfix` | This will not be worked on |

---

## Code style

- Follow the patterns in `CLAUDE.md` / `AGENTS.md` at the repo root — these
  are maintained automatically by the reflection agent.
- Shell scripts must pass `bash -n` syntax check before committing.
- Use `grep -E` for alternation; `\|` in BRE is non-portable on BSD/macOS.
- Read interactive input from `/dev/tty`; render UI output to stderr.
- Every new subcommand added to `main.go` or `TryDispatch()` must be:
  - dispatched in `internal/cli/cli.go` → `TryDispatch()`
  - documented in `cli.PrintHelp()` (USAGE list + COMMANDS section)
  - listed in the README quick reference if it has user-facing output

---

## Getting help

Open an issue with the `help wanted` label, or start a discussion on GitHub.
