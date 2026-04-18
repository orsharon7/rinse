# ROADMAP

PR reviews without the mess. RINSE drives AI agents in a loop until your code is approved — no babysitting, no context-switching, no dropped comments.

---

## v0.2 — From Scripts to Product ✓

The foundation. RINSE goes from a bag of bash scripts to a real, distributable tool.

- Real Go binary — single static executable, no runtime deps
- Config scaffolding — `rinse init` sets up any repo in seconds
- Engine reliability — crash recovery, state checkpoints, clean restarts
- Cycle monitor — real-time status and timing in your terminal
- README rewrite — product voice, clear install path

---

## v0.3 — Prove It Worked

Make the value visible. After every cycle, you should know exactly what RINSE did and whether it was worth it.

- Post-cycle summary — time saved, comments fixed, patterns detected
- Local session history and metrics — searchable run log
- Install in 10 seconds — `curl | sh` or `brew install rinse`
- First-run wizard — guided setup with sane defaults
- `--json` flag — machine-readable output for CI and scripting
- GitHub Action — `orsharon7/rinse-action` runs RINSE on every PR automatically

---

## v0.4 — Predict and Prevent ✓

Know what Copilot will say before you push. RINSE surfaces likely review patterns pre-flight.

- `rinse predict` — scan a staged diff or PR for predicted review comments
- `rinse predict --interactive` (Pro) — step through predictions and apply fixes in a TUI
- `rinse predict --doc-drift` (Pro) — LLM-backed detection of stale or missing godoc
- `rinse stats --predict` — hit-rate dashboard showing prediction accuracy over time
- Pro gate — `--interactive` and `--doc-drift` require a Pro licence (`~/.rinse/config.json` or `RINSE_PRO=1`)

---

## v1.1 — Smarter Pattern Classification ✓

Pattern labels go from raw comment text to a consistent, queryable taxonomy.

- Keyword-to-label classifier — maps Copilot comment text to 15 snake_case categories (error_handling, nil_check, naming, security, etc.)
- Session files and SQLite now store structured labels, enabling trend queries and dashboard tooling

---

## v1.0 — Ship to the World

RINSE becomes something you recommend to your team without caveats.

- Landing page at [rinse.sh](https://rinse.sh)
- Desktop notifications — know when Copilot finishes without watching your terminal
- Persistent session history — searchable across machines and projects
- Cross-machine deduplication — never fix the same comment twice
- Headless / daemon mode — run in CI or a background process with no TTY

---

## RINSE Pro — Team Features

For teams that want shared visibility and accountability.

- **Team dashboard** — per-developer stats, repo trends, cycle benchmarks
- **Slack digest** — weekly summary of review cycles and patterns caught
- **Custom model support** — swap in any Copilot-compatible model per repo
- **SSO + audit logs** — Enterprise-grade access control and traceability

---

Built by Or Sharon. BSL 1.1 licensed.
