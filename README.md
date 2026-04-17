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
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)
[![Latest release](https://img.shields.io/github/v/release/orsharon7/rinse)](https://github.com/orsharon7/rinse/releases)
[![PRs welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](https://github.com/orsharon7/rinse/pulls)

---

**Rinse drives GitHub Copilot through your PR review loop so you don't have to.**

Request review → wait → read comments → fix → repeat. Rinse handles every step, in a loop, until Copilot approves. One command. Walk away.

---

## ⚡ Install

Clone the repo and run the installer (requires Go ≥ 1.24 or a pre-built binary in `dist/`):

```bash
git clone https://github.com/orsharon7/rinse.git
cd rinse
bash install.sh
```

---

## 🚀 Quick start

```bash
# Interactive TUI wizard — recommended first run
rinse

# Show session history and time-saved metrics
rinse stats

# Print version
rinse --version
```

The interactive TUI walks you through setup — pick a PR, configure the runner, and let Rinse handle the loop.

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
rinse              # launch interactive TUI
rinse stats        # show session history and time-saved metrics
rinse --version    # print installed version
rinse --help       # show help
```

### Interactive TUI settings (press `s` inside the PR picker)

| Setting | Options | Description |
|---------|---------|-------------|
| `runner` | `opencode` (default), `claude` | AI agent to drive |
| `model` | any model string | AI model; leave blank for runner default |
| `reflect` | on/off | Enable reflection agent to improve rules each cycle |
| `branch` | branch name | Target branch for reflection commits (default: `main`) |
| `auto-merge` | on/off | Auto-merge PR once Copilot approves |

Settings are saved per-repo under `~/.rinse/`.

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
| `claude` CLI | Required when using the `claude` runner |
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
