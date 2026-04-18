# UX Spec: `.rinseignore` in Onboarding Wizard

**Status:** Audit complete  
**Owner:** UX Designer (RINSE)  
**Scope:** `internal/tui/onboarding_wizard.go` — Step A (intro screen)

---

## Current state

`.rinseignore` is already mentioned in Step A of the onboarding wizard (line 809–811):

```go
tip := theme.StyleMuted.Render("  "+theme.IconDiamond+" Tip  ") +
    theme.StyleTeal.Render(".rinseignore") +
    theme.StyleMuted.Render(" — add this file to exclude paths from your cycles.")
```

Rendered output:
```
  ♦ Tip  .rinseignore — add this file to exclude paths from your cycles.
```

This is **fine as a mention** — it's discoverable on first run.

---

## Gaps

### 1. No explanation of *what* to put in `.rinseignore`

The tip tells users the file exists and what it does, but not what format it uses. A new user reading this has no idea if it's gitignore-style globs, exact paths, or something else.

### 2. Not mentioned again during setup

The wizard has a "Repo path" step (Step B-area) where the user sets their working directory. That's the ideal moment to reinforce `.rinseignore` — once you've picked a repo, you might immediately want to exclude certain paths.

### 3. Not surfaced in `rinse init` completion screen

After `rinse init` finishes and creates `.rinse.json`, there's no hint about `.rinseignore`. A user setting up for the first time would benefit from seeing it mentioned then.

---

## Recommended changes

### A. Improve the wizard tip (Step A) — minor copy change

**Current:**
```
  ♦ Tip  .rinseignore — add this file to exclude paths from your cycles.
```

**New:**
```
  ♦ Tip  .rinseignore — gitignore-style globs to exclude paths from review cycles.
          Create it at your repo root, e.g.:  vendor/  *.generated.go
```

Implementation — update lines 809–812 in `onboarding_wizard.go`:

```go
tip := theme.StyleMuted.Render("  "+theme.IconDiamond+" Tip  ") +
    theme.StyleTeal.Render(".rinseignore") +
    theme.StyleMuted.Render(" — gitignore-style globs to exclude paths from cycles.\n") +
    theme.StyleMuted.Render("          Create at repo root, e.g.: ") +
    theme.StyleTeal.Render("vendor/  *.generated.go")
```

### B. Add `.rinseignore` hint to `rinse init` completion — `internal/tui/init.go`

After the `✓ Created .rinse.json` success line, add a secondary muted hint:

```
  ✓ Created .rinse.json

    Tip  Add .rinseignore at your repo root to exclude paths (gitignore-style).
```

Exact rendering:

```go
fmt.Println()
fmt.Println(theme.StyleMuted.Render("    Tip  Add ") +
    theme.StyleTeal.Render(".rinseignore") +
    theme.StyleMuted.Render(" at repo root to exclude paths (gitignore-style)."))
```

### C. No change to wizard step B/C/D

The repo path step doesn't need a `.rinseignore` mention — the wizard is already long enough. The init.go hint (change B) covers the post-setup moment adequately.

---

## Priority

- **Change A** (wizard tip copy): Low effort, improves discoverability. Do this.
- **Change B** (init.go hint): Low effort, correct moment in workflow. Do this.
- **Change C**: Skip.

Both changes are 3–5 lines each. Can be done in a single PR.
