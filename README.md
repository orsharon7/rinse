# RINSE

Automated GitHub Copilot PR review lifecycle manager. Drives an AI coding
agent in a loop to fix Copilot review comments until the PR is approved.

---

## Install

```bash
git clone https://github.com/luliluli/rinse  # or your fork
cd rinse
bash install.sh
```

This installs the `rinse` binary to `~/.local/bin/rinse` and copies the
runner scripts alongside it. Make sure `~/.local/bin` is in your `PATH`.

---

## Quick start

```bash
rinse
```

The interactive TUI wizard walks you through every option — repository, PR
number, runner, model, reflection — shows a confirmation summary, then
drives the review loop to completion.

You can also pass the PR number directly:

```bash
rinse 42
rinse 42 --repo owner/repo --cwd ~/dev/my-repo
```

---

## How it works

1. `rinse` opens a wizard to configure the run
2. Requests a GitHub Copilot review on your PR
3. When Copilot comments, the AI fix agent reads and resolves each comment
4. Pushes the fix, requests another review
5. Repeats until the PR is approved (or the iteration limit is reached)
6. On success: merge menu with optional branch cleanup

---

## Options

| Flag | Default | Description |
|------|---------|-------------|
| `--repo <owner/repo>` | auto-detect | GitHub repo |
| `--cwd <path>` | current dir | Local repo path |
| `--model <provider/model>` | `github-copilot/claude-sonnet-4.6` | Model string |
| `--wait-max <seconds>` | `300` | Max wait per Copilot review |
| `--reflect` | — | Enable reflection agent after each fix iteration |
| `--reflect-model <model>` | same as `--model` | Model for reflection agent |
| `--reflect-main-branch <branch>` | `main` | Branch reflection rules are pushed to |
| `--no-interactive` | — | Disable terminal UI (CI / piped output) |
| `--dry-run` | — | Print startup state and exit |

---

## Reflection agent

Pass `--reflect` to enable the reflection agent. After each fix cycle it
reads Copilot's comments, extracts generalizable rules, and permanently
updates `AGENTS.md` + `CLAUDE.md` in your repo. Rules are pushed directly
to `main` (via `git worktree`) so they never appear in the PR diff and are
loaded by AI agents on every future session — producing fewer comments over
time.

```bash
rinse 42 --repo owner/repo --cwd ~/dev/my-repo --reflect
```

---

## Requirements

| Dependency | Purpose | Check |
|------------|---------|-------|
| `opencode` CLI | default runner | `opencode --version` |
| `gh` CLI v2.88+ | all runners | `gh --version` |
| `jq` | all runners | `jq --version` |
| `git` | reflection agent | `git --version` |

The `rinse` binary uses `opencode` as its default runner (GitHub Copilot
OAuth — no separate API key required). A `claude` CLI runner is also
available; see [Appendix: Shell runners](#appendix-shell-runners) below.

---

## Contributing

1. **Fork** the repo and create a feature branch: `git checkout -b feat/my-change`
2. Make your changes. The TUI source is in `tui/` (Go ≥ 1.24); run `make` inside `tui/` to build locally.
3. **Test** with `rinse` or a direct runner invocation.
4. Open a **Pull Request** — Copilot will review it automatically.
5. Address Copilot comments (use `--reflect` to auto-update `AGENTS.md` / `CLAUDE.md`).

---

## License

RINSE is source-available under the [Business Source License 1.1](LICENSE). Free for personal and dev use.

---

---

## Appendix: Shell runners

> **These are the underlying scripts that `rinse` drives internally.**
> Most users do not need to call them directly. Use `rinse` instead.

### Script tree

```
pr-review/
├── pr-review-launch.sh     # Interactive TUI launcher (superseded by rinse binary)
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
| `--no-interactive` | — | Disable terminal UI |
| `--dry-run` | — | Print startup state and exit |

Available GitHub Copilot models: `github-copilot/claude-sonnet-4`,
`github-copilot/claude-sonnet-4.5`, `github-copilot/claude-sonnet-4.6`

```bash
./pr-review-opencode.sh 42 --repo owner/repo --cwd ~/dev/my-repo
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

```bash
./pr-review-claude-v2.sh 42 --repo owner/repo --cwd ~/dev/my-repo --model claude-sonnet-4-6
```

---

### `pr-review-claude.sh` (v1 — legacy)

Relies on `pr-review.sh` for GitHub operations. Use v2 for new setups.

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

### Reflection agent (`pr-review-reflect.sh`)

Runs in parallel with each fix cycle. Reads Copilot review comments, extracts
generalizable coding rules, and permanently updates `AGENTS.md` + `CLAUDE.md`
in the project repo. Rules are pushed to `main` via `git worktree` so the
reflection commit never appears in the PR diff.

```bash
# Standalone
./pr-review-reflect.sh 42 --repo owner/repo --cwd ~/dev/my-repo --review-id <id>
```

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

### Core tool (`pr-review.sh`)

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

### Background runners

**`pr-review-daemon.sh`** — persistent background process. Polls watched PRs
and fires a configurable callback when a review event occurs.

```bash
./pr-review-daemon.sh          # Start
./pr-review-daemon.sh --stop   # Stop
./pr-review-daemon.sh --status # Status
```

**`pr-review-cron.sh`** — lightweight cron-compatible poller.

```
*/2 * * * * /path/to/pr-review/pr-review-cron.sh
```

---

### Terminal UI (`pr-review-ui.sh`)

Sourced automatically by the runners. Do not run it directly.

When stdout is a TTY it provides colored log lines, an animated progress bar
while waiting for Copilot, and an arrow-key merge menu on success.

Disable with `NO_COLOR=1` or `--no-interactive`.

---

### Log files

| File | Written by |
|------|-----------|
| `~/.pr-review-claude.log` | `pr-review-claude-v2.sh`, `pr-review-claude.sh` |
| `~/.pr-review-opencode.log` | `pr-review-opencode.sh` |
| `~/.pr-review-reflect.log` | `pr-review-reflect.sh` |

---

### `.claude/settings.local.json`

Not committed — create at `pr-review/.claude/settings.local.json` to
pre-authorize tool permissions so Claude Code sessions don't prompt on every
shell command.

```json
{
  "permissions": {
    "allow": ["Bash", "Read", "Write", "Edit"]
  }
}
```
