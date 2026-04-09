# pr-review / claude

Automated Copilot PR review loop driven by **Claude Code CLI** (`claude`).

Runs `claude --print` in a loop to fix Copilot comments until the PR is approved or returns 0 comments.

## Usage

```bash
./pr-review-claude.sh <pr_number> [options]
```

### Options

| Flag | Default | Description |
|------|---------|-------------|
| `--repo <owner/repo>` | auto-detect | GitHub repo |
| `--cwd <path>` | current dir | Local repo path |
| `--max-iter <n>` | 10 | Max fix iterations before giving up |
| `--wait-max <seconds>` | 300 | Max seconds to wait per Copilot review |

### Example

```bash
./pr-review-claude.sh 42 \
  --repo orsharon7/gsc-solar-monitor \
  --cwd ~/dev/gsc-solar-monitor
```

## How it works

Each iteration:

1. **`cycle`** — waits for Copilot to finish reviewing (auto-requests if needed, handles stalls)
2. **Exit check** — `approved` / `clean` (0 comments) → done; `merged` / `closed` → stop
3. **`claude --print`** — fixes all comments, pushes, re-requests Copilot review, replies to each comment
4. **State save** — writes the review ID to `/tmp/pr-review-state/pr-<N>-last-review` so the next cycle knows to request a fresh review
5. Repeat

### Startup behaviour

On first run (no state file), the script checks the current PR state and seeds the state file so `cycle` correctly requests a fresh Copilot review rather than re-processing an already-seen review.

## Requirements

- `claude` CLI in PATH (`claude --version`)
- `gh` CLI authenticated
- `pr-review.sh` in parent directory (`../pr-review.sh`)

## Log file

All output is streamed to `~/.pr-review-claude.log`.

## `.claude/settings.local.json`

Pre-configured tool permissions for the Claude Code session that runs inside the loop.
