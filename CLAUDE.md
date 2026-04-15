# CLAUDE

Project instructions for AI coding agents.

<!-- BEGIN:COPILOT-RULES -->
## Coding Guidelines (AI-maintained)
*Auto-updated by pr-review-reflect — do not edit this section manually.*
*Last updated: 2026-04-15 from PR #22 review*

### Shell Scripting
- Read interactive input from `/dev/tty`; render UI output to stderr.
- Validate numeric CLI params as integers ≥ 0 before arithmetic/`sleep`; divisors ≥ 1; clamp display-width subtractions to ≥ 0.
- Pass explicit `--repo`/scope to `gh`; never rely on ambient inference. Boolean flags are not interchangeable with `--flag=value`.
- Use `tput sc`/`tput rc` for cursor repositioning; never hard-code row/column values.
- Honor `--no-interactive`: skip all prompts, never fall back to a blocking alternative.
- Use `grep -E` for alternation (`\|` in BRE is non-portable on BSD/macOS).
- Never use `local` outside a function; never leave `$(( ))` unbalanced; embed resolved absolute paths in heredoc wrappers.
- Every status-accepting function (`true`/`false`/`skip`) must reflect status visually; never silently discard it.
- Validate syntax with `bash -n`/`sh -n` before committing.
- Pipe via `bash -c 'set -euo pipefail; … | tee …'`; without `pipefail`, exit status is `tee`'s. When piping through `tee -a "$LOGFILE"`, suppress internal `log()` calls to avoid double-writing.
- Never swallow git branch/checkout failures with `|| true`; use `checkout -B <branch> origin/<branch> --set-upstream-to`; treat failure as fatal. Never `git worktree add -B` with a shared branch name — use a uniquely namespaced (PR-prefixed) local branch.
- Never `eval` a space-joined command string; build a Bash array and execute it directly.
- Give each script its own uniquely named log file; never redirect subprocess output to a logfile it also writes internally — use `/dev/null` or a separate log.
- Keep `--help`/usage in sync with arg-parsing. Error messages must name the exact variable/value involved.
- **Locking:** Use atomic primitives (`mkdir`/`noclobber`); never racy pidfile checks. Acquire after all preconditions pass; release on every exit path via `trap`/`finally`. In subshells, install EXIT trap to release the lock. Gate cleanup on ownership; use a sentinel for modes like `--once` that don't own resources.
- **PIDs & liveness:** Write the actual worker PID (not parent) to pidfiles. Never use PGID as liveness unless started via `setsid`.
- **Signal handling:** `trap ... INT TERM` for side-effects (kill/sleep/prune); `trap 'cleanup false' EXIT` for teardown. Use `CLEANUP_DONE` guard to prevent double-execution. Guard cleanup behind a check for active children.
- **Worker/lock coordination:** Trap TERM/INT in wrapper subshells and forward to worker before `wait`; never exit while worker mutates shared state. After `kill`, `wait` (escalate to SIGKILL) before releasing lock. Don't release job-dispatch locks a child may still hold — leave for stale-PID recovery.
- Validate env vars used in numeric comparisons (e.g. `MAX_CONCURRENT`) as integers ≥ 1 at startup.
- Surface subprocess errors from that component's dedicated log, not a shared interleaved log.
- Under `set -u`, guard associative array reads with `[[ -v arr[$key] ]]` or `${arr[$key]:-}`; return early when absent.
- When a script mutates multiple files that must be committed together, include all of them in the `git status --porcelain` change-detection check; checking only a subset risks a silent early-exit that leaves other mutated files uncommitted.

### Environment & CI Portability
- Validate all required env vars (including integer ones) before constructing paths/commands; keep error messages in sync with checks.
- Check both git identity pairs (`GIT_AUTHOR_NAME`/`EMAIL` and `GIT_COMMITTER_NAME`/`EMAIL`); a missing pair passes preflight but fails `git commit`.
- Avoid `declare -A` without a Bash 4+ check; macOS `/bin/bash` is 3.2 and fails silently.

### Documentation Integrity
- Keep README file trees, artifact references, and prerequisites (`go.mod`/`package.json`, tool versions) in sync; remove stale references on rename/delete.
- Match behavior exactly: no false "skips silently", no phantom methods. Match enum names, argument names, and code-example signatures exactly — mismatches cause silent copy-paste breakage.
- Keep architecture diagrams and ADRs in sync with actual SDK/API call paths. Reconcile contradicting sections against actual imports; phrase design assertions as intent, not fact.
- User-facing documentation must use standard prose conventions: sentences start with a capital letter, proper nouns (tool names, products) are capitalized consistently.

### CLI, Installers & Packaging
- Optional parameters default to empty; include flags only when the user provides a value.
- Installer wrappers using absolute paths must bundle those helpers or document the dependency.
- When renaming a binary or artifact, update all installer scripts, launchers, and cross-references atomically in the same change.
- Build commands in documentation and install hints must include all flags required for a correct build (e.g. `-ldflags -X main.version=...` for version injection); a bare `go build` without `-ldflags` embeds wrong metadata that `--version` will expose.

### TUI & Layout
- Use a single shared predicate per logical event; never duplicate format-detection logic.
- Layout-guard and render-guard must agree: clamp widget dimensions to ≥ 0; apply terminal-width fallback only when uninitialized. Account for all separator variants (ASCII `|` and box-drawing `│`) when trimming.
- Document helper return-value semantics (inner vs. outer width); apply border/padding at the call site. Gate input routing and focus to the active interaction mode. Use the active item (not hardcoded `[0]`) when resolving paths or scripts.
- Render functions must never return a string wider than their `width` argument; clamp all computed sub-widths to `max(0, width-used)` and treat negative space as 0 rather than substituting a forced minimum.
- Every view/mode must handle all globally advertised keybindings (e.g. quit) consistently; never let an overlay or sub-view silently swallow a key that the help text promises will work.

### UI & State Management
- Persist final item state on the data object (e.g. `finalStatus`); never derive display state from a mutable run-scoped map. Apply streaming styling only to actively streaming items.
- Normalize internal/legacy identifiers to canonical labels before rendering.
- Gate streaming finalization on all execution axes (`stepIdx > 0` alone misses transitions within step 0). Gate phase-scoped resets on an actual phase transition (`nextPhase != currentPhase`); never clear state on incidental log-line content.
- Never hard-code a UI value that mirrors a backend configurable; source it from the backend payload.
- Track "user has edited this field" with an explicit boolean; never use a sentinel value (e.g. `"main"`) as a proxy for "unmodified".

### Go
- **Performance:** Use `strings.Builder`; never `+=` in a loop. Pre-compute repeated expressions before loops.
- **Error handling:** Return errors to `main()` — never `os.Exit()` inside a UI lifecycle. Prefer `strings.Cut()` over `strings.Index()`. Always check `scanner.Err()` after `bufio.Scanner` loops. Never set `err: nil` or use a success indicator in an action result message when the underlying operation failed — always propagate the actual error. Never pair a success icon/symbol with a non-nil error in a result struct; when `err != nil`, use a failure icon so that visual output and error state agree.
- **Filesystem migration:** Use `os.IsNotExist(err)` to gate path migration or fallback logic; surface (don't swallow) all other `os.Stat` errors such as permission failures. When `os.Stat(newPath)` fails for a non-NotExist reason, fall back to `oldPath` if it exists rather than silently returning `newPath` — permission/mount errors must not cause silent config loss.
- **Format parsing:** Always check both the error and the scanned-item count from `fmt.Sscanf`/`fmt.Sscanf`-family calls; a partial scan returns no error but produces zero values, causing silent data corruption (e.g. colors rendered as black).
- **Safety:** Use pointers for non-copy-safe types (`strings.Builder`, `sync.Mutex`) in frequently-copied structs. Drain data channels before acting on a done-channel signal.
- **Module hygiene:** Run `go mod tidy` before committing; direct imports must not be `// indirect`. Every `-X pkg.Symbol=value` LDFLAGS symbol must be declared as a `var`. No unreferenced package-level identifiers. Use `filepath.Dir()`/`filepath.Join()`; rune-aware truncation for user-visible strings.
- **Config & maps:** Initialize fields most-specific-first (per-repo before global). Use explicit `ok` from map lookup; never treat zero values as "unset". Use scoped values verbatim; never merge with globals via `||`. Never persist a field without reading it back. Guard map writes against empty keys; parse input into canonical form before deriving values.
- **Log/text parsing:** Anchor numeric extraction to a known prefix/suffix; never scan all whitespace-delimited tokens.

### Python
- **Safety & initialization:** Guard boolean aggregates against empty collections. Initialize closure-captured accumulators before first use. If a docstring says "Never raises", wrap all code paths including pre-`try` operations.
- **Streams & serialization:** Close async streams via `await stream.aclose()` in `try/finally`. Join protocol frames with the spec-mandated separator. Verify serializer options match docstring claims.
- **Imports, parsing & deps:** Grep for remaining usages before removing an import. Parse structured strings (e.g. ARM IDs) by key, not fixed index. Guard removed third-party symbols with `try/except ImportError`; raise a descriptive `RuntimeError` at call-time. Reconcile pinned versions with imported symbols.
- **Dead code & warnings:** Remove overwritten assignments. Use narrowly scoped `"ignore"` filters with a precise `message` regex — broad `"always"` re-enables warnings globally.

### Configuration & Cloud Resources
- Never expose a config setting not wired to runtime behavior; verify end-to-end: read → validate → pass to constructor. In typed settings models (e.g. Pydantic `BaseSettings`), declare every env-var-backed field explicitly; `getattr` with a fallback silently bypasses the schema.
- Never infer a cloud resource's subscription/resource group from an unrelated resource; use explicit config (e.g. `AZURE_SEARCH_RESOURCE_ID`) — cross-resource-group inference will silently 404.

### Frontend & Accessibility
- Build a lookup map before render loops; never `Array.find()` (O(n)) inside a loop.
- Give every interactive control an explicit `aria-label`; `title` and `placeholder` are not substitutes.
- Use CSS classes for animations/`:hover` (not inline style/handlers); use design tokens or Tailwind for colors. Trigger scroll/layout side-effects after DOM mutations, not before.
- Use `overflow-y: auto` with viewport-relative caps; never `max-height` + `overflow: hidden`. Never combine percentage `width` with fixed pixel `height` on an SVG; use `aspect-ratio`.
- Never call browser-only APIs (`window`, `document`, `matchMedia`) during SSR; feature-detect and fall back. In `useEffect`, verify SSE/event payload runtime types before property access.

### API, Testing & Observability
- When a backend enum gains values/aliases, update all mirrored client-side type definitions in the same change.
- With `MagicMock`, explicitly set every field controlling branching logic (unset attributes are truthy); update fixtures and remove unused imports when settings fields are renamed/removed.
- Retry log denominators must match actual total attempts; keep constant names, comments, and loop bounds mutually consistent.

### Structured Text & Marker Validation
- Validate paired delimiters (e.g. `<!-- BEGIN:X -->` / `<!-- END:X -->`) as exact full lines using `grep -Fxc`; never substring-match (false positives on comments/examples). Assert each appears exactly once AND BEGIN precedes END.
- When a script writes identical content to multiple files, add a post-write comparison (`cmp -s <(extract A) <(extract B)`) and fail loudly on divergence. Never use `$()` string equality for section comparison — bash strips trailing newlines; use `cmp -s` instead.
- On validation failure, revert affected files (`git checkout -- <file>`) and abort; never continue with a corrupt section.
- After any agent/subprocess run that may modify tracked files, assert pre-existing files still exist; revert and abort if missing.
- After any agent/subprocess run, re-assert the full filesystem invariant of critical files — not just existence but also file type (e.g. symlink vs regular file); revert and abort if the type changed unexpectedly.

<!-- END:COPILOT-RULES -->
