# RINSE

**Stop manually shepherding PRs through Copilot review. RINSE does it for you.**

RINSE watches your pull request, waits for GitHub Copilot to finish reviewing, fixes every comment with an AI coding agent, and re-requests review — automatically, in a loop — until your PR is approved. When it's done, it tells you exactly how much time it saved.

---

## Install

```bash
go install github.com/orsharon7/rinse@latest
```

> Homebrew tap coming soon: `brew install rinse`

---

## Usage

```bash
rinse <pr_number> --repo owner/repo --cwd /path/to/local/repo
```

RINSE launches an interactive TUI, picks up the PR, and runs the fix-review loop until approval.

**Options**

| Flag | Default | Description |
|------|---------|-------------|
| `--repo <owner/repo>` | auto-detect | GitHub repository |
| `--cwd <path>` | current dir | Local clone path |
| `--model <model>` | `github-copilot/claude-sonnet-4.6` | AI model |
| `--reflect` | — | Learn from each review cycle and improve future runs |
| `--no-interactive` | — | Plain output, no TUI (useful in CI) |

**Example**

```bash
rinse 42 --repo myorg/myapp --cwd ~/dev/myapp
```

---

## How it works

1. **Watch** — RINSE requests a GitHub Copilot review on your PR and waits for it to finish.
2. **Fix** — An AI coding agent reads every unresolved comment and applies the fixes directly to your branch.
3. **Repeat** — RINSE commits, pushes, and re-requests review. It loops until Copilot approves — then surfaces a summary of every cycle and the time you saved.

---

## Reflection mode

Pass `--reflect` to enable RINSE's learning loop. After each fix cycle, a reflection agent reads the Copilot comments, extracts generalizable coding rules, and writes them into your project's `AGENTS.md` / `CLAUDE.md`. Rules land on `main` (not your PR branch), so every future session starts smarter and produces fewer review comments.

---

## Requirements

| Dependency | Purpose |
|------------|---------|
| `opencode` or `claude` CLI | AI coding agent |
| `gh` CLI v2.88+ | GitHub operations |
| `jq` | JSON processing |
| `git` | Reflection worktree |

---

## RINSE Pro

Want multi-repo dashboards, Slack notifications, and priority support?

**[Join the RINSE Pro waitlist →](https://rinse.dev/pro)**

---

## License

MIT
