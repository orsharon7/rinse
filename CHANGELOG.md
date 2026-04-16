# Changelog

All notable changes to **rinse** are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

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

[1.0.0]: https://github.com/orsharon7/rinse/releases/tag/v1.0.0
