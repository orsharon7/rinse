# pr-review

CLI tool for managing GitHub Copilot PR review cycles. Designed for AI agents and automation, but works great for humans too.

## Core tool

```bash
./pr-review.sh <pr_number> <subcommand> [options]
```

### Subcommands

| Command | Description |
|---------|-------------|
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

### Global flags

| Flag | Description |
|------|-------------|
| `--repo <owner/repo>` | Override repo detection |
| `--last-known <review_id>` | Skip if latest review matches this ID |
| `--no-color` | Suppress emoji in stderr progress messages |

### Examples

```bash
# Check status
./pr-review.sh 42 status

# Wait for review (blocks up to 300s)
./pr-review.sh 42 status --wait 300

# Get comments from latest review
./pr-review.sh 42 comments

# Full agent loop cycle (blocks, auto-requests if needed)
./pr-review.sh 42 cycle --wait 300

# Reset state (forces next cycle to treat any review as new)
./pr-review.sh 42 clear-state

# Cross-repo
./pr-review.sh 2 status --repo orsharon7/gsc-solar-monitor
```

All output is JSON. Logs go to stderr.

---

## Runners

The core tool exposes primitives. Runners wrap it in an automated loop.

| Runner | File | Agent | Mode |
|--------|------|-------|------|
| [Claude Code](./claude/) | `claude/pr-review-claude.sh` | `claude` CLI | Foreground loop |
| Daemon | `pr-review-daemon.sh` | OpenClaw | Background daemon |
| Cron | `pr-review-cron.sh` | OpenClaw | Cron job |

### Claude Code runner (recommended)

Drives `claude --print` in a loop until Copilot approves or returns 0 comments.

```bash
./claude/pr-review-claude.sh 42 \
  --repo orsharon7/gsc-solar-monitor \
  --cwd ~/dev/gsc-solar-monitor
```

See [`claude/README.md`](./claude/README.md) for full docs.

### Daemon runner

Persistent background process. Polls watched PRs every N seconds and fires OpenClaw system events.

```bash
# Start
./pr-review-daemon.sh

# Stop
./pr-review-daemon.sh --stop

# Status
./pr-review-daemon.sh --status
```

### Cron runner

Lightweight poller for cron. Fires OpenClaw events + Telegram notifications.

```
*/2 * * * * /path/to/pr-review-cron.sh
```
