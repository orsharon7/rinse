# pr-review / claude

Automated Copilot PR review loop driven by **Claude Code CLI** (`claude`).

Two versions — both fix Copilot comments in a loop until the PR is approved or returns 0 comments.

---

## Reflection agent

**`pr-review-reflect.sh`** — Runs in parallel with each fix cycle. Reads Copilot review comments, extracts generalizable coding rules, and permanently updates `AGENTS.md` + `CLAUDE.md` in the project repo. Both files are loaded automatically by AI coding agents on every future session, so each cycle produces fewer comments.

Enable it with `--reflect` on any runner:

```bash
./pr-review-opencode.sh 1 --repo orsharon7/repo --cwd /path/to/repo --reflect
```

Or run standalone:

```bash
./pr-review-reflect.sh 1 --repo orsharon7/repo --cwd /path/to/repo --review-id 4077186198
```

**How it works:**
1. Runs in background while the fix agent is working
2. Analyzes Copilot comments → identifies patterns → writes rules
3. Updates the `<!-- BEGIN:COPILOT-RULES --> ... <!-- END:COPILOT-RULES -->` section in both `AGENTS.md` and `CLAUDE.md`  
4. Commits and pushes the updated rules to the repo
5. Next fix iteration: both agents load the updated rules automatically → fewer issues → fewer review cycles

| Flag | Default | Description |
|------|---------|-------------|
| `--model <provider/model>` | same as runner | AI model for reflection |
| `--agent <opencode\|claude>` | `opencode` | Which CLI to use |
| `--review-id <id>` | latest Copilot review | Specific review to analyse |
| `--dry-run` | — | Print prompt, don't run |

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
| `--dry-run` | — | Print startup state and exit |

### Example

```bash
./pr-review-claude-v2.sh 1 \
  --repo orsharon7/stu-msft-agent-platform \
  --cwd "/path/to/repo" \
  --model claude-sonnet-4-6
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
| `--dry-run` | — | Print startup state and exit |

### Example

```bash
./pr-review-opencode.sh 1 \
  --repo orsharon7/stu-msft-agent-platform \
  --cwd "/path/to/repo"

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

## Log file

All output streams to `~/.pr-review-claude.log`.

## `.claude/settings.local.json`

Pre-configured tool permissions for Claude Code sessions running inside the loop.
