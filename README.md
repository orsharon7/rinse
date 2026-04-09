# tools

CLI tools for GitHub automation and AI-assisted development workflows.

## Tools

| Tool | Description |
|------|-------------|
| [pr-review](./pr-review/) | GitHub Copilot PR review lifecycle manager |

---

## pr-review

Manages the full Copilot code review cycle — request, wait, fix, repeat.

### Core tool

```bash
pr-review/pr-review.sh <pr_number> <subcommand>
pr-review/pr-review.sh poll-all   # no PR number — polls all watched PRs
```

Subcommands: `status`, `comments`, `reply`, `reply-all`, `request`, `push`, `cycle`, `clear-state`, `watch`, `unwatch`, `poll-all`

Stdout is JSON; logs/progress go to stderr. See [pr-review/README.md](./pr-review/README.md) for full docs.

### Runners

Runners wrap the core tool in an automated fix loop. Available runners:

| Runner | Script | Agent | Model | Mode/Iterations |
|--------|--------|-------|-------|-----------------|
| **opencode** | `pr-review/claude/pr-review-opencode.sh` | opencode | `github-copilot/claude-sonnet-4.6` | unlimited |
| **Claude v2** | `pr-review/claude/pr-review-claude-v2.sh` | claude CLI | configurable | unlimited |
| **Claude v1** | `pr-review/claude/pr-review-claude.sh` | claude CLI | N/A (Claude default) | 10 (default) |
| Daemon | `pr-review/pr-review-daemon.sh` | OpenClaw | — | background |
| Cron | `pr-review/pr-review-cron.sh` | OpenClaw | — | cron |

#### Recommended: opencode runner

```bash
pr-review/claude/pr-review-opencode.sh <pr_number> \
  --repo <owner/repo> \
  --cwd /path/to/local/repo
```

#### Claude v2 runner

```bash
pr-review/claude/pr-review-claude-v2.sh <pr_number> \
  --repo <owner/repo> \
  --cwd /path/to/local/repo \
  --model claude-sonnet-4-6
```

See [pr-review/claude/README.md](./pr-review/claude/README.md) for all runner options.

---

## Requirements

- `gh` CLI v2.88+ authenticated
- `claude` CLI (for Claude runners)
- `opencode` v1.3+ with GitHub Copilot OAuth (for opencode runner)
- `jq`

## License

MIT
