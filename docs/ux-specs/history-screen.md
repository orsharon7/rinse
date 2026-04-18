# UX Spec: `rinse history` Screen

**Status:** Audit complete — existing implementation reviewed  
**Owner:** UX Designer (RINSE)  
**Scope:** `internal/tui/history.go`

---

## Assessment: already well-implemented

`history.go` has a full Bubble Tea TUI with three screens (list, detail, filter), Lip Gloss styling, sort modes, outcome filters, and keyboard navigation. This is not a gap area.

### What's good

- List view: sorted sessions with icon (`✓`/`⚠`/`✗`), repo, PR number, date, wall-clock duration, outcome summary on second line
- Detail view: comments fixed, iterations, time saved, rules learned, top patterns, "open in browser" (`o`), "show run command" (`r`)
- Filter panel: repo text search + outcome filter with blinking cursor
- Sort modes: newest/oldest/most comments/most time saved
- Status bar: clear key hints on every screen
- Empty state: handled with a muted message

### Gaps identified

1. **Wall-clock elapsed not shown in list view** — `ElapsedWall()` exists on `Session` but `viewList()` only shows date and iter wall time from `formatElapsed(dur)`. The `dur` value is already computed — this is just a label ambiguity. It does show but the column header is implied. **No action needed.**

2. **No "resume" shortcut in list view** — from the list, there's no `r` to immediately re-run the selected PR. You have to enter detail (`enter`) then press `r`. Minor friction.

3. **Detail view: no elapsed shown** — the detail screen shows time saved (heuristic) but not actual wall-clock elapsed for that session. Could be useful context.

4. **Filter panel: outcome cycle wraps but no label count** — the outcome filter cycles through 4 states with `tab` but there's no `(1 of 4)` or total count hint.

---

## Recommended improvements (non-breaking, low scope)

### 1. Add `r` shortcut in list view to run selected PR directly

In `updateList()`:

```go
case msg.String() == "r":
    if n > 0 {
        s := m.filteredSessions[m.cursor]
        m.detailCmd = fmt.Sprintf("rinse run --pr %s", s.PR)
        m.screen = screenDetail
    }
```

Update status bar hint: add `theme.RenderKeyHint("r", "re-run")`.

### 2. Show elapsed in detail view

In `viewDetail()`, add a row after "Time saved":

```go
elapsed := s.ElapsedWall()
var elapsedVal string
if elapsed == 0 {
    elapsedVal = "—"
} else {
    elapsedVal = formatElapsed(elapsed)
}
// insert after savedStr row:
lines = append(lines, row("Elapsed:", elapsedVal))
```

### 3. Status bar: add count hint in filter panel

In `viewFilterPanel()`:

```go
"  " + theme.StyleMuted.Render(fmt.Sprintf("r=edit repo  tab=cycle outcome (%d/%d)  esc/f=close",
    int(m.filterOutcome)+1, 4))
```

---

## No redesign needed

The overall design is sound. Implement the three small improvements above when bandwidth allows.
