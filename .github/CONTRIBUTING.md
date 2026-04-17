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

## Project layout

```
rinse/
├── main.go                     # Entry point, CLI arg dispatch, helpText
├── Makefile                    # build / install / cross / clean targets
├── go.mod                      # Module: github.com/orsharon7/rinse
├── internal/
│   ├── cli/                    # CLI orchestration layer
│   ├── db/                     # Local session storage (SQLite)
│   ├── engine/                 # Review cycle engine
│   │   └── lock/               # Atomic lock primitives
│   ├── onboarding/             # First-run wizard state, cycles API, TOML config
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
| `RINSE_WEBHOOK_URL` | — | POST a JSON payload here after each completed cycle |
| `RINSE_API_URL` | `http://localhost:7433` | Override the local RINSE backend URL |

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
docs: document rinse init and rinse trends in --help
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

RINSE uses a structured label system. Labels are managed in the repo and
applied automatically by the runner scripts during review cycles.

### RINSE workflow labels (auto-applied by scripts)

| Label | Color | Meaning |
|-------|-------|---------|
| `rinse:running` | `#8B5CF6` | RINSE is actively reviewing this PR — do not merge |
| `rinse:approved` | `#10B981` | Copilot approved — ready to merge |
| `rinse:needs-work` | `#F59E0B` | RINSE cycle found issues |

### Component Labels

| Label | Color | Description |
|-------|-------|-------------|
| `cli` | `#0075ca` | Command-line interface |
| `db` | `#0052CC` | Database |
| `docs` | `#0075ca` | Documentation |
| `legal` | `#cfd3d7` | Legal |
| `brand` | `#8B5CF6` | Brand |
| `engine` | `#0075ca` | RINSE engine/runner scripts |
| `tui` | `#1d76db` | RINSE TUI application |

### Standard Labels

| Label | Color | Description |
|-------|-------|-------------|
| `bug` | `#d73a4a` | Something isn't working |
| `documentation` | `#0075ca` | Improvements or additions to documentation |
| `duplicate` | `#cfd3d7` | This issue or pull request already exists |
| `enhancement` | `#a2eeef` | New feature or request |
| `help wanted` | `#008672` | Extra attention is needed |
| `good first issue` | `#7057ff` | Good for newcomers |
| `invalid` | `#e4e669` | This doesn't seem right |
| `wontfix` | `#ffffff` | This will not be worked on |

### Priority Labels

| Label | Color | Description |
|-------|-------|-------------|
| `p0-critical` | `#b60205` | Must-have for launch |
| `p1-important` | `#d93f0b` | High priority |
| `p2-nice` | `#e4e669` | Nice to have |

### Milestone Labels

| Label | Color | Description |
|-------|-------|-------------|
| `milestone:v0.2` | `#c5def5` | Target: v0.2 release |
| `milestone:v0.3` | `#c5def5` | Target: v0.3 release |
| `milestone:v1.0` | `#c5def5` | Target: v1.0 release |

### Standard GitHub labels

`bug`, `documentation`, `duplicate`, `enhancement`, `good first issue`,
`help wanted`, `invalid`, `wontfix`

---

## Code style

- Follow the patterns in `CLAUDE.md` / `AGENTS.md` at the repo root — these
  are maintained automatically by the reflection agent.
- Shell scripts must pass `bash -n` syntax check before committing.
- Use `grep -E` for alternation; `\|` in BRE is non-portable on BSD/macOS.
- Read interactive input from `/dev/tty`; render UI output to stderr.
- Every new subcommand added to `main.go` must be documented in `cli.PrintHelp()` in `internal/cli/cli.go`.

---

## Getting help

Open an issue with the `help wanted` label, or start a discussion on GitHub.
