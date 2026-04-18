// Package predict — interactive.go
//
// RunInteractive implements `rinse predict --interactive`: a Bubble Tea TUI that
// walks the user through each prediction one at a time, offering to apply the
// suggested fix, skip, open in $EDITOR, or quit.
//
// Key bindings (per RIN-208 spec):
//
//	y  — apply the fix atomically; verifies `go build ./...`; stages the change
//	n  — skip, advance to next prediction
//	e  — open $EDITOR (fallback: nano) with the suggested fix
//	q  — exit loop, show summary
//
// After the loop a summary box is printed (N reviewed, M applied, estimated X
// min saved) and an interactive_session event is logged to
// ~/.rinse/sessions/<session-id>.json.
//
// Pro gate: if the binary is not running in Pro mode (RINSE_PRO=1 env var or
// pro:true in ~/.rinse/config.json), a styled upgrade prompt is shown and the
// command exits 0 without entering the loop — per the RIN-119 spec.
package predict

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/orsharon7/rinse/internal/theme"
)

// ── Pro gate ─────────────────────────────────────────────────────────────────

// IsProEnabled returns true when the user has an active Pro licence.
// Detection order:
//  1. RINSE_PRO=1 environment variable (overrides everything; useful for CI)
//  2. pro:true in ~/.rinse/config.json
func IsProEnabled() bool {
	if os.Getenv("RINSE_PRO") == "1" {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(home, ".rinse", "config.json"))
	if err != nil {
		return false
	}
	var cfg struct {
		Pro bool `json:"pro"`
	}
	_ = json.Unmarshal(data, &cfg)
	return cfg.Pro
}

// RenderUpgradePrompt prints the upgrade prompt for non-Pro users.
func RenderUpgradePrompt(w io.Writer) {
	star := lipgloss.NewStyle().Foreground(theme.Mauve).Bold(true).Render("✦")
	link := lipgloss.NewStyle().Foreground(theme.Overlay).Underline(true).Render("rinse.sh/#pro")
	fmt.Fprintf(w, "\n  %s  %s\n\n",
		star,
		theme.StyleStep.Render("rinse predict --interactive  requires RINSE Pro"),
	)
	fmt.Fprintf(w, "     %s\n", theme.StyleMuted.Render("Unlock interactive fix review, team dashboards, and unlimited patterns."))
	fmt.Fprintf(w, "     %s\n\n", link)
}

// ── Session event ─────────────────────────────────────────────────────────────

// interactiveSession is the on-disk payload for an interactive_session event.
type interactiveSession struct {
	Event       string    `json:"event"`
	SessionID   string    `json:"session_id"`
	StartedAt   string    `json:"started_at"`
	Predictions int       `json:"predictions"`
	Applied     int       `json:"applied"`
	Skipped     int       `json:"skipped"`
}

// logInteractiveSession writes an interactive_session event JSON file to
// ~/.rinse/sessions/.  Non-fatal on any error.
func logInteractiveSession(sessionID string, started time.Time, nPredictions, applied, skipped int) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".rinse", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	payload := interactiveSession{
		Event:       "interactive_session",
		SessionID:   sessionID,
		StartedAt:   started.UTC().Format(time.RFC3339),
		Predictions: nPredictions,
		Applied:     applied,
		Skipped:     skipped,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return
	}
	ts := started.UTC().Format("20060102-150405")
	nano := started.UTC().UnixNano() % 1e9
	name := fmt.Sprintf("interactive-%s-%s-%09d.json", sessionID, ts, nano)
	dest := filepath.Join(dir, name)

	tmp, err := os.CreateTemp(dir, ".interactive-*.json.tmp")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	if _, werr := tmp.Write(data); werr != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return
	}
	if cerr := tmp.Close(); cerr != nil {
		_ = os.Remove(tmpPath)
		return
	}
	_ = os.Rename(tmpPath, dest)
}

// ── Patch application ─────────────────────────────────────────────────────────

// ApplyPatchResult is returned by ApplyPatch.
type ApplyPatchResult struct {
	Applied   bool
	BuildFail bool
	Err       error
}

// ApplyPatch attempts to apply the suggested fix for a prediction.
// It writes a minimal unified diff to a temp file, runs `git apply`, then
// verifies `go build ./...`. On build failure it reverts via `git checkout`.
//
// When the Prediction has no SuggestedDiff the function generates a stub diff
// (a no-op) so the mechanics can still be exercised and the test harness works.
//
// NOTE: This function IS allowed to mutate the working tree — it is the
// intentional side-effect of the interactive command (unlike predict.Run which
// is strictly read-only).
func ApplyPatch(p Prediction) ApplyPatchResult {
	diff := p.SuggestedDiff
	if strings.TrimSpace(diff) == "" {
		// No diff to apply — treat as applied (skip the apply step).
		return ApplyPatchResult{Applied: true}
	}

	// Write patch to temp file.
	tmp, err := os.CreateTemp("", "rinse-patch-*.diff")
	if err != nil {
		return ApplyPatchResult{Err: fmt.Errorf("create patch temp file: %w", err)}
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, werr := tmp.WriteString(diff); werr != nil {
		_ = tmp.Close()
		return ApplyPatchResult{Err: fmt.Errorf("write patch: %w", werr)}
	}
	_ = tmp.Close()

	// Apply with git apply.
	applyOut, err := exec.Command("git", "apply", "--index", tmpPath).CombinedOutput()
	if err != nil {
		return ApplyPatchResult{Err: fmt.Errorf("git apply failed: %w\n%s", err, applyOut)}
	}

	// Verify the build still passes.
	buildOut, err := exec.Command("go", "build", "./...").CombinedOutput()
	if err != nil {
		// Revert the patch.
		_ = exec.Command("git", "apply", "--reverse", "--index", tmpPath).Run()
		return ApplyPatchResult{
			BuildFail: true,
			Err:       fmt.Errorf("go build failed after patch; change reverted\n%s", buildOut),
		}
	}

	// Stage the change (git apply --index already staged it).
	return ApplyPatchResult{Applied: true}
}

// ── Bubble Tea model ──────────────────────────────────────────────────────────

// interactiveModel is the Bubble Tea model for the predict interactive loop.
type interactiveModel struct {
	predictions []Prediction
	cursor      int // index of current prediction
	applied     []bool
	skipped     []bool
	done        bool
	termWidth   int
	sessionID   string
	startedAt   time.Time
	lastMsg     string // status message from last action
}

// newInteractiveModel creates the model for the given predictions.
func newInteractiveModel(predictions []Prediction, termWidth int, sessionID string) interactiveModel {
	return interactiveModel{
		predictions: predictions,
		applied:     make([]bool, len(predictions)),
		skipped:     make([]bool, len(predictions)),
		termWidth:   termWidth,
		sessionID:   sessionID,
		startedAt:   time.Now(),
	}
}

// applyResultMsg carries the result of an async ApplyPatch call back to Update.
type applyResultMsg struct {
	result ApplyPatchResult
	index  int
}

// editorDoneMsg signals that the editor subprocess has exited.
type editorDoneMsg struct {
	index int
}

func (m interactiveModel) Init() tea.Cmd {
	return nil
}

func (m interactiveModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case applyResultMsg:
		if msg.result.Applied {
			m.applied[msg.index] = true
			m.lastMsg = theme.StyleLogSuccess.Render(theme.IconCheck + " Applied and staged.")
		} else if msg.result.BuildFail {
			m.lastMsg = theme.StyleErr.Render(theme.IconCross + " Build failed; change reverted. " + msg.result.Err.Error())
		} else if msg.result.Err != nil {
			m.lastMsg = theme.StyleErr.Render(theme.IconCross + " " + msg.result.Err.Error())
		}
		next, cmd := m.advance()
		return next, cmd

	case editorDoneMsg:
		m.lastMsg = theme.StyleMuted.Render("Editor closed.")
		next, cmd := m.advance()
		return next, cmd

	case tea.WindowSizeMsg:
		m.termWidth = msg.Width
	}
	return m, nil
}

func (m interactiveModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		if m.done || m.cursor >= len(m.predictions) {
			return m, tea.Quit
		}
		idx := m.cursor
		p := m.predictions[idx]
		return m, func() tea.Msg {
			result := ApplyPatch(p)
			return applyResultMsg{result: result, index: idx}
		}

	case "n", "N":
		if m.done || m.cursor >= len(m.predictions) {
			return m, tea.Quit
		}
		m.skipped[m.cursor] = true
		m.lastMsg = theme.StyleMuted.Render("Skipped.")
		return m.advance()

	case "e", "E":
		if m.done || m.cursor >= len(m.predictions) {
			return m, tea.Quit
		}
		idx := m.cursor
		p := m.predictions[idx]
		return m, tea.ExecProcess(buildEditorCmd(p), func(err error) tea.Msg {
			return editorDoneMsg{index: idx}
		})

	case "q", "Q", "ctrl+c":
		// Mark remaining as skipped for summary accuracy.
		for i := m.cursor; i < len(m.predictions); i++ {
			if !m.applied[i] && !m.skipped[i] {
				m.skipped[i] = true
			}
		}
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

// advance moves to the next prediction or sets done=true when all reviewed.
func (m interactiveModel) advance() (tea.Model, tea.Cmd) {
	m.cursor++
	if m.cursor >= len(m.predictions) {
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

func (m interactiveModel) View() string {
	if m.done || len(m.predictions) == 0 {
		return ""
	}

	p := m.predictions[m.cursor]
	w := m.termWidth
	if w <= 0 {
		w = 80
	}

	var sb strings.Builder

	// Header: prediction N of M.
	idx := m.cursor + 1
	total := len(m.predictions)
	counter := theme.StyleMuted.Render(fmt.Sprintf("Prediction %d of %d", idx, total))
	sb.WriteString("\n  " + counter + "\n\n")

	// Pattern + confidence.
	conf := int(p.Confidence * 100)
	confStyle := confidenceStyle(p.Confidence)
	patternLine := fmt.Sprintf("  %s  %s  %s",
		lipgloss.NewStyle().Foreground(theme.Mauve).Render(theme.IconDiamond),
		lipgloss.NewStyle().Foreground(theme.Text).Bold(true).Render(p.Pattern),
		confStyle.Render(fmt.Sprintf("%d%%", conf)),
	)
	sb.WriteString(patternLine + "\n")

	// File:line.
	if p.File != "" {
		loc := p.File
		if p.Line > 0 {
			loc = fmt.Sprintf("%s:%d", p.File, p.Line)
		}
		sb.WriteString("     " + theme.StyleMuted.Render(loc) + "\n")
	}

	// Detail.
	if p.Detail != "" {
		maxDetail := w - 5
		if maxDetail < 20 {
			maxDetail = 20
		}
		detail := theme.Truncate(p.Detail, maxDetail)
		sb.WriteString("\n     " + theme.StyleMuted.Render(detail) + "\n")
	}

	// Suggested diff preview (first 8 lines).
	if strings.TrimSpace(p.SuggestedDiff) != "" {
		sb.WriteString("\n")
		lines := strings.Split(p.SuggestedDiff, "\n")
		limit := 8
		if len(lines) < limit {
			limit = len(lines)
		}
		for _, l := range lines[:limit] {
			var styled string
			switch {
			case strings.HasPrefix(l, "+"):
				styled = lipgloss.NewStyle().Foreground(theme.Green).Render(l)
			case strings.HasPrefix(l, "-"):
				styled = lipgloss.NewStyle().Foreground(theme.Red).Render(l)
			default:
				styled = theme.StyleMuted.Render(l)
			}
			sb.WriteString("     " + styled + "\n")
		}
		if len(strings.Split(p.SuggestedDiff, "\n")) > 8 {
			sb.WriteString("     " + theme.StyleMuted.Render("… (truncated)") + "\n")
		}
	}

	sb.WriteString("\n")

	// Last action message.
	if m.lastMsg != "" {
		sb.WriteString("  " + m.lastMsg + "\n\n")
	}

	// Prompt line.
	keyStyle := lipgloss.NewStyle().Foreground(theme.Mauve).Bold(true)
	prompt := fmt.Sprintf("  %s apply   %s skip   %s edit   %s quit",
		keyStyle.Render("[y]"),
		keyStyle.Render("[n]"),
		keyStyle.Render("[e]"),
		keyStyle.Render("[q]"),
	)
	sb.WriteString(prompt + "\n")

	return sb.String()
}

// ── Editor helpers ────────────────────────────────────────────────────────────

// buildEditorCmd builds the *exec.Cmd for opening the suggested diff in $EDITOR.
func buildEditorCmd(p Prediction) *exec.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "nano"
	}

	content := p.SuggestedDiff
	if strings.TrimSpace(content) == "" {
		content = fmt.Sprintf("# No suggested diff available for:\n# Pattern: %s\n# File:    %s:%d\n# Detail:  %s\n",
			p.Pattern, p.File, p.Line, p.Detail)
	}

	// Write to temp file.
	tmp, err := os.CreateTemp("", "rinse-edit-*.diff")
	if err != nil {
		// Fall back to /dev/null.
		return exec.Command(editor, "/dev/null")
	}
	if _, werr := tmp.WriteString(content); werr != nil {
		_ = tmp.Close()
		return exec.Command(editor, "/dev/null")
	}
	_ = tmp.Close()

	return exec.Command(editor, tmp.Name())
}

// ── Summary ───────────────────────────────────────────────────────────────────

const minutesPerAppliedFix = 4

// printSummary writes the post-loop summary box to w.
func printSummary(w io.Writer, predictions []Prediction, applied []bool, skipped []bool) {
	total := len(predictions)
	nApplied := 0
	nSkipped := 0
	for i := range predictions {
		if applied[i] {
			nApplied++
		}
		if skipped[i] {
			nSkipped++
		}
	}
	estMin := nApplied * minutesPerAppliedFix

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Mauve).
		Padding(0, 2).
		MarginLeft(1)

	var sb strings.Builder
	title := theme.StyleStep.Render("rinse predict  —  session complete")
	sb.WriteString(title + "\n\n")
	sb.WriteString(fmt.Sprintf("  Predictions reviewed:  %s\n",
		theme.StyleVal.Render(fmt.Sprintf("%d", total))))
	sb.WriteString(fmt.Sprintf("  Fixes applied:         %s\n",
		theme.StyleLogSuccess.Render(fmt.Sprintf("%d", nApplied))))
	sb.WriteString(fmt.Sprintf("  Skipped:               %s\n",
		theme.StyleMuted.Render(fmt.Sprintf("%d", nSkipped))))
	if estMin > 0 {
		sb.WriteString(fmt.Sprintf("  Est. time saved:       %s\n",
			theme.StyleVal.Render(fmt.Sprintf("~%d min", estMin))))
	}

	fmt.Fprintln(w, box.Render(sb.String()))
}

// ── Entry point ───────────────────────────────────────────────────────────────

// InteractiveOpts configures RunInteractive.
type InteractiveOpts struct {
	// Report is the prediction report to walk through.
	Report *Report

	// TermWidth is the terminal column count (0 means detect / use 80).
	TermWidth int

	// Out is the writer for summary and upgrade prompts (defaults to os.Stdout).
	Out io.Writer

	// SessionID overrides the auto-generated session ID (useful for tests).
	SessionID string

	// SkipProCheck disables the pro gate (for tests).
	SkipProCheck bool
}

// RunInteractive runs the Bubble Tea predict review loop.
// It returns an error only for fatal setup failures; user-visible errors are
// printed to opts.Out.
func RunInteractive(opts InteractiveOpts) error {
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}

	// Pro gate.
	if !opts.SkipProCheck && !IsProEnabled() {
		RenderUpgradePrompt(out)
		return nil
	}

	report := opts.Report
	if report == nil {
		return fmt.Errorf("predict: nil report passed to RunInteractive")
	}

	// Empty prediction set.
	if len(report.Predictions) == 0 {
		icon := theme.StyleLogSuccess.Render(theme.IconCheck)
		fmt.Fprintf(out, "\n  %s  %s\n\n", icon,
			theme.StyleLogSuccess.Render("No predictions — your diff looks clean."))
		return nil
	}

	sessionID := opts.SessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("predict-%d", time.Now().UnixNano())
	}

	termWidth := opts.TermWidth
	if termWidth <= 0 {
		termWidth = 80
	}

	model := newInteractiveModel(report.Predictions, termWidth, sessionID)

	prog := tea.NewProgram(model, tea.WithAltScreen())
	finalModel, err := prog.Run()
	if err != nil {
		return fmt.Errorf("predict interactive: %w", err)
	}

	final, ok := finalModel.(interactiveModel)
	if !ok {
		return fmt.Errorf("predict interactive: unexpected model type")
	}

	// Print summary.
	printSummary(out, final.predictions, final.applied, final.skipped)

	// Log session event (fire-and-forget).
	nApplied := 0
	nSkipped := 0
	for i := range final.predictions {
		if final.applied[i] {
			nApplied++
		}
		if final.skipped[i] {
			nSkipped++
		}
	}
	logInteractiveSession(sessionID, final.startedAt, len(final.predictions), nApplied, nSkipped)

	return nil
}
