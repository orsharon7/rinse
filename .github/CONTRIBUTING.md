# Contributing to RINSE

## GitHub Label System

RINSE uses a structured label system. All labels are managed via the repo and the `pr-review-session.sh` script.

### RINSE Workflow Labels

| Label | Color | Description |
|-------|-------|-------------|
| `rinse:running` | `#8B5CF6` | RINSE is actively reviewing this PR — do not merge |
| `rinse:approved` | `#10B981` | Copilot approved — ready to merge |
| `rinse:needs-work` | `#F59E0B` | RINSE cycle found issues |

### Component Labels

| Label | Color | Description |
|-------|-------|-------------|
| `cli` | `#0075ca` | Command-line interface |
| `db` | `#0052CC` | Database |
| `docs` | `#0075ca` | Documentation |
| `legal` | `#cfd3d7` | Legal |
| `brand` | `#8B5CF6` | Brand |
| `engine` | `#0075ca` | RINSE engine/runner scripts |
| `tui` | `#1d76db` | RINSE TUI application |

### Standard Labels

| Label | Color | Description |
|-------|-------|-------------|
| `bug` | `#d73a4a` | Something isn't working |
| `documentation` | `#0075ca` | Improvements or additions to documentation |
| `duplicate` | `#cfd3d7` | This issue or pull request already exists |
| `enhancement` | `#a2eeef` | New feature or request |
| `help wanted` | `#008672` | Extra attention is needed |
| `good first issue` | `#7057ff` | Good for newcomers |
| `invalid` | `#e4e669` | This doesn't seem right |
| `wontfix` | `#ffffff` | This will not be worked on |

### Priority Labels

| Label | Color | Description |
|-------|-------|-------------|
| `p0-critical` | `#b60205` | Must-have for launch |
| `p1-important` | `#d93f0b` | High priority |
| `p2-nice` | `#e4e669` | Nice to have |

### Milestone Labels

| Label | Color | Description |
|-------|-------|-------------|
| `milestone:v0.2` | `#c5def5` | Target: v0.2 release |
| `milestone:v0.3` | `#c5def5` | Target: v0.3 release |
| `milestone:v1.0` | `#c5def5` | Target: v1.0 release |

## PR Review Flow

When RINSE is actively reviewing a PR, `rinse:running` is applied automatically by `pr-review-session.sh`. Do not merge a PR while this label is present. Upon completion, the label is removed and one of `rinse:approved` or `rinse:needs-work` reflects the outcome.
