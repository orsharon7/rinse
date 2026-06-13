# AGENTS

Project instructions for AI coding agents.

<!-- BEGIN:COPILOT-RULES -->
## Coding Guidelines (AI-maintained)
*Auto-updated by pr-review-reflect — do not edit this section manually.*
*Last updated: 2026-06-13 from PR #268 review*

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
- Installer scripts must pass all required build flags (e.g. `-ldflags`) on every code path, including fallback/alternative branches; a fallback that omits version-injection flags silently produces binaries reporting wrong metadata.
- When a CLI flag can override a value that was validated earlier in the same command, re-validate at the point of override — never assume the initial check covers all assignment paths.
- Only gate a command behind an external-tool presence/auth check if that command actually invokes the external tool; offline-safe commands (e.g. reading local DB/session data) must not require network auth.
- A flag the parser claims to support (or that help/usage text and runtime hints suggest, e.g. `--pr <N>`) must be fully consumed by the argument parser on every command path; a flag that is recognized but left in the leftover-args slice causes the command to still fail its usage check.
- When a command accepts a value both positionally and via a named flag (e.g. positional PR vs `--pr <n>`), the positional extractor must consume the named-flag form too: assign its value to the target and remove the flag plus its value from the remaining args, so the named form works without an extra positional.
- A shared positional-argument extractor must list the value-taking (argument-consuming) flags of *every* subcommand it serves; if a subcommand's value flags (e.g. `--timeout/--interval/--strategy/--bot`) are missing from that list, the flag's value is misread as the positional (e.g. `wait-review --timeout 5m 123` treats `5m` as the PR number).

### TUI & Layout
- Use a single shared predicate per logical event; never duplicate format-detection logic.
- Layout-guard and render-guard must agree: clamp widget dimensions to ≥ 0; apply terminal-width fallback only when uninitialized. Account for all separator variants (ASCII `|` and box-drawing `│`) when trimming.
- Document helper return-value semantics (inner vs. outer width); apply border/padding at the call site. Gate input routing and focus to the active interaction mode. Use the active item (not hardcoded `[0]`) when resolving paths or scripts.
- Render functions must never return a string wider than their `width` argument; clamp all computed sub-widths to `max(0, width-used)` and treat negative space as 0 rather than substituting a forced minimum.
- When content inherently exceeds `width` (i.e. `lipgloss.Width(content) > width`), truncate the content or return a shorter fallback — clamping padding or fill is not sufficient to prevent overflow in narrow terminals.
- Every view/mode must handle all globally advertised keybindings (e.g. quit) consistently; never let an overlay or sub-view silently swallow a key that the help text promises will work.
- Overlay/modal dismiss keys (e.g. `q`) must close the overlay and return to the parent view, not terminate the program; reserve program-quit for an explicitly separate binding (e.g. `ctrl+c`).
- Never enforce per-column minimum widths when their sum exceeds the total available space; check first whether available space accommodates all minimums, and if not, shrink proportionally or fall back to a single-column layout.
- Never use a fixed character-width constant for any column in a multi-column layout; derive all column widths from the available `w` (or fall back to a compact single-column layout below a minimum threshold).
- When computing a layout dimension from a collection of lines or items, use the maximum visual width across all members (e.g. `lipgloss.Width`); never use a single element as a proxy for the group width.

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
- **Serialization compatibility:** When two structs read/write the same on-disk format (e.g. a canonical writer struct and a legacy reader struct over the same JSON files), keep their field tags reconciled; if the reader's struct lacks the writer's current key, map the new key onto the legacy field (or fall back when the legacy key is absent) so consumers don't silently lose data.
- **Log/text parsing:** Anchor numeric extraction to a known prefix/suffix; never scan all whitespace-delimited tokens.
- **Loop boundaries:** Always verify the starting index of iteration loops; off-by-one errors (e.g. starting at index 1 instead of 0) silently exclude the first element from aggregations like sums and averages.
- **Predicate logic:** When a function scans a collection for a match, return `true` on the matching condition (`item == target`), not the inverse; an inverted predicate almost always returns the wrong result.
- **Duration units:** `time.Duration` is a nanosecond count and JSON-marshals as integer nanoseconds; a field named `*_ms`/`*_sec` must carry an actual millisecond/second integer (e.g. `d.Milliseconds()`), not a raw `time.Duration`. Never construct `time.Duration(n)` from a value already expressed in ms/sec — that reinterprets `n` as nanoseconds.
- **Sub-unit truncation:** Converting a sub-unit duration to a coarser integer (`int(d.Seconds())`, `int(d.Minutes())`) yields 0 below one unit; guard any denominator derived from such a value (clamp divisor ≥ 1) before percent/ratio/progress math to avoid division by zero.
- **Concurrent field access:** A struct field written by one goroutine (e.g. an HTTP handler) and read by another (e.g. a ticker/poll loop) must be protected by the same mutex on *both* the write and the read path; an unsynchronized shared field trips the race detector and is undefined behavior — guarding only one side is not enough.

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
- Absence from a "pending"/"requested" collection (e.g. `requested_reviewers`) is ambiguous: it means both "never requested" and "already completed". Never treat absence as a single state — distinguish "was requested and is now done" from "was never requested" before reporting completion, or you risk a false "done" when no work ever started.
- Never identify an actor by substring match on its login (e.g. treating any login containing the bot name as the bot); a human login that contains the substring (e.g. `copilotfan`) yields a false positive. Match on an exact identity and, when available, a type discriminator (e.g. require `user.type == "Bot"`), rejecting non-matching types.
- With `MagicMock`, explicitly set every field controlling branching logic (unset attributes are truthy); update fixtures and remove unused imports when settings fields are renamed/removed.
- Retry log denominators must match actual total attempts; keep constant names, comments, and loop bounds mutually consistent.
- When adding or changing argument/flag parsing, extend the parser's test table with a case asserting the new form is consumed into its target and removed from the leftover args (e.g. assert `--pr 42` sets the PR and leaves `rest` empty).

### Structured Text & Marker Validation
- Validate paired delimiters (e.g. `<!-- BEGIN:X -->` / `<!-- END:X -->`) as exact full lines using `grep -Fxc`; never substring-match (false positives on comments/examples). Assert each appears exactly once AND BEGIN precedes END.
- When a script writes identical content to multiple files, add a post-write comparison (`cmp -s <(extract A) <(extract B)`) and fail loudly on divergence. Never use `$()` string equality for section comparison — bash strips trailing newlines; use `cmp -s` instead.
- On validation failure, revert affected files (`git checkout -- <file>`) and abort; never continue with a corrupt section.
- After any agent/subprocess run that may modify tracked files, assert pre-existing files still exist; revert and abort if missing.
- After any agent/subprocess run, re-assert the full filesystem invariant of critical files — not just existence but also file type (e.g. symlink vs regular file); revert and abort if the type changed unexpectedly.

### Repository Hygiene
- Never commit local tool/agent runtime artifacts (e.g. session-continuation JSON, scratch state written during a run); they create noisy diffs and merge conflicts for other contributors.
- Any directory generated at runtime by a tool or agent must be added to `.gitignore` so its contents are never accidentally staged; verify with `git status` before committing that no generated run-state paths are tracked.

<!-- END:COPILOT-RULES -->
