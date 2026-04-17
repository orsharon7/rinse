# tools

CLI automation tools for GitHub Copilot PR review workflows.

## pr-review

Automated GitHub Copilot PR review lifecycle manager. Drives an AI coding
agent in a loop to fix Copilot review comments until the PR is approved.

```
pr-review/
├── pr-review-launch.sh     # Interactive TUI launcher — start here
├── pr-review.sh            # Core primitives (JSON API wrapper around gh + GitHub REST)
├── pr-review-daemon.sh     # Background daemon — polls watched PRs continuously
├── pr-review-cron.sh       # Cron-compatible poller
├── pr-review-ui.sh         # Shared terminal UI library (sourced, not run directly)
├── pr-review-claude.sh     # v1 runner — uses claude CLI + pr-review.sh primitives
├── pr-review-claude-v2.sh  # v2 runner — standalone, recommended for Claude Code
├── pr-review-opencode.sh   # Recommended runner for opencode + GitHub Copilot
├── pr-review-reflect.sh    # Reflection agent — extracts rules, pushes to main
└── .claude/
    └── settings.local.json # (user-created) pre-authorized tool permissions for Claude Code
```

---

## Quick start

### Interactive launcher (recommended)

```bash
cd pr-review
./pr-review-launch.sh
```

A step-by-step wizard walks you through every option — repository, PR number,
runner, model, reflection, wait timeout — shows a confirmation summary, then
hands off to the selected runner. All log output flows in the same terminal.

You can also pre-fill the PR number and skip straight to the wizard:

```bash
./pr-review-launch.sh 42
./pr-review-launch.sh 42 --repo owner/repo --cwd ~/dev/my-repo
```

### Direct runner invocation

```bash
# opencode (GitHub Copilot — no API key required)
./pr-review-opencode.sh <pr_number> --repo owner/repo --cwd /path/to/local/repo

# Claude Code (direct Anthropic API key)
./pr-review-claude-v2.sh <pr_number> --repo owner/repo --cwd /path/to/local/repo

# With reflection agent (improves rules after each cycle)
./pr-review-opencode.sh <pr_number> --repo owner/repo --cwd /path/to/local/repo --reflect
```

---

## Which runner should I use?

| Runner | CLI | When to use |
|--------|-----|-------------|
| `pr-review-opencode.sh` | `opencode` | You have opencode authenticated with GitHub Copilot — no API key needed |
| `pr-review-claude-v2.sh` | `claude` | You have Claude Code CLI with an Anthropic API key — recommended v2 |
| `pr-review-claude.sh` | `claude` | Legacy v1 — use v2 instead |

---

## Runners

### `pr-review-opencode.sh`

Drives `opencode run` in a loop using the GitHub Copilot OAuth credential
from `~/.local/share/opencode/auth.json`.

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
| `--no-interactive` | — | Disable terminal UI (useful in CI or when piping output) |
| `--dry-run` | — | Print startup state and exit |

Available GitHub Copilot models: `github-copilot/claude-sonnet-4`,
`github-copilot/claude-sonnet-4.5`, `github-copilot/claude-sonnet-4.6`

**Example:**

```bash
./pr-review-opencode.sh 42 \
  --repo owner/repo \
  --cwd ~/dev/my-repo

# Different model
./pr-review-opencode.sh 42 --model github-copilot/claude-sonnet-4.5 --cwd ~/dev/my-repo
```

---

### `pr-review-claude-v2.sh`

Standalone runner (does not depend on `pr-review.sh`). Unlimited iterations,
model-agnostic. Uses `gh pr edit --add-reviewer @copilot` (requires gh v2.88+).

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
| `--no-interactive` | — | Disable terminal UI |
| `--dry-run` | — | Print startup state and exit |

**Startup behaviour:**

| State | Action |
|-------|--------|
| No reviews yet | Requests first Copilot review |
| Review pending | Waits for it |
| Existing review with unresolved comments | Fixes them immediately |
| Existing review with 0 comments / already seen | Requests a fresh review |
| Already approved | Exits successfully |
| Merged / closed | Exits |

**Example:**

```bash
./pr-review-claude-v2.sh 42 \
  --repo owner/repo \
  --cwd ~/dev/my-repo \
  --model claude-sonnet-4-6
```

---

### `pr-review-claude.sh` (v1 — legacy)

Relies on `pr-review.sh` for GitHub operations. Max iterations configurable.
Use v2 for new setups.

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

## Reflection agent (`pr-review-reflect.sh`)

Runs in parallel with each fix cycle. Reads Copilot review comments, extracts
generalizable coding rules, and permanently updates `AGENTS.md` + `CLAUDE.md`
in the project repo. Both files are loaded automatically by AI coding agents on
every future session, so each cycle produces fewer comments.

**Rules are pushed to `main`, not the PR branch** — using a `git worktree` so
the reflection commit never appears in the PR diff. This prevents Copilot from
re-reviewing the rule files and avoids an infinite review loop.

Enable with `--reflect` on any runner:

```bash
./pr-review-opencode.sh 42 --repo owner/repo --cwd ~/dev/my-repo --reflect
```

Run standalone:

```bash
./pr-review-reflect.sh 42 --repo owner/repo --cwd ~/dev/my-repo --review-id <id>
```

**How it works:**

1. Runs in background while the fix agent works
2. Creates a temporary `git worktree` checked out on `main`
3. Analyzes Copilot comments → identifies patterns → writes rules into the worktree (not the PR branch)
4. Commits and pushes updated `AGENTS.md` / `CLAUDE.md` directly to `main`
5. Cleans up the worktree
6. Next iteration: fix agent loads updated rules from `main` → fewer issues → fewer cycles

| Flag | Default | Description |
|------|---------|-------------|
| `--repo <owner/repo>` | required | GitHub repo |
| `--cwd <path>` | required | Local repo path |
| `--main-branch <branch>` | `main` | Branch to push rules to |
| `--review-id <id>` | latest Copilot review | Specific review to analyse |
| `--model <provider/model>` | `github-copilot/claude-sonnet-4.6` | AI model for reflection |
| `--agent <opencode\|claude>` | `opencode` | Which CLI to use |
| `--dry-run` | — | Print prompt and worktree path, don't run |

---

## Core tool (`pr-review.sh`)

Primitives used internally by the v1 runner and background polling scripts.
All output is JSON; logs go to stderr.

```bash
./pr-review.sh <pr_number> <subcommand> [options]
```

| Subcommand | Description |
|------------|-------------|
| `status` | Check review state (pending / new / approved) |
| `comments` | List unresolved Copilot comments |
| `reply` | Reply to a specific comment |
| `reply-all` | Batch reply from JSON stdin |
| `request` | Request Copilot review |
| `push` | Commit + push + request review |
| `cycle` | Wait for review → return comments (agent loop primitive) |
| `clear-state` | Reset last-known review ID for this PR |
| `watch` | Add a PR to the watch list (async polling) |
| `unwatch` | Remove a PR from the watch list |
| `poll-all` | Check all watched PRs, output results, auto-retry errors |

**Global flags:**

| Flag | Description |
|------|-------------|
| `--repo <owner/repo>` | Override repo detection |
| `--last-known <review_id>` | Skip if latest review matches this ID |
| `--no-color` | Suppress emoji in stderr progress messages |

---

## Terminal UI (`pr-review-ui.sh`)

Sourced automatically by `pr-review-opencode.sh` and `pr-review-claude-v2.sh`.
Do not run it directly.

When stdout is a TTY it provides:

- Colored, severity-coded log lines
- Animated progress bar while waiting for Copilot to finish reviewing
- Bold section headers per iteration
- Arrow-key merge menu on success: merge, branch cleanup, open PR in browser

Disable with `NO_COLOR=1` or `--no-interactive` (useful in CI or when piping output).

---

## Background runners

### `pr-review-daemon.sh`

Persistent background process. Polls watched PRs and fires a configurable
callback when a review event occurs.

```bash
./pr-review-daemon.sh          # Start
./pr-review-daemon.sh --stop   # Stop
./pr-review-daemon.sh --status # Status
```

### `pr-review-cron.sh`

Lightweight cron-compatible poller. Add to crontab:

```
*/2 * * * * /path/to/pr-review/pr-review-cron.sh
```

---

## Requirements

| Dependency | Used by | Check |
|------------|---------|-------|
| `opencode` CLI | `pr-review-opencode.sh` | `opencode --version` |
| `claude` CLI | `pr-review-claude-v2.sh`, `pr-review-claude.sh` | `claude --version` |
| `gh` CLI v2.88+ | all runners | `gh --version` |
| `jq` | all runners | `jq --version` |
| `git` | `pr-review-reflect.sh` (worktree) | `git --version` |

---

## Log files

| File | Written by |
|------|-----------|
| `~/.pr-review/logs/<owner_repo>-pr-<n>.log` | `pr-review-claude-v2.sh`, `pr-review-opencode.sh` |
| `~/.pr-review/logs/<owner_repo>-pr-<n>-reflect.log` | `pr-review-reflect.sh` |
| `~/.pr-review-claude.log` | `pr-review-claude.sh` (v1 legacy runner) |

---

## `.claude/settings.local.json`

Not committed to this repo — create it yourself at `pr-review/.claude/settings.local.json`.
Pre-configure tool permissions so Claude Code sessions running inside the review loop
don't prompt for approval on every shell command. Claude Code picks it up automatically
when invoked from the `pr-review/` directory.

Example:

```json
{
  "permissions": {
    "allow": ["Bash", "Read", "Write", "Edit"]
  }
}
```

---

## Contributing

Contributions are welcome!

1. **Fork** the repo and create a feature branch: `git checkout -b feat/my-change`
2. Make your changes. Keep scripts POSIX-compatible where possible; bash-specific features are fine where already used.
3. **Test** against a real PR with `./pr-review-launch.sh` or direct runner invocation.
4. Open a **Pull Request** — Copilot will review it automatically via `pr-review-opencode.sh`.
5. Address any Copilot comments (the `--reflect` flag will update `AGENTS.md` / `CLAUDE.md` automatically for future sessions).

### Dev tips

- All scripts are in `pr-review/`. The TUI source is in `tui/` (Go ≥ 1.24).
- Run `make` inside `tui/` to build the binary locally.
- Run `rinse init` in a repo directory to scaffold a per-repo `.rinse.json` config (engine, model, reflection, auto-merge). The file can be committed as a reference for your team, though runner and TUI behaviour is not yet driven by it automatically.
- Log output goes to `~/.pr-review/logs/<owner_repo>-pr-<n>.log` — check there first when debugging.
- Use `--dry-run` on any runner to inspect startup state without running the model or performing mutating GitHub actions; runners may still make read-only API calls during startup.
- `AGENTS.md` and `CLAUDE.md` at the repo root carry coding rules that AI agents load automatically. If you notice a pattern worth encoding, add a rule there.

---

## License

MIT
