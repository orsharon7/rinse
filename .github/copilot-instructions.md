# GSC Capital Tools — Copilot Code Review Instructions

This repo contains shell scripts for automating Copilot PR review loops.

## Focus On

- **Bash safety**: flag `set -euo pipefail` issues, unquoted variables (especially paths with spaces), and uninitialized variable access
- **JSON handling**: all `jq` output used in conditionals must have fallbacks (never assume non-empty)
- **Exit codes**: functions that can fail must propagate errors; callers must check `$?`
- **No hardcoded paths**: never hardcode `/home/<user>/` — always use `$HOME` or env-var overrides
- **Idempotency**: operations that mutate state (watch file, GitHub API calls) should be safe to retry

## Do Not Flag

- Shell style preferences (tabs vs spaces, brace style)
- `set -e` interactions in subshells called via `$(...)` — these are intentional
- GitHub API `--input -` pattern with heredoc `<<< '{...}'` — this is correct usage

## Context

Scripts interact with the GitHub REST API via `gh api` and `gh pr edit`.
The watch file is `~/.pr-review-watches.json` (JSON array of PR entries).
State files live in `/tmp/pr-review-state/`.
