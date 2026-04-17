# RINSE

```
 ██████╗ ██╗███╗  ██╗███████╗███████╗
 ██╔══██╗██║████╗ ██║██╔════╝██╔════╝
 ██████╔╝██║██╔██╗██║███████╗█████╗  
 ██╔══██╗██║██║╚████║╚════██║██╔══╝  
 ██║  ██║██║██║  ███║███████║███████╗
 ╚═╝  ╚═╝╚═╝╚═╝  ╚══╝╚══════╝╚══════╝
  PR reviews without the mess.
```

[![Go version](https://img.shields.io/github/go-mod/go-version/orsharon7/rinse)](https://go.dev/)
[![License: BSL 1.1](https://img.shields.io/badge/License-BSL_1.1-purple.svg)](./LICENSE)
[![Latest release](https://img.shields.io/github/v/release/orsharon7/rinse)](https://github.com/orsharon7/rinse/releases)
[![PRs welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](https://github.com/orsharon7/rinse/pulls)

---

**Rinse drives GitHub Copilot through your PR review loop so you don't have to.**

Request review → wait → read comments → fix → repeat. Rinse handles every step, in a loop, until Copilot approves. One command. Walk away.

---

## ⚡ Install

```bash
git clone https://github.com/orsharon7/rinse.git
cd rinse
make install
```

> **Note:** `make install` installs only the `rinse` binary. The runner scripts (`scripts/pr-review-*.sh`) are **not** installed automatically. To use `rinse` after installation, either:
> - Copy the `scripts/` directory to a location next to the installed binary, **or**
> - Set `RINSE_SCRIPT_DIR` (or the legacy alias `PR_REVIEW_SCRIPT_DIR`) to the path of your local `scripts/` directory.

Or build and install manually (requires Go ≥ 1.24):

```bash
mkdir -p ~/.local/bin
go build -ldflags "-X main.version=$(git describe --tags --always)" -o ~/.local/bin/rinse .
```

Without the `-ldflags` above, `rinse --version` will print `dev`.

Pre-built binaries are also available on the [Releases](https://github.com/orsharon7/rinse/releases) page.

---

## 🚀 Quick start

```bash
# Interactive TUI wizard — recommended first run
rinse

# One-time repo setup: create .rinse.json config
rinse init

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
3. **Waits** for Copilot to finish (animated progress)
4. **Reads** every unresolved comment and hands them to your AI fix agent
5. **Pushes** the fixes and requests the next review
6. **Loops** until approved — then shows a merge menu

---

## 🛠 Options

```
rinse              # launch interactive TUI (PR picker)
rinse init         # create a per-repo .rinse.json config (guided setup)
rinse start        # start a review cycle non-interactively
rinse status       # show current cycle status
rinse stats        # show session history and time-saved metrics
rinse --version    # print installed version
rinse --help       # show full help
```

### Interactive TUI settings (press `s` inside the PR picker)

| Setting | Options | Description |
|---------|---------|-------------|
| `runner` | `opencode` (default), `claude` | AI agent to drive |
| `model` | any model string | AI model; leave blank for runner default |
| `reflect` | on/off | Enable reflection agent to improve rules each cycle |
| `branch` | branch name | Target branch for reflection commits (default: `main`) |
| `auto-merge` | on/off | Auto-merge PR once Copilot approves |

Interactive TUI preferences are saved in your user config directory under `rinse/config.json` (for example, `~/.config/rinse/config.json` on Linux). Other Rinse data such as session history or runtime state may still appear under `~/.rinse/`.

### Example Copilot models (non-exhaustive)

Configure any valid model string via the TUI `model` setting (or by invoking the underlying runner scripts directly), not via a `rinse --model` flag. The table below shows a few examples — any model string accepted by your runner can be used.

| Model | Model string |
|-------|--------------|
| Claude Sonnet 4.6 | `github-copilot/claude-sonnet-4.6` |
| Claude Sonnet 4.5 | `github-copilot/claude-sonnet-4.5` |
| Claude Sonnet 4   | `github-copilot/claude-sonnet-4` |

---

## 📁 Config file (`.rinse.json`)

Run `rinse init` to create a per-repo config file. Commit it so your whole team shares the same defaults.

```json
{
  "engine":         "opencode",
  "model":          "",
  "reflect":        false,
  "reflect_branch": "main",
  "auto_merge":     false
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `engine` | string | `"opencode"` | Runner to use: `"opencode"` or `"claude"` |
| `model` | string | `""` | AI model string. Empty = runner default (`github-copilot/claude-sonnet-4.6` for opencode) |
| `reflect` | bool | `false` | Enable the reflection agent to update `AGENTS.md` / `CLAUDE.md` after each cycle |
| `reflect_branch` | string | `"main"` | Branch where reflection rules are committed |
| `auto_merge` | bool | `false` | Auto-merge the PR once Copilot approves |

Settings in `.rinse.json` are shared team defaults. Individual overrides can be set in the TUI settings panel (`s`) or passed as flags to `rinse start`.

---

## 🪞 Reflection agent

The `reflect` setting runs a reflection pass in parallel with each fix cycle.

It reads Copilot's comments, extracts generalizable coding rules, and permanently updates `AGENTS.md` + `CLAUDE.md` in your repo — **pushed directly to `main` via a git worktree, never polluting the PR branch**.

Every future cycle loads those rules automatically, so the AI makes fewer mistakes and Copilot leaves fewer comments. The loop gets faster over time.

Enable reflection via the TUI settings (press `s` inside the PR picker and toggle `reflect` on), or pass `--reflect` directly to the underlying runner scripts:

```bash
./scripts/pr-review-opencode.sh 42 --repo owner/repo --cwd ~/dev/my-repo --reflect
```

---

## 🔔 Desktop notifications

Pass `--notify` to `rinse start` to receive a native desktop notification when the review cycle completes:

```bash
rinse start 42 --repo owner/repo --notify
```

- **macOS** — uses `osascript` (built-in, no extra tools needed)
- **Linux** — uses `notify-send` (install `libnotify-bin` if missing)
- **CI / headless** — automatically skipped when `TERM=dumb` or `DISPLAY` is unset; never breaks a pipeline

Notifications are best-effort — a failure to notify never interrupts or fails the CLI.

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

## 🔧 Environment variables

| Variable | Description |
|----------|-------------|
| `RINSE_SCRIPT_DIR` | Override the directory where runner scripts (`pr-review-*.sh`) are found. Set this if you installed the binary but kept the scripts elsewhere. |
| `PR_REVIEW_SCRIPT_DIR` | Legacy alias for `RINSE_SCRIPT_DIR`. Deprecated — prefer `RINSE_SCRIPT_DIR`. |
| `RINSE_WEBHOOK_URL` | When set, RINSE POSTs a JSON payload to this URL after each completed review cycle. Useful for Slack notifications, dashboards, or CI integrations. |

---

## 🤝 Contributing

1. Fork the repo and create a branch: `git checkout -b feat/my-change`
2. Make your changes — keep code POSIX-compatible where possible
3. Test against a real PR: `rinse` (launch the TUI and select the PR)
4. Open a PR — Copilot reviews it automatically
5. To update coding rules for future sessions, enable the TUI `reflect` setting when reviewing the PR (press `s` in the PR picker and toggle `reflect` on)

**Dev tips:**

- TUI source is in `internal/tui/` (Go + Charm Bubble Tea)
- Run `make` to build, `make install` to install locally
- Logs: shell script runner logs land in `~/.pr-review/logs/<owner_repo>-pr-<n>.log`; Go binary session data is stored in `~/.rinse/sessions/`

---

## 📄 License

BSL 1.1 — see [LICENSE](LICENSE). Or Sharon's IP.
