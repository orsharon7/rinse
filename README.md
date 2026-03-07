# pr-review

CLI tool for managing GitHub Copilot PR review cycles. Designed for AI agents and automation, but works great for humans too.

## What it does

Automates the full Copilot code review lifecycle:
- Check review status (pending / new / approved)
- Fetch review comments
- Reply to comments
- Request reviews
- Wait for reviews to land (with stall detection + auto-retry)
- Full cycle mode for agent loops

All output is JSON (stdout). Progress/logs go to stderr.

## Requirements

- [`gh`](https://cli.github.com/) (GitHub CLI, authenticated)
- `jq`
- Bash 4+

## Installation

```bash
# Clone
git clone git@github.com:orsharon7/pr-review.git
cd pr-review

# Make available globally
chmod +x pr-review.sh
ln -sf "$(pwd)/pr-review.sh" /usr/local/bin/pr-review
```

## Usage

```bash
# Check review status
pr-review.sh <pr_number> status

# Check with wait (blocks until Copilot finishes, up to 300s)
pr-review.sh <pr_number> status --wait [<seconds>]

# Get comments from latest review
pr-review.sh <pr_number> comments

# Get comments from a specific review
pr-review.sh <pr_number> comments --review-id <id>

# Reply to a comment
pr-review.sh <pr_number> reply <comment_id> "Fixed in abc123"

# Batch reply (JSON from stdin)
echo '[{"comment_id": 123, "body": "Fixed"}]' | pr-review.sh <pr_number> reply-all

# Request Copilot review (skips if already pending)
pr-review.sh <pr_number> request

# Commit, push, and request review
pr-review.sh <pr_number> push -m "fix: address review comments"

# Full cycle: wait for review → return comments
pr-review.sh <pr_number> cycle --wait 300
```

### Global flags

```bash
--repo <owner/repo>        # Override repo detection (default: current git repo)
--last-known <review_id>   # Skip if latest review matches this ID
```

## Status values

| Status | Meaning |
|--------|---------|
| `pending` | Copilot is actively reviewing |
| `new_review` | New review with comments |
| `approved` | Copilot approved the PR |
| `no_change` | Latest review matches `--last-known` |
| `no_reviews` | No Copilot reviews exist yet |
| `merged` | PR already merged |
| `closed` | PR closed without merge |
| `error` | API error or PR not found |

## Agent loop example

```bash
# 1. Fix code based on comments
# 2. Push and request review
./pr-review.sh 42 push -m "fix: address review"
# 3. Wait for review and get comments
./pr-review.sh 42 cycle --wait 300
# 4. Parse JSON output, fix comments, repeat
```

## How it works

- Uses `gh api` to interact with GitHub's REST API
- Tracks last-seen review ID in `/tmp/pr-review-state/` for dedup
- In `--wait` mode, polls every 15s with stall detection
- If Copilot appears stalled (>300s), automatically dismisses and re-requests
- `reply-all` saves last review ID to prevent re-processing

## License

MIT
