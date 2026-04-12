# AGENTS

Project instructions for AI coding agents.

<!-- BEGIN:COPILOT-RULES -->
## Coding Guidelines (AI-maintained)
*Auto-updated by pr-review-reflect — do not edit this section manually.*
*Last updated: 2026-04-12 from PR #23 review*

### Shell Scripting
- Read interactive input from `/dev/tty`, never stderr; render UI output to stderr.
- Validate numeric parameters are ≥ 1 before use as divisors; clamp display-width subtractions to ≥ 0.
- Verify CLI flag syntax — boolean flags are not interchangeable with `--flag=value`; always pass an explicit `--repo`/scope flag to `gh`, never rely on ambient inference.
- Use `tput sc`/`tput rc` for cursor repositioning; never hard-code row/column values.
- When `--no-interactive` is set, skip all prompts entirely; never fall back to a blocking alternative.
- Use `grep -E` for alternation; `\|` in BRE is non-portable across BSD/macOS and GNU grep.
- Never use `local` outside a function body or unbalanced `$(( ))`. In heredoc wrappers, embed resolved absolute paths directly.
- All output paths of a status-accepting function (`true`/`false`/`skip`) must reflect that status visually; never silently discard a failure/skip signal.

### Environment & CI Portability
- Check both git identity pairs: `GIT_AUTHOR_NAME`/`EMAIL` and `GIT_COMMITTER_NAME`/`EMAIL`; missing one can pass preflight while `git commit` still fails.
- Validate all required env vars before constructing paths or commands; keep preflight error messages in sync with what is actually checked.

### Documentation Integrity
- Keep README file trees, artifact references, and prerequisites (`go.mod`/`package.json`, tool versions) in sync with the repo; remove stale references on rename/delete.
- Labels, log messages, and docstrings must match actual behavior: never describe a side effect the code doesn't perform; never say "skips silently" if the function logs or emits.
- Document serialization formats and SDK call paths exactly as implemented; never carry over assumptions from prior designs or reference methods the code doesn't call.
- When documenting SDK calls in any file (docs, diagrams, README, code comments), match enum member names (including casing) and argument names/shapes exactly to the implementation; a mismatched enum variant or wrong argument name in an example causes silent copy/paste breakage.
- Phrase design-document assertions as intent, not fact.
- Keep architecture and data-flow diagrams in sync with actual SDK/API call paths; when an implementation changes its call site, update every diagram that references it in the same PR.
- When a document section contradicts another (e.g. "Project Context" claims a framework that another section says is not used), reconcile them by verifying against actual imports and removing or qualifying the inaccurate claim.

### CLI, Installers & Packaging
- Optional parameters default to empty; include the flag only when the user provides a value — never silently pin an omitted value.
- Installer wrappers referencing helpers by absolute path must bundle those files or document the source-repo path dependency.

### TUI & Layout
- Use a single shared predicate per logical event; never duplicate format-detection logic.
- When a panel is hidden, layout-guard and render-guard must agree: don't subtract its width from layout and don't route output to it without another render path.
- Clamp widget dimensions to ≥ 0; apply terminal-width fallback only when uninitialized (`<= 0`). Account for all separator variants (ASCII `|` and box-drawing `│`) when trimming.
- Document helper return-value semantics (inner vs. outer width); callers apply border/padding at the call site. Scope input routing and focus gating to the active interaction mode.

### UI & State Management
- Persist final item state on the data object (e.g. `finalStatus`); never derive display state from a mutable run-scoped map. Apply streaming styling only to actively streaming items; use persisted state for all others.
- Normalize internal/legacy identifiers to canonical labels before rendering in chips or badges.
- In multi-axis execution models (e.g. sequential steps × agents-per-step), gate streaming finalization on all axes — `stepIdx > 0` alone misses agent transitions within step 0.

### Go
- **Performance:** Use `strings.Builder` for string construction; never `+=` in a loop (O(n²)).
- **Error handling:** Never call `os.Exit()` inside a UI framework lifecycle; return errors to `main()`. Prefer `strings.Cut()` over `strings.Index()`. Always check `scanner.Err()` after a `bufio.Scanner` loop.
- **Safety:** Never embed non-copy-safe types (`strings.Builder`, `sync.Mutex`) by value in frequently-copied structs; use a pointer. Drain data channels before acting on a done-channel signal.
- **Paths & Unicode:** Use `filepath.Dir()`/`filepath.Join()`; rune-aware truncation for user-visible strings, never byte-index slicing.
- **Module hygiene:** Run `go mod tidy` before committing; direct imports must not be `// indirect`. Every `-X pkg.Symbol=value` LDFLAGS symbol must be declared as a `var`.

### Python
- **Safety:** Guard boolean aggregates against the empty-collection case (`all_failed = bool(collection)`). If a docstring asserts "Never raises", wrap every code path including pre-`try` operations.
- **Streams & serialization:** Close async streams in a `try/finally` via `await stream.aclose()` when not using a context manager. Join multi-line protocol frames with the spec-mandated separator (`"\n"`), not an empty string.
- **Imports & parsing:** Before removing a module-level import symbol, grep the file for remaining usages. When parsing structured strings (e.g. ARM IDs), locate segments by key, not fixed index; guard against malformed input.
- **Warnings:** Use a narrowly scoped `"ignore"` filter with a precise `message` regex; a broad `"always"` filter re-enables warnings globally.

### Configuration & Cloud Resources
- Never expose a config setting not wired to runtime behavior; verify end-to-end: read → validate → pass to constructor.
- In typed settings models (e.g. Pydantic `BaseSettings`), declare every env-var-backed field explicitly; `getattr` with a fallback silently bypasses the schema.
- Never infer a cloud resource's subscription/resource group from an unrelated resource; prefer explicit config (e.g. `AZURE_SEARCH_RESOURCE_ID`) — cross-resource-group inference will silently 404.

### Frontend & Accessibility
- Build a lookup map before render loops; never use `Array.find()` (O(n)) inside a loop.
- Give every interactive control an explicit `aria-label`; `title` and `placeholder` are not substitutes.
- Never apply animations via inline `style` — `@media (prefers-reduced-motion)` cannot override them. Use CSS classes.
- Trigger scroll/layout side-effects after DOM mutations, not before.
- Use viewport-relative caps (e.g. `min(80vh, 1400px)`) with `overflow-y: auto`; never fixed `max-height` + `overflow: hidden`.
- Use CSS `:hover` or class toggles for hover effects, not `onMouseEnter`/`onMouseLeave` mutations.
- Never combine a percentage `width` with a fixed pixel `height` on an SVG; use `aspect-ratio` or explicit pixel dimensions.
- Never call browser-only APIs (`window`, `document`, `matchMedia`) during SSR render; compute in `useEffect`. Feature-detect before calling modern Web APIs; fall back to legacy equivalents (e.g. `addListener`).

### API, Testing & Observability
- When a backend enum gains new values or aliases, update all mirrored client-side type definitions in the same change.
- When patching config with `MagicMock`, explicitly set every field controlling branching logic; unset attributes are truthy. When a settings field is renamed/removed, update all test fixtures and remove unused imports.
- Retry log messages must use a denominator matching actual total attempts; keep constant names, comments, and loop bounds mutually consistent (e.g. `range(MAX_RETRIES + 1)` → comment "total attempts").

<!-- END:COPILOT-RULES -->
