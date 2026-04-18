# Changelog

All notable changes to **rinse** are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

---

## [Unreleased]

### Added

- **`rinse` Go binary** — full rewrite of the TUI as a native Go CLI; replaces the `pr-review-launch.sh` entry point. Built with Bubble Tea + Lip Gloss.
- **`rinse init`** — guided per-repo setup wizard; scaffolds `.rinse.json` with engine, model, reflection, and auto-merge preferences. Commit `.rinse.json` to share defaults with your team.
- **`rinse start`** — non-interactive review loop for agent pipelines and CI (no TTY required). Accepts `--repo`, `--cwd`, `--model`, `--runner`, `--reflect`, `--reflect-main-branch`, `--auto-merge`, `--json`.
- **`rinse status`** — machine-readable Copilot review status for a PR (`approved`, `pending`, `new_review`, `no_reviews`, `merged`, `closed`, `error`). Supports `--json` for structured output.
- **`rinse stats`** — reads local session history and prints PRs reviewed, comments fixed, avg iterations, estimated time saved, and top recurring code patterns.
- **`rinse report`** — all-time dashboard view: total cycles run, PRs reviewed, comments fixed, estimated time saved, and top recurring patterns. Rendered with Lip Gloss for a clean terminal output.
- **First-run onboarding wizard** — multi-step TUI (Steps A–E) shown on first launch: overview, cycle naming, defaults picker, cycle preview, and celebration animation. Fully `$NO_COLOR`-aware.
- **PR picker TUI** — interactive list of open PRs with vim keybindings (`j/k`, `g/G`), PR number jump (`#`), settings panel (`s`), refresh (`r`), and keyboard shortcuts overlay (`?`).
- **Settings panel** — per-repo persistence of runner (`opencode`/`claude`), model, reflect toggle, reflect branch, and auto-merge; saved under `~/.rinse/`.
- **Reflection agent integration** — `--reflect` / TUI toggle runs a second AI agent after each fix cycle to extract coding rules and push them to `AGENTS.md` + `CLAUDE.md` on the main branch via a git worktree.
- **Post-cycle insights** — session summary printed after each completed cycle: PR, runner, model, iterations, comments fixed, patterns detected.
- **Session persistence** — each run saved as JSON to `~/.rinse/sessions/`; feeds `rinse stats`.
- **SQLite telemetry DB** — optional local telemetry written to `~/.rinse/rinse.db` for richer future analytics.
- **Distributed lock** — prevents concurrent RINSE cycles on the same PR via file-based locking under `~/.pr-review/locks/`.
- **Crash recovery** — state machine in the runner detects interrupted cycles and resumes cleanly.
- **`RINSE_SCRIPT_DIR` env var** — override where runner scripts are found; `PR_REVIEW_SCRIPT_DIR` accepted as legacy alias.
- **`NO_COLOR` env var** — when set to any non-empty value, disables all ANSI colour output. Follows the [no-color.org](https://no-color.org) standard.
- **`.rinseignore`** — optional file in the repo root; list path patterns (one per line) to exclude from RINSE review cycles. Useful for generated files, vendored code, or large assets.
- **`rinse --version`** — prints the installed version (set at build time via `-ldflags`).
- **`.github/CONTRIBUTING.md`** — full contributor guide: prerequisites, build steps, test workflow, repo layout, env vars, PR workflow, label system, and code style.
- **`.github/copilot-instructions.md`** — Copilot review instructions tuned for RINSE: focus on bugs, error handling, security, races; skip style/formatting.
- **ROADMAP.md** — phase-by-phase product plan.
- **GitHub Actions hardening** — bot filter, author-association check, job-level concurrency, and timeout on the PR review workflow.

### Changed

- **License**: MIT → BSL 1.1 (Business Source License) to protect Or Sharon's IP (PR #74 pending merge).
- **`--help` source of truth**: unified into `cli.PrintHelp()` in `internal/cli/cli.go`; removed duplicate `helpText` from `main.go`.
- **README**: rewritten to lead with the `rinse` binary — install, quick start, options, env vars, contributing. Shell scripts demoted to context.
- **Reflection agent**: refactored from `reflect-optimize` into a practice-based rule rewriter for higher signal-to-noise output.
- **TUI theme**: Catppuccin-inspired Lip Gloss palette with gradient titles, teal accents, and `$NO_COLOR` fallback throughout.
- **First-run wizard copy**: welcome screen leads with value proposition; "review session" renamed to "cycle" throughout for consistency; Step C toggle labels rewritten with concrete, specific language; Step E completion screen orients the user to the PR picker.
- **CONTRIBUTING.md label system section**: added two-tier rationale (RINSE-managed vs human-applied) and a warning not to manually remove `rinse:running` during an active cycle.
- **`--help` env vars section**: removed `RINSE_WEBHOOK_URL` (not implemented in Go); added `NO_COLOR`; added FILES section documenting `.rinse.json` and `.rinseignore`; documented `--pr` flag alias on `rinse status`.

### Fixed

- **First-run onboarding copy**: replaced laundry-app placeholder text ("Weekly laundry", "Delicates") with PR-review–specific copy.
- **`--help` accuracy**: removed phantom `RINSE_WEBHOOK_URL` env var (documented but never implemented in Go); documented `--pr` flag alias on `rinse status`; added FILES section for `.rinse.json` and `.rinseignore`.
- **Dead `rinse trends` command**: removed from `--help` — the command was documented but unimplemented on master, causing silent fallback to the TUI.
- **`rinse init` / `rinse status` / `rinse start` missing from docs**: all three commands existed but were absent from `--help` and README.
- **LICENSE badge**: README badge corrected from MIT to BSL 1.1.
- **Stale log path in README**: clarified that shell script logs go to `~/.pr-review/logs/` and Go binary session data goes to `~/.rinse/sessions/`.
- **`isProcessAlive` on Unix and Windows**: fixed platform-specific lock correctness.

---

## [1.0.0] — 2026-04-16

### Added

- **`pr-review-launch.sh`** — interactive TUI wizard covering all runner options (repo, PR number, model, reflection, wait timeout) with a confirmation summary before handoff.
- **`pr-review-opencode.sh`** — recommended runner for opencode + GitHub Copilot; no API key required. Drives `opencode run` in a loop until PR is approved.
- **`pr-review-claude-v2.sh`** — standalone Claude Code runner (v2); unlimited iterations, model-agnostic, does not depend on `pr-review.sh` primitives.
- **`pr-review-reflect.sh`** — reflection agent that extracts generalizable coding rules from Copilot comments and pushes them to `AGENTS.md` / `CLAUDE.md` on `main` via a temporary git worktree (never pollutes the PR branch).
- **`pr-review-ui.sh`** — shared terminal UI library: colored log lines, animated progress bar, and an arrow-key merge menu on success.
- **`pr-review-daemon.sh`** — persistent background process that polls watched PRs and fires configurable callbacks on review events.
- **`pr-review-cron.sh`** — lightweight cron-compatible poller.
- **`pr-review.sh`** — core JSON primitives (`status`, `comments`, `reply`, `reply-all`, `request`, `push`, `cycle`, `clear-state`, `watch`, `unwatch`, `poll-all`) used by the v1 runner and daemon.
- **`pr-review-claude.sh`** (v1, legacy) — original runner built on `pr-review.sh` primitives; retained for compatibility.
- **`tui/`** — Go (≥ 1.24) TUI binary (`pr-review-tui`) with pre-built binaries for macOS and Linux (amd64 / arm64).
- **`install.sh`** — one-line installer: detects platform, installs pre-built binary or builds from source, copies helper scripts, and writes a `pr-review` wrapper.
- **`--reflect` flag** on all runners to enable the reflection agent after each fix cycle.
- **`--dry-run` flag** on all runners to inspect startup state without making API calls.
- **`--no-interactive` flag** for CI / piped output scenarios.
- Parallel PR review execution via git worktrees (multiple PRs simultaneously).
- Startup state machine in `pr-review-claude-v2.sh`: handles no-review-yet, pending, unresolved comments, clean review, already-approved, merged/closed states automatically.
- GitHub Actions CI/CD pipeline.

### Changed

- Moved Claude runner into `claude/` subdirectory for cleaner monorepo layout.
- Reflection agent refactored from reflect-optimize into a practice-based rule rewriter for higher signal-to-noise output.

### Fixed

- Guard `jq` against `null` when no Copilot reviews exist.
- Stale state bug, `reply-all` poisoning, and control character handling in `pr-review.sh`.
- Detect both `Copilot` and `copilot-pull-request-reviewer[bot]` as pending reviewers.
- Fetch review body via GitHub REST API for accurate error detection.
- Auto re-request review when Copilot errors are detected mid-cycle.
- Emit `clean` status when a review has zero comments.
- Daemon repo path resolution after monorepo restructure.

---

[Unreleased]: https://github.com/orsharon7/rinse/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/orsharon7/rinse/releases/tag/v1.0.0
