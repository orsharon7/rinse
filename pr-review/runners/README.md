# pr-review / runners

Automated Copilot PR review loops. Pick a runner based on which AI CLI you have set up:

| Runner | CLI used | When to use |
|--------|----------|-------------|
| `pr-review-opencode.sh` | `opencode` | You have opencode authenticated with GitHub Copilot — no API key needed |
| `pr-review-claude-v2.sh` | `claude` (Claude Code) | You have the Claude Code CLI and a direct Anthropic API key |
| `pr-review-claude.sh` | `claude` (Claude Code) | Legacy v1 — use v2 instead |

Each runner fixes Copilot comments in a loop until the PR is approved or returns 0 comments, then presents an interactive merge menu.

### Terminal UI (`pr-review-ui.sh`)

Sourced automatically by both `pr-review-opencode.sh` and `pr-review-claude-v2.sh`. Do not run it directly.

When stdout is a TTY it provides:
- Colored, severity-coded log lines
- Animated progress bar while waiting for Copilot to review
- Bold section headers per iteration
- Arrow-key menu on success: merge, branch cleanup, open PR in browser

Disable with `NO_COLOR=1` or `--no-interactive` (useful in CI or when piping output).

---

## Reflection agent

**`pr-review-reflect.sh`** — Runs in parallel with each fix cycle. Reads Copilot review comments, extracts generalizable coding rules, and permanently updates `AGENTS.md` + `CLAUDE.md` in the project repo. Both files are loaded automatically by AI coding agents on every future session, so each cycle produces fewer comments.

**Rules are pushed to `main`, not the PR branch** — using a `git worktree` so the reflection commit never appears in the PR diff. This prevents Copilot from re-reviewing the rule files and avoids an infinite review loop.

Enable it with `--reflect` on any runner:

```bash
./pr-review-opencode.sh 1 --repo owner/repo --cwd /path/to/repo --reflect
```

Or run standalone:

```bash
./pr-review-reflect.sh 1 --repo owner/repo --cwd /path/to/repo --review-id 4077186198
```

**How it works:**
1. Runs in background while the fix agent is working
2. Creates a temporary `git worktree` checked out on `main`
3. Analyzes Copilot comments → identifies patterns → writes rules into the worktree (not the PR branch)
4. Script commits and pushes updated `AGENTS.md` / `CLAUDE.md` directly to `main`
5. Worktree is cleaned up automatically
6. Next fix iteration: both agents load the updated rules from `main` automatically → fewer issues → fewer review cycles

| Flag | Default | Description |
|------|---------|-------------|
| `--model <provider/model>` | same as runner | AI model for reflection |
| `--agent <opencode\|claude>` | `opencode` | Which CLI to use |
| `--main-branch <branch>` | `main` | Branch to push rules to |
| `--review-id <id>` | latest Copilot review | Specific review to analyse |
| `--dry-run` | — | Print prompt and worktree path, don't run |

---

## v2 — Recommended

**`pr-review-claude-v2.sh`** — Standalone, unlimited iterations, model-agnostic.

- Uses `gh pr edit --add-reviewer @copilot` (official gh CLI v2.88+)
- No hard iteration cap — runs until Copilot is satisfied
- `--model` parameter (default: `claude-sonnet-4-6`)
- Correctly handles existing unresolved reviews on startup
- No dependency on `pr-review.sh`

```bash
./pr-review-claude-v2.sh <pr_number> [options]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--repo <owner/repo>` | auto-detect | GitHub repo |
| `--cwd <path>` | current dir | Local repo path |
| `--model <model-id>` | `claude-sonnet-4-6` | Claude model |
| `--wait-max <seconds>` | `300` | Max wait per Copilot review |
| `--reflect` | — | Enable reflection agent after each fix iteration |
| `--reflect-model <model>` | same as `--model` | Model for reflection agent |
| `--reflect-main-branch <branch>` | `main` | Branch reflection rules are pushed to |
| `--dry-run` | — | Print startup state and exit |

### Example

```bash
./pr-review-claude-v2.sh 1 \
  --repo owner/repo \
  --cwd "/path/to/repo" \
  --model claude-sonnet-4-6

# With reflection (pushes rules to main after each fix cycle)
./pr-review-claude-v2.sh 1 \
  --repo owner/repo \
  --cwd "/path/to/repo" \
  --reflect
```

### How it works

Each iteration:

1. Check if Copilot review is pending — if not, request one via `gh pr edit --add-reviewer @copilot` (fallback: direct API)
2. Wait for Copilot to finish (up to `--wait-max` seconds, with stall recovery via dismiss+re-request)
3. Read the review — if `APPROVED` or 0 comments → exit success
4. `claude --print --model <model>` — fixes all comments, commits, pushes, requests new review, replies
5. Save last-known review ID → repeat

### Startup behaviour

On startup, checks current PR state and handles all cases:

| State | Action |
|-------|--------|
| No reviews yet | Requests first Copilot review |
| Review pending | Waits for it |
| Existing review with unresolved comments | Fixes them immediately |
| Existing review with 0 comments / already seen | Requests a fresh review |
| Already approved | Exits successfully |
| Merged / closed | Exits |

---

## v1

**`pr-review-claude.sh`** — Relies on `pr-review.sh` for GitHub operations. Max iterations configurable.

```bash
./pr-review-claude.sh <pr_number> [options]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--repo <owner/repo>` | auto-detect | GitHub repo |
| `--cwd <path>` | current dir | Local repo path |
| `--max-iter <n>` | `10` | Max fix iterations |
| `--wait-max <seconds>` | `300` | Max wait per Copilot review |

---

## opencode runner

**`pr-review-opencode.sh`** — Same flow as v2 but uses `opencode run` with GitHub Copilot as the model provider.

Configured to use the GitHub Copilot OAuth credential from `~/.local/share/opencode/auth.json`.

```bash
./pr-review-opencode.sh <pr_number> [options]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--repo <owner/repo>` | auto-detect | GitHub repo |
| `--cwd <path>` | current dir | Local repo path |
| `--model <provider/model>` | `github-copilot/claude-sonnet-4.6` | opencode model string |
| `--wait-max <seconds>` | `300` | Max wait per Copilot review |
| `--reflect` | — | Enable reflection agent after each fix iteration |
| `--reflect-model <model>` | same as `--model` | Model for reflection agent |
| `--reflect-main-branch <branch>` | `main` | Branch reflection rules are pushed to |
| `--dry-run` | — | Print startup state and exit |

### Example

```bash
./pr-review-opencode.sh 1 \
  --repo owner/repo \
  --cwd "/path/to/repo"

# With reflection (pushes rules to main after each fix cycle)
./pr-review-opencode.sh 1 \
  --repo owner/repo \
  --cwd "/path/to/repo" \
  --reflect

# Different model
./pr-review-opencode.sh 1 --model github-copilot/claude-sonnet-4.5 --cwd "/path/to/repo"
```

Available GitHub Copilot models: `github-copilot/claude-sonnet-4`, `github-copilot/claude-sonnet-4.5`, `github-copilot/claude-sonnet-4.6`

---

## Requirements

- `claude` CLI in PATH — for v1/v2 (`claude --version`)
- `opencode` CLI in PATH — for opencode runner (`opencode --version`)
- `gh` CLI v2.88+ authenticated (`gh --version`)
- `jq`

## Log files

| File | Written by |
|------|-----------|
| `~/.pr-review-claude.log` | `pr-review-claude-v2.sh`, `pr-review-claude.sh` |
| `~/.pr-review-opencode.log` | `pr-review-opencode.sh` |
| `~/.pr-review-reflect.log` | `pr-review-reflect.sh` |

## Terminal UI

When stdout is a TTY the runners display:

- Colored, severity-coded log lines
- Animated progress bar while waiting for Copilot to finish reviewing
- Bold section headers for each iteration
- An interactive arrow-key menu on success (merge, cleanup, open PR)

Set `NO_COLOR=1` or pass `--no-interactive` to disable interactive features (useful in CI or when piping output).

## `pr-review/.claude/settings.local.json`

Pre-configured tool permissions for Claude Code sessions running inside the loop.
