# CLAUDE

Project instructions for AI coding agents.

<!-- BEGIN:COPILOT-RULES -->
## Coding Guidelines (AI-maintained)
*Auto-updated by pr-review-reflect — do not edit this section manually.*
*Last updated: 2026-04-12 from PR #22 review*

### Shell Scripting
- Read interactive terminal input from `/dev/tty`, never stderr; render UI output to stderr.
- Validate numeric parameters are ≥ 1 before use as divisors; never assume caller-supplied values are safe for arithmetic.
- Verify CLI flag syntax against the tool's spec — boolean flags (presence/absence) are not interchangeable with `--flag=value`; test before shipping.
- Pass an explicit `--repo` (or equivalent scope flag) to CLI tools like `gh`; never rely on ambient directory inference.
- Use `tput sc`/`tput rc` around temporary cursor repositioning; never restore to hard-coded row/column values.
- When `--no-interactive` is set, skip all prompts entirely; never fall back to a still-blocking alternative.
- Clamp display-width subtractions to a minimum of 0; negative lengths in `${var:offset:length}` slice from the end.
- Use `grep -E` for alternation patterns; `\|` in BRE is non-portable across BSD/macOS and GNU grep.
- Never use `local` outside a function body (invalid at top-level under `set -e`) or unbalanced `$(( ))` (syntax error).
- In heredoc-generated wrapper scripts, embed resolved absolute paths directly; `sed` cannot resolve shell variables from the outer scope.
- All output paths of a status-accepting function (`true`/`false`/`skip`) must reflect that status visually; never silently discard a failure/skip signal.

### Environment & CI Portability
- For git operations requiring identity, check both author (`GIT_AUTHOR_NAME`/`GIT_AUTHOR_EMAIL`) and committer (`GIT_COMMITTER_NAME`/`GIT_COMMITTER_EMAIL`) identity; a preflight that checks only one role can pass while `git commit` still fails.
- When a preflight supports multiple config sources, check all of them and keep error messages in sync with what is actually checked.
- Validate required environment variables before constructing paths or commands; return a clear error rather than silently producing an invalid path.

### Documentation Integrity
- Keep README file trees, artifact references, and documented prerequisites (tool versions, `go.mod`/`package.json`) in sync with actual repository contents and module manifests; update or remove stale references when files are added, renamed, or deleted.
- UI labels, log messages, and menu text must exactly match the behavior performed; never describe a side effect unless the corresponding code is present.
- Phrase implementation-scope assertions in design documents as intent, not fact; the actual implementation may diverge.

### CLI, Installers & Packaging
- For optional parameters, default the prompt to empty and only include the flag when the user provides a non-empty value; never silently pin a value the user intended to omit.
- Installer-generated wrappers that reference helper files by absolute path must either bundle those files or document that the source repo must remain at its original path.

### TUI & Layout
- Use a single shared predicate for detecting the same logical event across log/output formats; never duplicate format-detection logic.
- When a panel is hidden, both layout-guard and render-guard logic must agree; never subtract a hidden panel's width from layout calculations.
- Always clamp computed widget dimensions to ≥ 0 before passing to a rendering library; apply the terminal-width fallback only when the value is uninitialized (`<= 0`), never over a legitimately small positive value.
- Guard log/output routing to a conditional panel on whether the panel is actually visible; never silently discard output with no other render path.
- Document helper-function return-value semantics (inner vs. outer width) in comments; callers must apply border/padding adjustments at the call site.
- Keep layout-constant comments in sync with the actual constant value; scope input routing and focus gating to the currently active interaction mode.
- When trimming a separator, account for all visual/encoding variants (e.g. ASCII `|` and box-drawing `│`).

### UI & State Management
- Persist final item state on the data object itself (e.g. a `finalStatus` field); never derive display state from a mutable run-scoped map that resets on each run.
- Apply streaming-derived styling only to actively streaming items; use persisted state for all others.
- Normalize internal/legacy identifiers to canonical user-facing labels before rendering in chips, badges, or labels.

### Go
- **Performance:** Use `strings.Builder` for incremental string construction; never use `+=` in a loop (O(n²)).
- **Error handling:** Never call `os.Exit()` inside a UI framework lifecycle (e.g. Bubble Tea); return errors to `main()`. Prefer `strings.Cut()` over `strings.Index()` to avoid `-1` slice panics. Always check `scanner.Err()` after a `bufio.Scanner` loop.
- **Safety:** Never embed non-copy-safe types (`strings.Builder`, `sync.Mutex`) by value in frequently-copied structs (e.g. Bubble Tea models); use a pointer instead.
- **Concurrency:** Drain all data channels before acting on a done-channel signal; never let a `select` race drop buffered output.
- **Paths & Unicode:** Use `filepath.Dir()`/`filepath.Join()` for path derivation. Use rune-aware truncation for user-visible strings; never slice by byte index.
- **Module hygiene:** Run `go mod tidy` before committing; directly-imported packages must not be marked `// indirect`.
- **Linker:** Ensure every `-X pkg.Symbol=value` LDFLAGS symbol is declared as a `var` in the target package.

### Python
- **Aggregates:** Guard boolean aggregates over collections against the empty-collection case (e.g. `all_failed = bool(collection)`); never assume a loop input is non-empty.
- **Contracts:** If a function's docstring asserts "Never raises", wrap every code path — including pre-`try` operations — in exception handling.
- **Warnings:** Use a narrowly scoped `"ignore"` filter with a precise `message` regex; a broad `"always"` filter re-enables warnings globally.
- **Serialization:** Join multi-line protocol frames (e.g. SSE `data:` lines) with the spec-mandated separator (`"\n"`), not an empty string.

### Configuration Integrity
- Never expose a config setting (env var, config key, documented option) that is not wired to runtime behavior; remove or connect it.
- Verify new config options end-to-end: read → validate → pass to the relevant constructor or call site.
- In typed settings models (e.g. Pydantic `BaseSettings`), declare every env-var-backed field explicitly; `getattr` with a fallback silently bypasses the schema.

### Cloud Resource Configuration
- Never infer a cloud resource's subscription/resource group from an unrelated resource's metadata; prefer explicit config (e.g. `AZURE_SEARCH_RESOURCE_ID`) and warn on fallback inference, since cross-resource-group deployments will silently 404.

### Frontend & Accessibility
- Build a lookup map before render loops; never use `Array.find()` (O(n)) inside a loop over the same collection.
- Add `aria-label` to all icon-only buttons; `title` alone is not a reliable accessible name.
- Include a `@media (prefers-reduced-motion: reduce)` block that disables CSS animations/transitions.
- Never apply animations via inline `style` attributes; CSS `@media (prefers-reduced-motion)` rules cannot override inline styles, so motion-sensitive users will not be protected. Use CSS classes or check `window.matchMedia('(prefers-reduced-motion: reduce)')` at render time instead.
- Trigger scroll/layout side-effects (e.g. `scrollDown()`) after DOM mutations that append new nodes, not before; elements appended after a scroll call land off-screen until the next interaction.

### Documentation Integrity (Functions & Docstrings)
- Docstrings must accurately reflect which parameters are truly required vs. optional and what side-effects occur (e.g. logging, emitting events); never describe a parameter as required if the function only conditionally uses it, and never say "skips silently" if the function logs or emits status messages.

<!-- END:COPILOT-RULES -->

