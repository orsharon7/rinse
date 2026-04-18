// Package predict — render.go
//
// Render produces the styled terminal output for `rinse predict`.
// It is intentionally not a Bubble Tea TUI — pure fmt.Fprint to stdout.
//
// Signature:
//
//	Render(w io.Writer, report *Report, termWidth int)
//
// The caller is responsible for detecting terminal width and passing it.
// When termWidth < 60, the confidence column is omitted (narrow terminal rule).
// When NO_COLOR is set or stdout is not a TTY, Lip Gloss degrades to plain text
// automatically. A structured dumb-terminal fallback ([PREDICT] prefix) is emitted
// when the renderer detects dumb-terminal mode.
package predict

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/orsharon7/rinse/internal/theme"
)

// ── Layout constants ──────────────────────────────────────────────────────────

const (
	// confidenceColumn is the target column (0-indexed) where the confidence
	// percentage is right-aligned. Must be ≥ narrowTermWidth.
	confidenceColumn = 64

	// narrowTermWidth is the threshold below which the confidence column is dropped.
	narrowTermWidth = 60

	// maxPatternLen is the max visible rune width for the pattern+detail field
	// before truncation with an ellipsis.
	maxPatternLen = 48
)

// ── Confidence styling ────────────────────────────────────────────────────────

// confidenceStyle returns the Lip Gloss style for a confidence value in [0,1].
func confidenceStyle(c float64) lipgloss.Style {
	pct := int(c * 100)
	switch {
	case pct >= 80:
		return lipgloss.NewStyle().Foreground(theme.Green).Bold(true)
	case pct >= 60:
		return lipgloss.NewStyle().Foreground(theme.Yellow)
	default:
		return lipgloss.NewStyle().Foreground(theme.Red)
	}
}

// ── isDumb detects dumb/plain terminal environments ───────────────────────────

func isDumb() bool {
	if os.Getenv("NO_COLOR") != "" {
		return true
	}
	t := os.Getenv("TERM")
	return t == "dumb" || t == ""
}

// ── Render ────────────────────────────────────────────────────────────────────

// Render writes the styled predict output to w.
//
//   - termWidth is the terminal column count (use 80 as a safe default).
//   - When isDumb() is true, the output falls back to plain [PREDICT] prefix lines.
func Render(w io.Writer, report *Report, termWidth int) {
	if isDumb() {
		renderDumb(w, report)
		return
	}
	renderStyled(w, report, termWidth)
}

// ── Styled output ─────────────────────────────────────────────────────────────

func renderStyled(w io.Writer, report *Report, termWidth int) {
	preds := report.Predictions
	narrow := termWidth < narrowTermWidth

	switch {
	case len(preds) == 0 && strings.TrimSpace(report.Source) == "staged changes" &&
		report.Source == "staged changes":
		// Distinguish "nothing staged" vs "staged but clean" by checking
		// Source — Run() sets Source to "staged changes" in both cases, but
		// the diff is empty when nothing is staged.
		// We detect "nothing to scan" via a sentinel Source set by Run when
		// the diff is empty (handled below in the empty-source path).
		renderClean(w)
	case len(preds) == 0:
		renderClean(w)
	default:
		renderPredictions(w, preds, narrow, termWidth)
	}
}

// renderEmpty handles the "no staged diff / no PR" case.
// This is called from the CLI layer when Run returns an empty report with
// Source == "".
func renderEmpty(w io.Writer) {
	icon := theme.StyleMuted.Render(theme.IconCircle)
	header := theme.StyleMuted.Render("rinse predict  —  nothing to analyze")
	fmt.Fprintf(w, "%s  %s\n", icon, header)
	fmt.Fprintf(w, "   %s\n", theme.StyleMuted.Render("Stage changes or pass --pr <N> --repo <owner/repo>"))
}

// renderClean handles the "zero predictions" case.
func renderClean(w io.Writer) {
	icon := theme.StyleLogSuccess.Render(theme.IconCheck)
	header := theme.StyleLogSuccess.Render("rinse predict  —  no likely issues detected")
	fmt.Fprintf(w, "%s  %s\n", icon, header)
	fmt.Fprintf(w, "   %s\n", theme.StyleMuted.Render("Diff looks clean. Good to submit."))
}

// renderPredictions handles the standard case with ≥1 predictions.
func renderPredictions(w io.Writer, preds []Prediction, narrow bool, termWidth int) {
	count := len(preds)

	// Header line.
	icon := theme.StyleStep.Render(theme.IconDiamond)
	header := theme.StyleStep.Render("rinse predict")
	suffix := theme.StyleMuted.Render(fmt.Sprintf("—  %d likely Copilot comment%s detected",
		count, plural(count)))
	fmt.Fprintf(w, "%s  %s  %s\n\n", icon, header, suffix)

	// Each prediction row.
	for _, p := range preds {
		renderPredictionRow(w, p, narrow, termWidth)
		fmt.Fprintln(w) // blank line between items
	}

	// Footer CTA.
	countStr := theme.StyleStep.Render(fmt.Sprintf("%d prediction%s", count, plural(count)))
	hint := theme.StyleMuted.Render("• run `rinse` to fix automatically")
	fmt.Fprintf(w, "   %s %s\n", countStr, hint)
}

// renderPredictionRow writes a single two-line prediction item.
func renderPredictionRow(w io.Writer, p Prediction, narrow bool, termWidth int) {
	icon := lipgloss.NewStyle().Foreground(theme.Mauve).Render(theme.IconDiamond)

	// Pattern + detail — truncate if needed.
	desc := p.Pattern
	if p.Detail != "" {
		desc = p.Pattern + ": " + p.Detail
	}
	desc = theme.Truncate(desc, maxPatternLen)
	descRendered := lipgloss.NewStyle().Foreground(theme.Text).Render(desc)

	if narrow {
		// Narrow: icon + description only.
		fmt.Fprintf(w, "  %s %s\n", icon, descRendered)
	} else {
		// Wide: pad description to confidenceColumn, then right-align confidence.
		pct := int(p.Confidence * 100)
		confStr := fmt.Sprintf("%d%%", pct)
		confRendered := confidenceStyle(p.Confidence).Render(confStr)

		// Compute padding between description and confidence column.
		descPlain := lipgloss.Width(descRendered)
		prefix := lipgloss.Width("  " + icon + " ") // "  ◇ "
		usedCols := prefix + descPlain
		pad := confidenceColumn - usedCols
		if pad < 1 {
			pad = 1
		}
		fmt.Fprintf(w, "  %s %s%s%s\n", icon, descRendered, strings.Repeat(" ", pad), confRendered)
	}

	// Second line: file path + line number.
	if p.File != "" {
		loc := p.File
		if p.Line > 0 {
			loc = fmt.Sprintf("%s:%d", p.File, p.Line)
		}
		loc = theme.Truncate(loc, 40)
		locRendered := theme.StyleMuted.Render(loc)
		fmt.Fprintf(w, "   %s\n", locRendered)
	}
}

// ── Dumb terminal fallback ────────────────────────────────────────────────────

func renderDumb(w io.Writer, report *Report) {
	preds := report.Predictions
	if len(preds) == 0 {
		fmt.Fprintf(w, "[rinse predict] no predictions. Diff looks clean.\n")
		return
	}
	fmt.Fprintf(w, "[rinse predict] %d prediction%s found:\n", len(preds), plural(len(preds)))
	for _, p := range preds {
		desc := p.Pattern
		if p.Detail != "" {
			desc = p.Pattern + ": " + p.Detail
		}
		pct := int(p.Confidence * 100)
		loc := p.File
		if p.Line > 0 {
			loc = fmt.Sprintf("%s:%d", loc, p.Line)
		}
		if loc != "" {
			fmt.Fprintf(w, "  [PREDICT] %s (%s) (%d%%)\n", desc, loc, pct)
		} else {
			fmt.Fprintf(w, "  [PREDICT] %s (%d%%)\n", desc, pct)
		}
	}
	fmt.Fprintln(w, "Run `rinse` to fix automatically.")
}

// ── RenderError ───────────────────────────────────────────────────────────────

// RenderError writes a styled error line (exit 1 path).
func RenderError(w io.Writer, err error) {
	if isDumb() {
		fmt.Fprintf(w, "[rinse predict] error: %v\n", err)
		return
	}
	line := fmt.Sprintf("%s  rinse predict failed: %v",
		theme.StyleErr.Render(theme.IconCross), err)
	fmt.Fprintln(w, theme.StyleErr.Render(line))
}

// RenderEmpty wraps renderEmpty for external callers (cli.go).
func RenderEmpty(w io.Writer) {
	if isDumb() {
		fmt.Fprintln(w, "[rinse predict] nothing to analyze. Stage changes or pass --pr <N> --repo <owner/repo>")
		return
	}
	renderEmpty(w)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
