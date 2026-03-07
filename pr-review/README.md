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

# Cross-repo
./pr-review.sh 2 status --repo orsharon7/gsc-solar-monitor
```

All output is JSON. Logs go to stderr.

See header comments in `pr-review.sh` for full documentation.
