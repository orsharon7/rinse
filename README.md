```
 ██████╗ ██╗███╗  ██╗███████╗███████╗
 ██╔══██╗██║████╗ ██║██╔════╝██╔════╝
 ██████╔╝██║██╔██╗██║███████╗█████╗  
 ██╔══██╗██║██║╚████║╚════██║██╔══╝  
 ██║  ██║██║██║  ███║███████║███████╗
 ╚═╝  ╚═╝╚═╝╚═╝  ╚══╝╚══════╝╚══════╝
  AI PR review · proves it saved you time
```

[![Go version](https://img.shields.io/github/go-mod/go-version/orsharon7/rinse)](https://go.dev/)
[![License: BSL 1.1](LICENSE))
[![Latest release](https://img.shields.io/github/v/release/orsharon7/rinse)](https://github.com/orsharon7/rinse/releases)
[![PRs welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](https://github.com/orsharon7/rinse/pulls)

---

**Rinse drives GitHub Copilot through your PR review loop so you don't have to.**

Request review → wait → read comments → fix → repeat. Rinse handles every step, in a loop, until Copilot approves. One command. Walk away.

---

## ⚡ Install

```bash
curl -fsSL https://raw.githubusercontent.com/orsharon7/rinse/main/install.sh | bash
```

Or build from source (requires Go ≥ 1.24):

```bash
git clone https://github.com/orsharon7/rinse.git
cd rinse
make install
```

---

## 🚀 Quick start

```bash
# Interactive TUI wizard — recommended first run
rinse

# Check the Copilot review status of a PR
rinse status 42 --repo owner/repo

# Start the fix loop non-interactively (no TTY required)
rinse start 42 --repo owner/repo --cwd ~/dev/my-repo
```

The first-run wizard walks you through setup. After that, `rinse` remembers your preferences.

---

## 🔄 How it works

1. **Detects** the current PR state (no review? pending? unresolved comments?)
2. **Requests** a Copilot review if none is pending
3. **Waits** for Copilot to finish (animated progress, configurable timeout)
4. **Reads** every unresolved comment and hands them to your AI fix agent
5. **Pushes** the fixes and requests the next review
6. **Loops** until approved — then shows a merge menu

---

## 🛠 Options

```
rinse                                   # launch interactive TUI
rinse status [<pr>] [--repo <owner/repo>] [--json]
rinse start  <pr>  [options]            [--json]
rinse help
```

### `rinse status`

| Flag | Default | Description |
|------|---------|-------------|
| `<pr>` (positional) | auto-detect | PR number to check |
| `--repo <owner/repo>` | auto-detect | GitHub repo |
| `--json` | — | Output status as JSON |

### `rinse start`

| Flag | Default | Description |
|------|---------|-------------|
| `<pr>` (positional, required) | — | PR number to fix |
| `--repo <owner/repo>` | auto-detect | GitHub repo |
| `--cwd <path>` | current dir | Local repo path |
| `--model <model>` | `github-copilot/claude-sonnet-4.6` | AI model for fix agent |
| `--runner opencode\|claude` | `opencode` | Runner to use |
| `--reflect` | — | Enable reflection agent (improves rules after each cycle) |
| `--reflect-main-branch <br>` | `main` | Target branch for reflection commits |
| `--auto-merge` | — | Auto-merge when Copilot approves |
| `--json` | — | Emit a JSON result after the runner exits |

### Available Copilot models

| Model | Flag value |
|-------|-----------|
| Claude Sonnet 4.6 | `github-copilot/claude-sonnet-4.6` |
| Claude Sonnet 4.5 | `github-copilot/claude-sonnet-4.5` |
| Claude Sonnet 4   | `github-copilot/claude-sonnet-4` |

---

## 🪞 Reflection agent

The `--reflect` flag runs a reflection pass in parallel with each fix cycle.

It reads Copilot's comments, extracts generalizable coding rules, and permanently updates `AGENTS.md` + `CLAUDE.md` in your repo — **pushed directly to `main` via a git worktree, never polluting the PR branch**.

Every future cycle loads those rules automatically, so the AI makes fewer mistakes and Copilot leaves fewer comments. The loop gets faster over time.

```bash
rinse start 42 --repo owner/repo --cwd ~/dev/my-repo --reflect
```

---

## 📋 Requirements

| Dependency | Notes |
|------------|-------|
| `opencode` CLI | Authentication via GitHub Copilot OAuth |
| `gh` CLI ≥ v2.88 | `gh --version` to check |
| `jq` | JSON processing |
| `git` | Required for `--reflect` worktree |

---

## 🤝 Contributing

1. Fork the repo and create a branch: `git checkout -b feat/my-change`
2. Make your changes — keep code POSIX-compatible where possible
3. Test against a real PR: `rinse start <number> --repo owner/repo`
4. Open a PR — Copilot reviews it automatically
5. Use `--reflect` to update coding rules for future sessions

**Dev tips:**

- TUI source is in `internal/tui/` (Go + Charm Bubble Tea)
- Run `make` to build, `make install` to install locally
- Logs: `~/.pr-review/logs/<owner_repo>-pr-<n>.log` (and related reflect logs in `~/.pr-review/logs/`)

---

## 📄 License

MIT — see [LICENSE](LICENSE)
