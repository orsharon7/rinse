# RINSE

> **lather · rinse · repeat** — AI-powered PR review that fixes your code automatically.

RINSE drives an AI agent in a loop to resolve GitHub Copilot review comments until your PR is approved. You pick the PR; RINSE handles the rest.

```
$ rinse
```

That's it. RINSE opens an interactive picker, you select your PR, and the AI gets to work — fetching comments, applying fixes, requesting re-review, repeat — until the PR is clean.

---

## Why RINSE?

The average PR review cycle looks like this:

1. Open PR → Copilot leaves 8 comments
2. You fix them → Copilot leaves 4 more
3. You fix those → PR approved
4. ~45 minutes of your morning gone

RINSE does steps 2–3 for you, automatically. Our users report **saving 2–9 hours per developer per month** on review iteration alone.

After each cycle, RINSE records what patterns it fixed. Run `rinse stats` to see how much time it has saved you — and what your codebase tends to get wrong.

---

## Install

```bash
brew install rinse
```

**Requirements:** `gh` CLI (v2.88+), and one of:
- `opencode` — for GitHub Copilot (no API key needed)
- `claude` — for Claude Code (requires Anthropic API key)

---

## Quick start

### Interactive mode (recommended)

```bash
cd your-repo
rinse
```

RINSE auto-detects your repository, lists open PRs, and pre-selects the branch you're on. Press **Enter** to launch.

### View your stats

```bash
rinse stats
```

```
  RINSE Stats (last 30 days)
  PRs reviewed:     23
  Comments fixed:   187
  Avg iterations:   2.1
  Est. time saved:  ~9.4 hours

  Top patterns:
    1. Missing error handling  (41x)
    2. Unused imports          (28x)
```

---

## How it works

```
rinse
  │
  ▼
Pick a PR ──────────────────────────────────────────────────┐
  │                                                          │
  ▼                                                          │
Request Copilot review                                       │
  │                                                          │
  ▼                                                          │
Wait for review results                                      │
  │                                                          │
  ▼                                                          │
AI agent reads comments → applies fixes → pushes changes     │
  │                                                          │
  ▼                                                          │
Request re-review ──── comments remain ─────────────────────┘
  │
  ▼  approved
Done ✓  (optionally: auto-merge)
```

**Reflection** (optional): After each cycle, a second AI agent extracts generalizable coding rules from the review comments and updates `AGENTS.md` / `CLAUDE.md` in your repo. Future cycles start with those rules already loaded — so each PR gets fewer comments than the last.

---

## TUI keyboard shortcuts

| Key | Action |
|-----|--------|
| `↑` `↓` or `j` `k` | Navigate PR list |
| `Enter` | Launch review cycle on selected PR |
| `g` / `G` | Jump to top / bottom of list |
| `#` | Enter a PR number manually |
| `s` | Open settings |
| `r` | Refresh PR list from GitHub |
| `?` | Toggle keyboard shortcuts help |
| `q` or `Ctrl+C` | Quit |

---

## Settings

Press `s` in the PR picker to configure:

| Setting | Options | Default |
|---------|---------|---------|
| **runner** | `opencode` (GitHub Copilot, no key) · `claude` (Claude Code) | `opencode` |
| **model** | Any model string your runner supports | runner default |
| **reflect** | on · off | off |
| **reflect branch** | branch where coding rules are pushed | `main` |
| **auto-merge** | on · off | off |

Settings are saved per-repo under `~/.rinse/`.

---

## Commands

```
rinse              Launch the interactive PR picker
rinse stats        Show session history and time-saved metrics
rinse --version    Print the installed version
rinse --help       Show this help
```

---

## Environment variables

| Variable | Description |
|----------|-------------|
| `RINSE_SCRIPT_DIR` | Override the directory where runner scripts are found |
| `PR_REVIEW_SCRIPT_DIR` | Fallback script directory (legacy alias for `RINSE_SCRIPT_DIR`) |
| `RINSE_WEBHOOK_URL` | When set, POST a JSON payload to this URL after each completed cycle |

---

## Session data

RINSE stores one JSON file per run in `~/.rinse/sessions/`. Each file records:

- Repository and PR number
- Runner and model used
- Number of comments fixed and iterations taken
- Whether the PR was approved
- Common fix patterns (powers `rinse stats`)

No data leaves your machine. `rinse stats` reads these files locally.

---

## Requirements

| Tool | Used by | Check |
|------|---------|-------|
| `gh` v2.88+ | all runners | `gh --version` |
| `opencode` | opencode runner | `opencode --version` |
| `claude` | claude runner | `claude --version` |
| `jq` | shell scripts | `jq --version` |
| `git` | reflection agent | `git --version` |

---

## RINSE Pro (coming soon)

The free CLI gives you session stats locally. **RINSE Pro** brings:

- **Team dashboard** — per-dev stats, repo trends, team benchmarks
- **Slack integration** — weekly digest to your team channel
- **Trend alerts** — get notified when a new error pattern spikes

Join the waitlist: [rinse.sh](https://rinse.sh)

---

## License

MIT
