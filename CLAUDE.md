# CLAUDE

Project instructions for AI coding agents.

<!-- BEGIN:COPILOT-RULES -->
## Coding Guidelines (AI-maintained)
*Auto-updated by pr-review-reflect — do not edit this section manually.*
*Last updated: 2026-04-10 from PR #7 review*

### Shell Scripting
- Always read interactive terminal input (keypresses, menus) from `/dev/tty`, never from stderr (`&2`); render UI output to stderr.
- Always validate numeric parameters are within an acceptable range (e.g. ≥ 1) before using them as divisors; never assume a caller-supplied value is safe for arithmetic.
- Always verify CLI flag syntax against the tool's actual specification — boolean flags (enabled by presence/absence) are not interchangeable with `--flag=value` style; test flags before shipping.
- Always pass an explicit `--repo` (or equivalent scope flag) to CLI tools like `gh` rather than relying on ambient directory inference; the inferred context may differ from the user's intended target.
- Use `tput sc`/`tput rc` (or ANSI save/restore cursor `\0337`/`\0338`) around any temporary cursor repositioning; never restore the cursor to hard-coded row/column values.
- When a non-interactive mode flag (e.g. `--no-interactive`) is set, skip all interactive menus and prompts entirely rather than falling back to a still-blocking alternative.
- When computing a display width or substring length by subtracting a prefix/offset, clamp the result to a minimum of 0 before use; negative lengths in bash substring expressions (`${var:offset:length}`) slice from the end rather than yielding an empty string.

### Environment & CI Portability
- When performing git operations that require user identity, add a preflight check for `user.name`/`user.email` with a clear error message, or accept identity overrides via environment variables.
- When a preflight check supports multiple configuration sources (config file, environment variables), check all advertised sources and keep the error message exactly in sync with what is actually checked.

### Documentation Integrity
- Keep README directory trees and file references in sync with actual repository contents; if a path is user-created at runtime, document it as such rather than listing it as a committed file.
- Ensure log messages, menu text, and UI labels exactly match the behavior the code actually performs; never describe a side effect (e.g. "with remote branch deletion") unless the corresponding flag or call is present in the implementation.

### CLI & User Input
- When a parameter is optional (e.g. "leave blank for default"), default its prompt to an empty string and only include the corresponding flag/argument in the command when the user explicitly provides a non-empty value; never use a non-empty default that silently pins a value the user intended to omit.

<!-- END:COPILOT-RULES -->

