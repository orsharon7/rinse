# AGENTS

Project instructions for AI coding agents.

<!-- BEGIN:COPILOT-RULES -->
## Coding Guidelines (AI-maintained)
*Auto-updated by pr-review-reflect — do not edit this section manually.*
*Last updated: 2026-04-14 from PR #16 review*

### Shell Scripting
- Read interactive input from `/dev/tty`; render UI output to stderr.
- Validate numeric parameters are ≥ 1 before use as divisors; clamp display-width subtractions to ≥ 0. Validate all numeric CLI parameters (including optional ones like `--stagger`) as integers ≥ 0 before use in arithmetic or `sleep`.
- Pass an explicit `--repo`/scope flag to `gh`; never rely on ambient inference. Boolean flags are not interchangeable with `--flag=value`.
- Use `tput sc`/`tput rc` for cursor repositioning; never hard-code row/column values.
- Honor `--no-interactive`: skip all prompts, never fall back to a blocking alternative.
- Use `grep -E` for alternation; `\|` in BRE is non-portable across BSD/macOS and GNU.
- Never use `local` outside a function body; never leave `$(( ))` unbalanced. Embed resolved absolute paths directly in heredoc wrappers.
- Every status-accepting function (`true`/`false`/`skip`) must reflect that status visually; never silently discard it.
- Validate syntax with `bash -n`/`sh -n` before committing.
- When piping through `tee -a "$LOGFILE"`, suppress internal `log()` calls to avoid double-writing.
- Retry pipelines with `bash -c 'set -euo pipefail; … | tee …'`; without `pipefail`, exit status is `tee`'s, masking upstream failures.
- Never swallow git branch/checkout failures with `|| true`; use `checkout -B <branch> origin/<branch>` and `--set-upstream-to`, and treat failure as fatal.
- Never `eval` a space-joined command string; build a Bash array and execute it directly to avoid word-splitting and injection issues.
- Never redirect a subprocess's output to the same logfile the subprocess writes internally; redirect to `/dev/null` or a separate log to avoid double-logging.
- Keep `--help`/usage comment blocks in sync with actual arg-parsing; update documented flags whenever options are added or removed.
- Use atomic lock primitives (`mkdir`-based lock directory or `noclobber` redirection) for cross-process mutual exclusion; never rely on racy pidfile checks (`-f` test followed by write).
- Capture dynamically constructed paths (e.g. per-PR log file paths) into variables at creation time and reference those variables consistently; never fall back to a hardcoded legacy path in error-reporting paths.

### Environment & CI Portability
- Check both git identity pairs (`GIT_AUTHOR_NAME`/`EMAIL` and `GIT_COMMITTER_NAME`/`EMAIL`); a missing pair can pass preflight but fail `git commit`.
- Validate all required env vars before constructing paths/commands; keep preflight error messages in sync with what is checked.
- Avoid `declare -A` (associative arrays) without a Bash 4+ version check at startup; macOS system `/bin/bash` is typically 3.2 and will fail silently or abort.

### Documentation Integrity
- Keep README file trees, artifact references, and prerequisites (`go.mod`/`package.json`, tool versions) in sync; remove stale references on rename/delete.
- Descriptions must match behavior exactly: no false "skips silently", no phantom methods/side-effects.
- Match enum names (including casing), argument names, and code-example signatures exactly to the implementation — mismatches cause silent copy-paste breakage.
- Keep architecture diagrams and ADRs in sync with actual SDK/API call paths in the same PR; account for all dispatch paths and explicitly carve out legitimate exceptions.
- Reconcile contradicting doc sections against actual imports; phrase design assertions as intent, not fact.

### CLI, Installers & Packaging
- Optional parameters default to empty; include the flag only when the user provides a value.
- Installer wrappers using absolute paths must bundle those helpers or document the dependency.

### TUI & Layout
- Use a single shared predicate per logical event; never duplicate format-detection logic.
- Layout-guard and render-guard must agree: don't subtract width without a matching render path. Clamp widget dimensions to ≥ 0; apply terminal-width fallback only when uninitialized (`<= 0`). Account for all separator variants (ASCII `|` and box-drawing `│`) when trimming.
- Document helper return-value semantics (inner vs. outer width); apply border/padding at the call site. Gate input routing and focus to the active interaction mode.
- Use the currently selected/active item (not a hardcoded index `[0]`) when resolving paths or scripts.

### UI & State Management
- Persist final item state on the data object (e.g. `finalStatus`); never derive display state from a mutable run-scoped map. Apply streaming styling only to actively streaming items.
- Normalize internal/legacy identifiers to canonical labels before rendering.
- In multi-axis execution (sequential steps × agents-per-step), gate streaming finalization on all axes — `stepIdx > 0` alone misses agent transitions within step 0.
- Never hard-code a UI value that mirrors a backend configurable setting; source it from the backend payload.
- Gate phase-scoped state resets on an actual phase transition (`nextPhase != currentPhase`); never clear state on incidental log-line content.
- Track "user has edited this field" with an explicit boolean; never use a sentinel value (e.g. `"main"`) as a proxy for "unmodified" — it may be a valid input.

### Go
- **Performance:** Use `strings.Builder`; never `+=` in a loop. Pre-compute repeated expressions once before a loop.
- **Error handling:** Return errors to `main()` — never `os.Exit()` inside a UI lifecycle. Prefer `strings.Cut()` over `strings.Index()`. Always check `scanner.Err()` after a `bufio.Scanner` loop.
- **Safety:** Use pointers for non-copy-safe types (`strings.Builder`, `sync.Mutex`) in frequently-copied structs. Drain data channels before acting on a done-channel signal.
- **Paths & Unicode:** Use `filepath.Dir()`/`filepath.Join()`; rune-aware truncation for user-visible strings.
- **Module hygiene:** Run `go mod tidy` before committing; direct imports must not be `// indirect`. Every `-X pkg.Symbol=value` LDFLAGS symbol must be declared as a `var`. Never declare unreferenced package-level identifiers.
- **Config:** Initialize fields from the most-specific scope first (per-repo before global). Use an explicit presence flag (`ok` from map lookup) — never treat zero values as "unset". Use scoped values verbatim; never merge with globals via `||`. Never persist a field without reading it back at load time; verify persisted paths match the current working context before use.
- **State & maps:** Parse input into canonical form and update mutable state before deriving values from it. Guard map writes against empty keys.
- **Log/text parsing:** Anchor numeric extraction to a known prefix/suffix (sentinel emoji or keyword); never scan all whitespace-delimited tokens.

### Python
- **Safety & initialization:** Guard boolean aggregates against empty collections. Initialize closure-captured accumulators before first use. If a docstring says "Never raises", wrap all code paths including pre-`try` operations.
- **Streams & serialization:** Close async streams via `await stream.aclose()` in `try/finally`. Join protocol frames with the spec-mandated separator. Verify serializer options match docstring claims.
- **Imports, parsing & deps:** Grep for remaining usages before removing an import. Parse structured strings (e.g. ARM IDs) by key, not fixed index. Guard removed third-party symbols with `try/except ImportError`; raise a descriptive `RuntimeError` at call-time. Reconcile pinned versions with imported symbols.
- **Dead code & warnings:** Remove overwritten variable assignments. Use narrowly scoped `"ignore"` filters with a precise `message` regex — broad `"always"` re-enables warnings globally.

### Configuration & Cloud Resources
- Never expose a config setting not wired to runtime behavior; verify end-to-end: read → validate → pass to constructor.
- In typed settings models (e.g. Pydantic `BaseSettings`), declare every env-var-backed field explicitly; `getattr` with a fallback silently bypasses the schema.
- Never infer a cloud resource's subscription/resource group from an unrelated resource; use explicit config (e.g. `AZURE_SEARCH_RESOURCE_ID`) — cross-resource-group inference will silently 404.

### Frontend & Accessibility
- Build a lookup map before render loops; never use `Array.find()` (O(n)) inside a loop.
- Give every interactive control an explicit `aria-label`; `title` and `placeholder` are not substitutes.
- Use CSS classes for animations and `:hover` effects (not inline `style` or `onMouseEnter`/`onMouseLeave`); use design tokens or Tailwind for colors (not inline hex).
- Trigger scroll/layout side-effects after DOM mutations, not before.
- Use `overflow-y: auto` with viewport-relative caps; never fixed `max-height` + `overflow: hidden`. Never combine percentage `width` with fixed pixel `height` on an SVG; use `aspect-ratio`.
- Never call browser-only APIs (`window`, `document`, `matchMedia`) during SSR; feature-detect and fall back to legacy equivalents. In `useEffect`, verify SSE/event payload runtime types before property access.

### API, Testing & Observability
- When a backend enum gains values/aliases, update all mirrored client-side type definitions in the same change.
- With `MagicMock`, explicitly set every field controlling branching logic (unset attributes are truthy); update fixtures and remove unused imports when settings fields are renamed/removed.
- Retry log denominators must match actual total attempts; keep constant names, comments, and loop bounds mutually consistent.

<!-- END:COPILOT-RULES -->
