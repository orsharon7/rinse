# AGENTS

Project instructions for AI coding agents.

<!-- BEGIN:COPILOT-RULES -->
## Coding Guidelines (AI-maintained)
*Auto-updated by pr-review-reflect — do not edit this section manually.*
*Last updated: 2026-04-14 from PR #16 review (optimized)*

### Shell Scripting
- Read interactive input from `/dev/tty`; render UI output to stderr.
- Validate all numeric CLI parameters (including `--stagger`) as integers ≥ 0 before arithmetic/`sleep`; validate divisors as ≥ 1; clamp display-width subtractions to ≥ 0.
- Pass an explicit `--repo`/scope flag to `gh`; never rely on ambient inference. Boolean flags are not interchangeable with `--flag=value`.
- Use `tput sc`/`tput rc` for cursor repositioning; never hard-code row/column values.
- Honor `--no-interactive`: skip all prompts, never fall back to a blocking alternative.
- Use `grep -E` for alternation; `\|` in BRE is non-portable across BSD/macOS and GNU.
- Never use `local` outside a function body; never leave `$(( ))` unbalanced. Embed resolved absolute paths in heredoc wrappers.
- Every status-accepting function (`true`/`false`/`skip`) must reflect that status visually; never silently discard it.
- Validate syntax with `bash -n`/`sh -n` before committing.
- Pipe with `bash -c 'set -euo pipefail; … | tee …'`; without `pipefail`, exit status is `tee`'s, masking upstream failures. When piping through `tee -a "$LOGFILE"`, suppress internal `log()` calls to avoid double-writing.
- Never swallow git branch/checkout failures with `|| true`; use `checkout -B <branch> origin/<branch> --set-upstream-to`; treat failure as fatal. Never use `git worktree add -B` with a shared branch name — use a uniquely namespaced local branch (e.g. PR-prefixed) and set upstream explicitly.
- Never `eval` a space-joined command string; build a Bash array and execute it directly.
- Never redirect a subprocess's output to the same logfile it writes internally; redirect to `/dev/null` or a separate log. Give each script its own uniquely named log file; never share filenames between concurrent processes.
- Keep `--help`/usage blocks in sync with actual arg-parsing. Error messages must reference the exact variable/value involved (e.g. include both local branch name and upstream ref for branch failures).
- **Locking:** Use atomic lock primitives (`mkdir`-based or `noclobber`) for mutual exclusion; never use racy pidfile checks. Acquire locks only after all precondition checks pass. Release on every exit path via `trap`/`finally`. In subshells that acquire a lock, install an EXIT trap to release it — signals can bypass normal flow. Gate resource-cleanup traps on ownership (only remove state this process created); use a mode flag/sentinel for modes like `--once` that don't own resources.
- **PIDs & liveness:** Write the actual worker PID (not parent/orchestrator) to pidfiles. Never use a PGID as a liveness signal unless the job was started via `setsid`; inherited PGIDs outlive child jobs.
- **Signal handling:** Use `handle_sigint`/`handle_sigterm` with `trap ... INT TERM` for interruption-specific side-effects (kill, sleep, prune); use `trap 'cleanup false' EXIT` for universal teardown. Guard cleanup side-effects behind a check for active child processes. Use `CLEANUP_DONE` idempotency guard to prevent double-execution when both EXIT trap and signal handler fire.
- **Worker/lock coordination:** In wrapper subshells, trap TERM/INT and forward to worker before `wait`-ing; never exit while worker mutates shared state. After `kill`, always `wait` for child to exit (escalating to SIGKILL) before releasing the lock. In daemon teardown, do not release job-dispatch locks a child may still hold — leave them for stale-PID detection to recover.
- Validate env vars used in numeric comparisons (e.g. `MAX_CONCURRENT`) as integers ≥ 1 at startup; non-integer values silently abort under `set -euo pipefail`.
- When surfacing sub-process errors, tail that component's dedicated log, not a shared interleaved log.
- Under `set -u`, guard associative array reads with `[[ -v arr[$key] ]]` or `${arr[$key]:-}`; return early when key is absent.

### Environment & CI Portability
- Validate all required env vars before constructing paths/commands; keep preflight error messages in sync with checks. Validate env vars used as integers at startup.
- Check both git identity pairs (`GIT_AUTHOR_NAME`/`EMAIL` and `GIT_COMMITTER_NAME`/`EMAIL`); a missing pair can pass preflight but fail `git commit`.
- Avoid `declare -A` without a Bash 4+ version check; macOS `/bin/bash` is typically 3.2 and will fail silently.

### Documentation Integrity
- Keep README file trees, artifact references, and prerequisites (`go.mod`/`package.json`, tool versions) in sync; remove stale references on rename/delete.
- Descriptions must match behavior exactly: no false "skips silently", no phantom methods. Match enum names (casing), argument names, and code-example signatures exactly — mismatches cause silent copy-paste breakage.
- Keep architecture diagrams and ADRs in sync with actual SDK/API call paths; account for all dispatch paths. Reconcile contradicting doc sections against actual imports; phrase design assertions as intent, not fact.

### CLI, Installers & Packaging
- Optional parameters default to empty; include the flag only when the user provides a value.
- Installer wrappers using absolute paths must bundle those helpers or document the dependency.

### TUI & Layout
- Use a single shared predicate per logical event; never duplicate format-detection logic.
- Layout-guard and render-guard must agree: clamp widget dimensions to ≥ 0; apply terminal-width fallback only when uninitialized. Account for all separator variants (ASCII `|` and box-drawing `│`) when trimming.
- Document helper return-value semantics (inner vs. outer width); apply border/padding at the call site. Gate input routing and focus to the active interaction mode.
- Use the currently selected/active item (not hardcoded index `[0]`) when resolving paths or scripts.

### UI & State Management
- Persist final item state on the data object (e.g. `finalStatus`); never derive display state from a mutable run-scoped map. Apply streaming styling only to actively streaming items.
- Normalize internal/legacy identifiers to canonical labels before rendering.
- Gate streaming finalization on all execution axes — `stepIdx > 0` alone misses agent transitions within step 0. Gate phase-scoped resets on an actual phase transition (`nextPhase != currentPhase`); never clear state on incidental log-line content.
- Never hard-code a UI value that mirrors a backend configurable setting; source it from the backend payload.
- Track "user has edited this field" with an explicit boolean; never use a sentinel value (e.g. `"main"`) as a proxy for "unmodified".

### Go
- **Performance:** Use `strings.Builder`; never `+=` in a loop. Pre-compute repeated expressions before loops.
- **Error handling:** Return errors to `main()` — never `os.Exit()` inside a UI lifecycle. Prefer `strings.Cut()` over `strings.Index()`. Always check `scanner.Err()` after `bufio.Scanner` loops.
- **Safety:** Use pointers for non-copy-safe types (`strings.Builder`, `sync.Mutex`) in frequently-copied structs. Drain data channels before acting on a done-channel signal.
- **Paths & Unicode:** Use `filepath.Dir()`/`filepath.Join()`; rune-aware truncation for user-visible strings.
- **Module hygiene:** Run `go mod tidy` before committing; direct imports must not be `// indirect`. Every `-X pkg.Symbol=value` LDFLAGS symbol must be declared as a `var`. Never declare unreferenced package-level identifiers.
- **Config:** Initialize fields from most-specific scope first (per-repo before global). Use explicit `ok` from map lookup — never treat zero values as "unset". Use scoped values verbatim; never merge with globals via `||`. Never persist a field without reading it back; verify persisted paths match current context.
- **State & maps:** Parse input into canonical form before deriving values. Guard map writes against empty keys.
- **Log/text parsing:** Anchor numeric extraction to a known prefix/suffix; never scan all whitespace-delimited tokens.

### Python
- **Safety & initialization:** Guard boolean aggregates against empty collections. Initialize closure-captured accumulators before first use. If a docstring says "Never raises", wrap all code paths including pre-`try` operations.
- **Streams & serialization:** Close async streams via `await stream.aclose()` in `try/finally`. Join protocol frames with the spec-mandated separator. Verify serializer options match docstring claims.
- **Imports, parsing & deps:** Grep for remaining usages before removing an import. Parse structured strings (e.g. ARM IDs) by key, not fixed index. Guard removed third-party symbols with `try/except ImportError`; raise a descriptive `RuntimeError` at call-time. Reconcile pinned versions with imported symbols.
- **Dead code & warnings:** Remove overwritten variable assignments. Use narrowly scoped `"ignore"` filters with a precise `message` regex — broad `"always"` re-enables warnings globally.

### Configuration & Cloud Resources
- Never expose a config setting not wired to runtime behavior; verify end-to-end: read → validate → pass to constructor. In typed settings models (e.g. Pydantic `BaseSettings`), declare every env-var-backed field explicitly; `getattr` with a fallback silently bypasses the schema.
- Never infer a cloud resource's subscription/resource group from an unrelated resource; use explicit config (e.g. `AZURE_SEARCH_RESOURCE_ID`) — cross-resource-group inference will silently 404.

### Frontend & Accessibility
- Build a lookup map before render loops; never use `Array.find()` (O(n)) inside a loop.
- Give every interactive control an explicit `aria-label`; `title` and `placeholder` are not substitutes.
- Use CSS classes for animations and `:hover` effects (not inline `style`/event handlers); use design tokens or Tailwind for colors (not inline hex). Trigger scroll/layout side-effects after DOM mutations, not before.
- Use `overflow-y: auto` with viewport-relative caps; never `max-height` + `overflow: hidden`. Never combine percentage `width` with fixed pixel `height` on an SVG; use `aspect-ratio`.
- Never call browser-only APIs (`window`, `document`, `matchMedia`) during SSR; feature-detect and fall back. In `useEffect`, verify SSE/event payload runtime types before property access.

### API, Testing & Observability
- When a backend enum gains values/aliases, update all mirrored client-side type definitions in the same change.
- With `MagicMock`, explicitly set every field controlling branching logic (unset attributes are truthy); update fixtures and remove unused imports when settings fields are renamed/removed.
- Retry log denominators must match actual total attempts; keep constant names, comments, and loop bounds mutually consistent.

<!-- END:COPILOT-RULES -->
