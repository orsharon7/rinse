# AGENTS

Project instructions for AI coding agents.

<!-- BEGIN:COPILOT-RULES -->
## Coding Guidelines (AI-maintained)
*Auto-updated by pr-review-reflect — do not edit this section manually.*
*Last updated: 2026-04-12 from PR #22 review*

### Shell Scripting
- Read interactive terminal input from `/dev/tty`, never stderr; render UI output to stderr.
- Validate numeric parameters are ≥ 1 before use as divisors; clamp display-width subtractions to ≥ 0 (`${var:offset:length}` with a negative length slices from the end).
- Verify CLI flag syntax — boolean flags are not interchangeable with `--flag=value`; always pass an explicit `--repo`/scope flag to tools like `gh`, never rely on ambient inference.
- Use `tput sc`/`tput rc` for cursor repositioning; never hard-code row/column values.
- When `--no-interactive` is set, skip all prompts entirely; never fall back to a blocking alternative.
- Use `grep -E` for alternation; `\|` in BRE is non-portable across BSD/macOS and GNU grep.
- Never use `local` outside a function body (invalid under `set -e`) or unbalanced `$(( ))`.
- In heredoc-generated wrappers, embed resolved absolute paths directly; `sed` cannot resolve outer-scope shell variables.
- All output paths of a status-accepting function (`true`/`false`/`skip`) must reflect that status visually; never silently discard a failure/skip signal.

### Environment & CI Portability
- Check both git identity roles: `GIT_AUTHOR_NAME`/`GIT_AUTHOR_EMAIL` and `GIT_COMMITTER_NAME`/`GIT_COMMITTER_EMAIL`; missing one can pass preflight while `git commit` still fails.
- When preflight supports multiple config sources, check all of them and keep error messages in sync with what is actually checked.
- Validate required environment variables before constructing paths or commands; return a clear error rather than silently producing an invalid path.

### Documentation Integrity
- Keep README file trees, artifact references, and documented prerequisites (`go.mod`/`package.json`, tool versions) in sync with the repo; remove stale references when files are added, renamed, or deleted.
- UI labels, log messages, and docstrings must exactly match the behavior performed: never describe a side effect the code doesn't perform, mark a parameter required only if always required, never say "skips silently" if the function logs or emits.
- Phrase design-document assertions as intent, not fact; implementations may diverge.

### CLI, Installers & Packaging
- For optional parameters, default to empty and include the flag only when the user provides a value; never silently pin a value the user intended to omit.
- Installer wrappers referencing helper files by absolute path must bundle those files or document that the source repo must stay at its original path.

### TUI & Layout
- Use a single shared predicate for detecting the same logical event; never duplicate format-detection logic.
- When a panel is hidden, layout-guard and render-guard must agree: never subtract its width from the layout and never route output to it without another render path.
- Clamp computed widget dimensions to ≥ 0 before passing to a rendering library; apply the terminal-width fallback only when uninitialized (`<= 0`), never over a legitimately small positive value.
- Document helper-function return-value semantics (inner vs. outer width) in comments; callers apply border/padding at the call site. Keep layout-constant comments in sync with the actual value; scope input routing and focus gating to the active interaction mode.
- When trimming a separator, account for all visual/encoding variants (e.g. ASCII `|` and box-drawing `│`).

### UI & State Management
- Persist final item state on the data object (e.g. `finalStatus`); never derive display state from a mutable run-scoped map. Apply streaming-derived styling only to actively streaming items; use persisted state for all others.
- Normalize internal/legacy identifiers to canonical user-facing labels before rendering in chips, badges, or labels.

### Go
- **Performance:** Use `strings.Builder` for incremental string construction; never `+=` in a loop (O(n²)).
- **Error handling:** Never call `os.Exit()` inside a UI framework lifecycle (e.g. Bubble Tea); return errors to `main()`. Prefer `strings.Cut()` over `strings.Index()` to avoid `-1` slice panics. Always check `scanner.Err()` after a `bufio.Scanner` loop.
- **Safety:** Never embed non-copy-safe types (`strings.Builder`, `sync.Mutex`) by value in frequently-copied structs; use a pointer.
- **Concurrency:** Drain all data channels before acting on a done-channel signal; never let a `select` race drop buffered output.
- **Paths & Unicode:** Use `filepath.Dir()`/`filepath.Join()` for paths; use rune-aware truncation for user-visible strings, never byte-index slicing.
- **Module hygiene:** Run `go mod tidy` before committing; directly-imported packages must not be marked `// indirect`. Ensure every `-X pkg.Symbol=value` LDFLAGS symbol is declared as a `var` in the target package.

### Python
- **Aggregates:** Guard boolean aggregates against the empty-collection case (e.g. `all_failed = bool(collection)`).
- **Contracts:** If a docstring asserts "Never raises", wrap every code path — including pre-`try` operations — in exception handling.
- **Warnings:** Use a narrowly scoped `"ignore"` filter with a precise `message` regex; a broad `"always"` filter re-enables warnings globally.
- **Serialization:** Join multi-line protocol frames (e.g. SSE `data:` lines) with the spec-mandated separator (`"\n"`), not an empty string.
- **Import hygiene:** When removing a symbol from a module-level import, grep the entire file for remaining usages first; `NameError` only surfaces at runtime.
- **Structured string parsing:** When splitting a structured string format (e.g. ARM resource IDs), locate named segments by searching for the segment key rather than assuming a fixed positional index; always guard against malformed input and return a clear error or skip instead of raising.

### Configuration & Cloud Resources
- Never expose a config setting (env var, config key, documented option) that is not wired to runtime behavior; verify end-to-end: read → validate → pass to the relevant constructor.
- In typed settings models (e.g. Pydantic `BaseSettings`), declare every env-var-backed field explicitly; `getattr` with a fallback silently bypasses the schema.
- Never infer a cloud resource's subscription/resource group from an unrelated resource's metadata; prefer explicit config (e.g. `AZURE_SEARCH_RESOURCE_ID`) — cross-resource-group inference will silently 404.

### Frontend & Accessibility
- Build a lookup map before render loops; never use `Array.find()` (O(n)) inside a loop over the same collection.
- Give every interactive control an explicit accessible name: `aria-label` on icon-only buttons and all form controls (input, textarea, select); `title` and placeholder text are not substitutes.
- Never apply animations via inline `style` attributes — `@media (prefers-reduced-motion: reduce)` cannot override them. Use CSS classes and include a `prefers-reduced-motion` block.
- Trigger scroll/layout side-effects (e.g. `scrollDown()`) after DOM mutations, not before.
- Never cap a dynamically-sized container with fixed `max-height` + `overflow: hidden`; use a viewport-relative cap (e.g. `min(80vh, 1400px)`) with `overflow-y: auto`.
- Use CSS `:hover` or class toggles for hover effects, not `onMouseEnter`/`onMouseLeave` style mutations.
- Never combine a percentage `width` with a fixed pixel `height` on an SVG; omit the explicit height and let CSS/`aspect-ratio` control it, or use explicit pixel dimensions.
- Never call browser-only APIs (`window`, `document`, `matchMedia`, etc.) during render in SSR contexts; compute such values in a `useEffect` so the initial client render matches server output and avoids hydration mismatches.
- Feature-detect Web APIs before calling them unconditionally; older browsers may only support legacy equivalents (e.g. `MediaQueryList.addListener` vs `addEventListener`) — check for the modern API's presence first and fall back to the legacy form.

### API & Type Contracts
- When a backend enum or union type gains new values or legacy aliases, update all mirrored client-side type definitions (e.g. TypeScript unions) in the same change.

### Testing
- When patching settings/config with `MagicMock`, explicitly set every field controlling branching logic; unset mock attributes are truthy and silently force unintended code paths.
- When a settings field is renamed or removed, update all test fixtures referencing the old name; stale assignments silently mask regressions. Remove unused imports from test files.

### Logging & Observability
- Retry log messages must use a denominator matching actual total attempts; distinguish "attempt N of M" (total) from "retry N of M" (retries-only).

<!-- END:COPILOT-RULES -->
