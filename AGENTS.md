# AGENTS

Project instructions for AI coding agents.

<!-- BEGIN:COPILOT-RULES -->
## Coding Guidelines (AI-maintained)
*Auto-updated by pr-review-reflect — do not edit this section manually.*
*Last updated: 2026-04-10 from PR #7 review*

### Shell Scripting
- Always read interactive terminal input (keypresses, menus) from `/dev/tty`, never from stderr (`&2`); render UI output to stderr.

### Environment & CI Portability
- When performing git operations that require user identity, add a preflight check for `user.name`/`user.email` with a clear error message, or accept identity overrides via environment variables.

### Documentation Integrity
- Keep README directory trees and file references in sync with actual repository contents; if a path is user-created at runtime, document it as such rather than listing it as a committed file.

<!-- END:COPILOT-RULES -->

