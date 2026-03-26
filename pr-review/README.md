# pr-review

CLI tool for managing GitHub Copilot PR review cycles. Designed for AI agents and automation, but works great for humans too.

## Usage

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
| `cycle` | Wait for review → return comments (agent loop) |
| `clear-state` | Delete the local state file for this PR (reset last-known) |
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

# Get comments
./pr-review.sh 42 comments

# Full agent loop cycle
./pr-review.sh 42 cycle --wait 300

# Reset state (forces next cycle to treat any review as new)
./pr-review.sh 42 clear-state

# Suppress emoji output (useful for CI / log parsers)
./pr-review.sh 42 cycle --wait 300 --no-color

# Cross-repo
./pr-review.sh 2 status --repo orsharon7/gsc-solar-monitor
```

All output is JSON. Logs go to stderr.

See header comments in `pr-review.sh` for full documentation.
