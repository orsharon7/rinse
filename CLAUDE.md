# CLAUDE

Project instructions for AI coding agents.

<!-- BEGIN:COPILOT-RULES -->
## Coding Guidelines (AI-maintained)
*Auto-updated by pr-review-reflect — do not edit this section manually.*
*Last updated: 2026-04-13 from PR #12 review*

### Shell Scripting
- Read interactive input from `/dev/tty`; render UI output to stderr.
- Validate numeric parameters are ≥ 1 before use as divisors; clamp display-width subtractions to ≥ 0.
- Pass an explicit `--repo`/scope flag to `gh`; never rely on ambient inference. Boolean flags are not interchangeable with `--flag=value`.
- Use `tput sc`/`tput rc` for cursor repositioning; never hard-code row/column values.
- When `--no-interactive` is set, skip all prompts — never fall back to a blocking alternative.
- Use `grep -E` for alternation; `\|` in BRE is non-portable across BSD/macOS and GNU grep.
- Never use `local` outside a function body or unbalanced `$(( ))`. Embed resolved absolute paths directly in heredoc wrappers.
- Every output path of a status-accepting function (`true`/`false`/`skip`) must reflect that status visually; never silently discard it.
- Validate shell script syntax with `bash -n`/`sh -n` before committing; a stray brace or unbalanced delimiter silently prevents the entire script from executing.
- When piping a function through `tee -a "$LOGFILE"`, suppress or redirect the function's internal `log()` calls to avoid writing each line twice to the logfile.

### Environment & CI Portability
- Check both git identity pairs (`GIT_AUTHOR_NAME`/`EMAIL` and `GIT_COMMITTER_NAME`/`EMAIL`); missing one can pass preflight while `git commit` still fails.
- Validate all required env vars before constructing paths or commands; keep preflight error messages in sync with what is actually checked.

### Documentation Integrity
- Keep README file trees, artifact references, and prerequisites (`go.mod`/`package.json`, tool versions) in sync with the repo; remove stale references on rename/delete.
- Descriptions must match behavior: never say "skips silently" if the function logs; never reference methods or side effects the code doesn't perform.
- Match enum member names (including casing), argument names, and code-example field names/function signatures exactly to the implementation — mismatches cause silent copy-paste breakage.
- Keep architecture diagrams and ADRs in sync with actual SDK/API call paths in the same PR. ADRs must account for all runtime dispatch paths; carve out legitimate exceptions (e.g. platform-managed orchestrators) explicitly.
- Reconcile contradicting document sections against actual imports; phrase design assertions as intent, not fact.

### CLI, Installers & Packaging
- Optional parameters default to empty; include the flag only when the user provides a value.
- Installer wrappers using absolute paths must bundle those helpers or document the dependency.

### TUI & Layout
- Use a single shared predicate per logical event; never duplicate format-detection logic.
- Hidden panels: layout-guard and render-guard must agree — don't subtract width from layout without a matching render path.
- Clamp widget dimensions to ≥ 0; apply terminal-width fallback only when uninitialized (`<= 0`). Account for all separator variants (ASCII `|` and box-drawing `│`) when trimming.
- Document helper return-value semantics (inner vs. outer width); apply border/padding at the call site. Gate input routing and focus to the active interaction mode.

### UI & State Management
- Persist final item state on the data object (e.g. `finalStatus`); never derive display state from a mutable run-scoped map. Apply streaming styling only to actively streaming items.
- Normalize internal/legacy identifiers to canonical labels before rendering.
- In multi-axis execution (sequential steps × agents-per-step), gate streaming finalization on all axes — `stepIdx > 0` alone misses agent transitions within step 0.
- Never hard-code a UI value that mirrors a backend configurable setting; source it from the backend payload or settings endpoint.

### Go
- **Performance:** Use `strings.Builder`; never `+=` in a loop (O(n²)).
- **Error handling:** Return errors to `main()` — never `os.Exit()` inside a UI lifecycle. Prefer `strings.Cut()` over `strings.Index()`. Always check `scanner.Err()` after a `bufio.Scanner` loop.
- **Safety:** Use pointers for non-copy-safe types (`strings.Builder`, `sync.Mutex`) in frequently-copied structs. Drain data channels before acting on a done-channel signal.
- **Paths & Unicode:** Use `filepath.Dir()`/`filepath.Join()`; rune-aware truncation for user-visible strings.
- **Module hygiene:** Run `go mod tidy` before committing; direct imports must not be `// indirect`. Every `-X pkg.Symbol=value` LDFLAGS symbol must be declared as a `var`.
- **Dead code:** Never declare package-level variables or identifiers that are unreferenced in code; Go will refuse to compile.
- **Config scoping:** Initialize per-resource fields from the most-specific config scope first (e.g. per-repo); fall back to global/last-run values only when no scoped value exists.
- **Config presence:** Use an explicit presence flag (e.g. the `ok` return from a map lookup or loader) to detect missing per-scope config; never treat a zero value as "unset" — `0`, `false`, and `""` are all valid explicit choices.
- **Config override:** When a scoped config entry exists, use its values verbatim; never combine with globals via `||`/`OR` — a global `true` must not override a scoped `false`.
- **Config persistence:** Never persist a config field without also reading it back and applying it at load time; remove unused persisted fields rather than leaving misleading dead config.
- **State sequencing:** Parse structured input into its canonical representation and update mutable state before deriving any values from it; never read a field before the parsing step that updates it.
- **Map writes:** Guard map writes against empty/zero-value keys; validate that the key is non-empty before writing to avoid creating phantom entries.

### Python
- **Safety & initialization:** Guard boolean aggregates against empty collections (`all_failed = bool(collection)`). Initialize all closure-captured accumulator variables before the first iteration that reads them. If a docstring asserts "Never raises", wrap all code paths including pre-`try` operations.
- **Streams & serialization:** Close async streams via `await stream.aclose()` in `try/finally`. Join protocol frames with the spec-mandated separator (`"\n"`). Verify serializer options match docstring claims (e.g. `exclude_none=True` when "field omitted when None").
- **Imports & parsing:** Grep for remaining usages before removing an import. Parse structured strings (e.g. ARM IDs) by key, not fixed index; guard against malformed input.
- **Dependency compatibility:** Guard removed third-party symbols with `try/except ImportError`; raise a descriptive `RuntimeError` at call-time. Reconcile pinned versions with imported symbols so tests don't stub away a runtime `ImportError`.
- **Dead code & warnings:** Remove overwritten variable assignments before merging. Use a narrowly scoped `"ignore"` filter with a precise `message` regex — a broad `"always"` filter re-enables warnings globally.

### Configuration & Cloud Resources
- Never expose a config setting not wired to runtime behavior; verify end-to-end: read → validate → pass to constructor.
- In typed settings models (e.g. Pydantic `BaseSettings`), declare every env-var-backed field explicitly; `getattr` with a fallback silently bypasses the schema.
- Never infer a cloud resource's subscription/resource group from an unrelated resource; use explicit config (e.g. `AZURE_SEARCH_RESOURCE_ID`) — cross-resource-group inference will silently 404.

### Frontend & Accessibility
- Build a lookup map before render loops; never use `Array.find()` (O(n)) inside a loop.
- Give every interactive control an explicit `aria-label`; `title` and `placeholder` are not substitutes.
- Use CSS classes for animations (not inline `style`) and `:hover`/class toggles for hover effects (not `onMouseEnter`/`onMouseLeave`); use design tokens or Tailwind classes for colors (not inline hex).
- Trigger scroll/layout side-effects after DOM mutations, not before.
- Use `overflow-y: auto` with viewport-relative caps (e.g. `min(80vh, 1400px)`); never fixed `max-height` + `overflow: hidden`.
- Never combine percentage `width` with fixed pixel `height` on an SVG; use `aspect-ratio` or explicit pixel dimensions.
- Never call browser-only APIs (`window`, `document`, `matchMedia`) during SSR; compute in `useEffect`. Feature-detect modern Web APIs before calling; fall back to legacy equivalents.
- In `useEffect`, verify SSE/event payload runtime types before property access; never access object properties on a value typed as a string without a type guard.

### API, Testing & Observability
- When a backend enum gains values/aliases, update all mirrored client-side type definitions in the same change.
- With `MagicMock`, explicitly set every field controlling branching logic (unset attributes are truthy); update fixtures and remove unused imports when settings fields are renamed/removed.
- Retry log denominators must match actual total attempts; keep constant names, comments, and loop bounds mutually consistent.

<!-- END:COPILOT-RULES -->
