# UX Spec: Post-Cycle Summary Screen

**Status:** Draft  
**Owner:** UX Designer (RINSE)  
**Scope:** `internal/session/session.go` — `PrintSummary()` function  
**Priority:** High — this is the last thing a user sees after every cycle

---

## What exists today

`PrintSummary()` already prints a styled summary to stdout after every successful cycle.
Current output (reconstructed from code):

```
  ✓ RINSE  PR #42  approved

  Time saved          ~12 min
  Comments fixed      3 in 1 round
  Iterations          2
  Top patterns        unused variable, missing nil check, ...
```

This is good. The gaps are:

1. **No visual hierarchy** — time saved (the value prop) is buried at the same weight as iterations.
2. **No wall-clock elapsed shown** — users want to know "how long did this run?"
3. **No quality signal** — approved in 1 iter vs 5 iters feels very different; we should reflect that.
4. **No "what's next" prompt** — after an approved cycle, the user is just dropped back to the shell.
5. **Iteration breakdown is too raw** — `"3 across 2 rounds (2, 1)"` is cryptic.

---

## Redesigned layout

### Approved cycle (happy path)

```
                                              ← blank line
  ✓ RINSE  PR #42  orsharon7/my-repo  approved  4m 32s
                                              ← blank line
  ┌─────────────────────────────────────────────────────┐
  │  ✦  3 comments fixed  ·  ~12 min saved             │
  │     2 rounds: 2 → 1                                 │
  └─────────────────────────────────────────────────────┘
                                              ← blank line
  Rules learned      +2
  Iterations         2
                                              ← blank line
  Top patterns
    • unused variable assignment
    • missing nil check on err
                                              ← blank line
  $ rinse history   to browse past cycles
                                              ← blank line
```

### Not-approved cycle (max iterations reached)

```
  ○ RINSE  PR #42  orsharon7/my-repo  complete  6m 11s
                                              ← blank line
  ┌─────────────────────────────────────────────────────┐
  │  ⚠  3 comments fixed, PR not yet approved          │
  └─────────────────────────────────────────────────────┘
                                              ← blank line
  Iterations         4 (max reached)
  Remaining          ~2 comments open
                                              ← blank line
  $ rinse run --pr 42   to resume
                                              ← blank line
```

---

## Exact strings and colors

All colors via `internal/theme/theme.go` tokens — no inline hex.

| Element | Style token | String |
|---|---|---|
| Title check icon | `theme.StyleLogSuccess` | `✓` |
| Title "RINSE" | `theme.GradientString(…, Mauve, Lavender, true)` | `RINSE` |
| PR badge | `theme.StylePRNum` | `PR #<n>` |
| Repo | `theme.StyleMuted` | `<org>/<repo>` |
| "approved" label | `theme.StyleLogSuccess` | `approved` |
| "complete" label | `theme.StyleMuted` | `complete` |
| Elapsed | `theme.StyleMuted` | `<Xs>` or `<Xm Ys>` |
| Hero box border | `lipgloss.RoundedBorder()`, `theme.Green` (approved) / `theme.Yellow` (not approved) | — |
| Hero icon (approved) | `theme.StyleLogSuccess` | `✦` |
| Hero icon (warn) | `theme.StylePhaseStalled` | `⚠` |
| Hero text (main) | `theme.StyleVal` | `<N> comments fixed  ·  ~<M> min saved` |
| Hero text (rounds) | `theme.StyleMuted` | `<N> rounds: <a> → <b>` (only if >1 round) |
| Row key | `theme.StyleKey.Copy().Width(18)` | label text |
| Row value | `theme.StyleVal` | value text |
| Top patterns header | `theme.StyleKey` | `Top patterns` |
| Pattern bullet | `theme.StyleMuted` | `  •  <text>` |
| "what's next" hint | `theme.StyleTeal` | `$ rinse history` or `$ rinse run --pr <n>` |
| Hint suffix | `theme.StyleMuted` | `  to browse past cycles` |

---

## Formatting rules

- **Box width**: `min(termWidth-4, 56)`. Default `52` when term width unknown.
- **Elapsed format**: `formatElapsed()` already exists in `monitor.go` — reuse it in `session.go`.
- **Rounds breakdown**: only show if `len(s.CommentsByRound) > 1`. Single-round: omit.
- **Rules learned**: only show row if `s.RulesExtracted > 0`.
- **Top patterns**: cap at 3. Each on its own line with `•` bullet. Truncate to 50 chars.
- **"What's next"**: always show one hint line. Approved → `rinse history`. Not approved → `rinse run --pr <n>`.
- **NO_COLOR / dumb terminal**: skip box entirely; fall back to existing plain-text style (already guarded in monitor.go flow).

---

## Implementation notes

- `PrintSummary` signature stays the same: `PrintSummary(s Session, jsonMode bool)`.
- Add a `termWidth int` parameter OR read `os.Getenv("COLUMNS")` with fallback to 80.
  Preferred: add optional width via a new `PrintSummaryOpts` struct to avoid breaking callers.
- Move `formatElapsed` to a shared `internal/tui/format.go` or inline it in `session.go`.
- The hero box is the only new rendering element. All other rows already exist — just reorder and conditionally suppress.

---

## Edge cases

| Scenario | Behaviour |
|---|---|
| 0 comments fixed | Hero text: `0 comments fixed` (muted, no time-saved) |
| 0 iterations | Iterations row: `—` |
| No patterns | Omit top patterns section entirely |
| Term width < 40 | Skip box, use plain two-line output |
| `jsonMode=true` | No change — JSON path bypasses all rendering |
| `NO_COLOR` set | Skip box and gradient; plain text only |
