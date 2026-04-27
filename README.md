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
[![Landing page](https://img.shields.io/badge/site-orsharon7.github.io%2Frinse-blue)](https://orsharon7.github.io/rinse)

---

**Rinse drives GitHub Copilot through your PR review loop so you don't have to.**

Request review → wait → read comments → fix → repeat. Rinse handles every step, in a loop, until Copilot approves. One command. Walk away.

---

## ⚡ Install

```bash
brew tap orsharon7/rinse https://github.com/orsharon7/rinse
brew install rinse
```

That's it.

<details>
<summary>Other install methods</summary>

**Direct download (macOS / Linux):**
```bash
curl -fsSL https://raw.githubusercontent.com/orsharon7/rinse/master/install.sh | sh
```

**Build from source (requires Go ≥ 1.24):**
```bash
git clone https://github.com/orsharon7/rinse.git
cd rinse
make install
```

> **Note:** `make install` installs only the `rinse` binary. Set `RINSE_SCRIPT_DIR` to your local `scripts/` directory if the runner scripts are needed.

Pre-built binaries are also available on the [Releases](https://github.com/orsharon7/rinse/releases) page.

</details>

The `--model` flag accepts any valid opencode model string (e.g. `github-copilot/claude-sonnet-4.6`).

---

## 🚀 Quick start

```bash
# Interactive TUI wizard — recommended first run
# Requires staged changes: run `git add <files>` first.
# To review an existing PR without staging, use: rinse start <pr> --repo owner/repo
rinse

# Predict what Copilot will comment on — before you open the PR
rinse predict

# Create a per-repo config (.rinse.json) for shared team settings
rinse init

# Show session history and time-saved metrics (30-day rolling)
# Requires opt-in: run `rinse opt-in` once to enable stats collection
rinse stats

# Show today's PR review dashboard (approval rate, timing)
rinse report

# Check the Copilot review status of a PR (great for CI scripts)
rinse status 42 --repo owner/repo

# Start the review loop non-interactively (no TTY required)
rinse start 42 --repo owner/repo

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
rinse predict      # predict Copilot comments before you push
rinse init         # scaffold a per-repo .rinse.json config (guided setup)
rinse stats        # show session history and time-saved metrics (30-day)
rinse report       # show today's PR review dashboard (approval rate, timing)
rinse status <pr>  # print Copilot review status (agent/CI use)
rinse start <pr>   # start review loop non-interactively (no TTY)
rinse run <pr>     # native Go runner — NDJSON lifecycle events (CI)
rinse opt-in       # enable session stats collection (required for stats/report)
rinse opt-out      # disable session stats collection
rinse --version    # print installed version
rinse --help       # show full help
```

### `rinse predict`

Flags what Copilot will likely comment on **before you open the PR** — from your staged diff, locally, in under a second.

```
$ rinse predict

◇  rinse predict  —  3 likely Copilot comments detected

  ◇ Missing error handling: os.WriteFile return discarded         91%
   internal/runner/runner.go:147

  ◇ Unused variable: 'ctx' declared but not referenced            85%
   internal/runner/runner.go:203

  ◇ Naked return in named-return function                         72%
   internal/engine/engine.go:89

   3 predictions · run `rinse` to fix automatically
```

AST-based analysis — no network call, no LLM. Confidence scores tell you which issues Copilot is most likely to flag. Fix the high-confidence ones before review, ship cleaner PRs.

**Pro (v0.4):** `rinse predict --interactive` (or `-i`) opens a TUI review loop so you can step through predictions and fix or open each one in your editor (`e` to open in `$EDITOR`). `rinse predict --doc-drift` uses an LLM to detect where your docs have drifted from your code.

### `rinse init`

Scaffolds a `.rinse.json` config file in the current directory. Prompts for engine, model, reflection settings, and auto-merge preference. Commit this file to share consistent defaults with your team — every developer on the repo gets the same starting point.

### `rinse report`

Shows a today-focused PR review dashboard. Falls back to all-time data if no sessions were recorded today.

```
● RINSE  Today's Report · April 18, 2026

Cycles run              3
PRs reviewed            3
PRs approved            2 (67%)

Time saved              ~1.2 hours (est.)
Comments fixed          14
Avg per PR              5 comments, 2.1 iters

Fastest cycle           4 min  PR #42
Longest cycle           18 min  PR #38
```

Use `rinse stats` instead for a 30-day rolling summary.

### `rinse status`

```bash
rinse status [<pr>] [--repo <owner/repo>] [--json]
```

Prints the current Copilot review status of a PR without launching the TUI. Suitable for CI pipelines and agent scripts. When `<pr>` is omitted, auto-detects from the current branch.

Output statuses: `approved` / `pending` / `new_review` / `no_reviews` / `merged` / `closed` / `error`

### `rinse start`

```bash
rinse start <pr> [--repo <owner/repo>] [--cwd <path>] [--runner opencode|claude] \
            [--model <model>] [--reflect] [--auto-merge] [--notify] [--json]
```

Starts the PR review fix loop without the TUI — no TTY required. Suitable for agent pipelines and CI. Streaming output goes to stderr; use `--json` to get a machine-readable result on stdout after the run completes.

### Interactive TUI settings (press `s` inside the PR picker)

| Setting | Options | Description |
|---------|---------|-------------|
| `runner` | `opencode` (default), `claude` | AI agent to drive |
| `model` | any model string | AI model; leave blank for runner default |
| `reflect` | on/off | Enable reflection agent to improve rules each cycle |
| `branch` | branch name | Target branch for reflection commits (default: `main`) |
| `auto-merge` | on/off | Auto-merge PR once Copilot approves |
| `notify` | on/off | Send a desktop notification when the cycle completes (macOS: osascript, Linux: notify-send) |

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

## 🤖 CI / pipeline usage

`rinse start`, `rinse run`, and `rinse status` are designed for non-interactive use in agent pipelines and CI.

### Run the review loop in CI

**`rinse run`** is the preferred CI command — it's the native Go runner with structured NDJSON output, no shell-script wrapper, and automatic JSON mode when stdout is not a TTY:

```bash
# Stream NDJSON lifecycle events on stdout, exit with structured result
rinse run 42 --repo owner/repo --json
```

Each output line is a JSON event (`phase`, `iteration_start`, `poll`, `iteration_complete`, `done`, `error`). See `rinse --help` for the full event schema.

Exit codes: `0` = approved · `1` = max iterations · `2` = error

**`rinse start`** wraps the shell-script runners and is useful when you need the `claude` runner or want shell-level output:

```bash
rinse start 42 --repo owner/repo --json
```

### Check PR status

```bash
# Human-readable
rinse status 42 --repo owner/repo

# Machine-readable (for scripts and pipelines)
rinse status 42 --repo owner/repo --json
```

JSON schema:
```json
{"ok": true,  "pr": "42", "repo": "owner/repo", "status": "approved"}
{"ok": false, "pr": "42", "repo": "owner/repo", "status": "error", "error": "..."}
```

`status` values: `approved` · `pending` · `new_review` · `no_reviews` · `merged` · `closed` · `error`

---

## Excluding files with .rinseignore

Place a `.rinseignore` file at your repo root to tell RINSE which files and paths to skip during review cycles. Uses the same syntax as `.gitignore`.

```
# Generated files
*.pb.go
*.gen.go
vendor/

# Auto-generated mocks
internal/mocks/

# Database migrations (auto-generated)
# internal/db/migrations/*.sql
```

When Copilot comments on an ignored path, RINSE:
1. **Skips** the comment — does not pass it to the AI agent for fixing.
2. **Replies** to the comment with `Skipped — file is excluded by .rinseignore`.

`rinse init` creates a starter `.rinseignore` with common patterns. Commit it so the whole team benefits.

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
- Logs: session data is stored in `~/.rinse/sessions/`; shell script runner logs land in `~/.pr-review/logs/` (set `RINSE_SCRIPT_DIR` to your local `scripts/` dir if needed)

---

## 📄 License

BSL 1.1 — see [LICENSE](LICENSE). Or Sharon's IP.
