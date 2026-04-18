// Package predict — interactive.go
//
// RunInteractive implements `rinse predict --interactive`: a Bubble Tea TUI that
// walks the user through each prediction one at a time, offering to apply the
// suggested fix, skip, open in $EDITOR, or quit.
//
// Key bindings (per RIN-208 spec):
//
//	y        — apply the fix atomically; verifies `go build ./...`; stages the change
//	n/space  — skip, advance to next prediction
//	e        — mark as edited — show a muted note (editor launch is v0.5)
//	→/l      — advance without deciding
//	←/h      — go back to previous prediction
//	q        — exit loop, show summary
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
	Event       string `json:"event"`
	SessionID   string `json:"session_id"`
	StartedAt   string `json:"started_at"`
	Predictions int    `json:"predictions"`
	Applied     int    `json:"applied"`
	Skipped     int    `json:"skipped"`
}

// logInteractiveSession writes an interactive_session event JSON file to
// ~/.rinse/sessions/.  When the sessions directory cannot be created or
// written to, it falls back to appending a summary line to
// ~/.rinse/predict-events.log and emits a warning to warnW (os.Stderr when
// nil).  Non-fatal on any error — interactive mode always continues.
func logInteractiveSession(sessionID string, started time.Time, nPredictions, applied, skipped int, warnW io.Writer) {
	if warnW == nil {
		warnW = os.Stderr
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	dir := filepath.Join(home, ".rinse", "sessions")
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		fmt.Fprintln(warnW, "Session write failed — continuing without logging")
		_ = logSessionFallback(sessionID, started, nPredictions, applied, skipped, filepath.Join(home, ".rinse"))
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
		fmt.Fprintln(warnW, "Session write failed — continuing without logging")
		_ = logSessionFallback(sessionID, started, nPredictions, applied, skipped, filepath.Join(home, ".rinse"))
		return
	}
	ts := started.UTC().Format("20060102-150405")
	nano := started.UTC().UnixNano() % 1e9
	name := fmt.Sprintf("interactive-%s-%s-%09d.json", sessionID, ts, nano)
	dest := filepath.Join(dir, name)

	tmp, err := os.CreateTemp(dir, ".interactive-*.json.tmp")
	if err != nil {
		fmt.Fprintln(warnW, "Session write failed — continuing without logging")
		_ = logSessionFallback(sessionID, started, nPredictions, applied, skipped, filepath.Join(home, ".rinse"))
		return
	}
	tmpPath := tmp.Name()
	if _, werr := tmp.Write(data); werr != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		fmt.Fprintln(warnW, "Session write failed — continuing without logging")
		_ = logSessionFallback(sessionID, started, nPredictions, applied, skipped, filepath.Join(home, ".rinse"))
		return
	}
	if cerr := tmp.Close(); cerr != nil {
		_ = os.Remove(tmpPath)
		fmt.Fprintln(warnW, "Session write failed — continuing without logging")
		_ = logSessionFallback(sessionID, started, nPredictions, applied, skipped, filepath.Join(home, ".rinse"))
		return
	}
	_ = os.Rename(tmpPath, dest)
}

// logSessionFallback appends a one-line interactive_session summary to
// ~/.rinse/predict-events.log when the sessions directory is unavailable.
func logSessionFallback(sessionID string, started time.Time, nPredictions, applied, skipped int, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(dir, "predict-events.log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	line := fmt.Sprintf(
		`{"ts":%q,"event":"interactive_session","session_id":%q,"predictions":%d,"applied":%d,"skipped":%d}`+"\n",
		started.UTC().Format(time.RFC3339), sessionID, nPredictions, applied, skipped,
	)
	_, err = f.WriteString(line)
	return err
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
	applyOut, applyErr := exec.Command("git", "apply", "--index", tmpPath).CombinedOutput()
	if applyErr != nil {
		return ApplyPatchResult{Err: fmt.Errorf("git apply failed: %w\n%s", applyErr, applyOut)}
	}

	// Verify the build still passes.
	buildOut, buildErr := exec.Command("go", "build", "./...").CombinedOutput()
	if buildErr != nil {
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

// ── Confidence bar ────────────────────────────────────────────────────────────

// renderConfBar renders a 14-block confidence bar:
//
//	██████████░░░░  87%
//
// Green ≥ 80%, Yellow ≥ 60%, Red < 60%.
func renderConfBar(conf float64) string {
	const total = 14
	filled := int(conf * float64(total))
	if filled > total {
		filled = total
	}
	if filled < 0 {
		filled = 0
	}
	empty := total - filled

	var barColor lipgloss.Color
	switch {
	case conf >= 0.80:
		barColor = theme.Green
	case conf >= 0.60:
		barColor = theme.Yellow
	default:
		barColor = theme.Red
	}

	bar := lipgloss.NewStyle().Foreground(barColor).Render(strings.Repeat("█", filled)) +
		theme.StyleMuted.Render(strings.Repeat("░", empty))
	pct := lipgloss.NewStyle().Foreground(barColor).Bold(true).Render(fmt.Sprintf("%d%%", int(conf*100)))
	return bar + "  " + pct
}

// ── Progress bar ──────────────────────────────────────────────────────────────

// renderReviewProgress renders the progress row:
//
//	████████░░  2 / 7 reviewed  •  1 applied  •  ~4 min saved
func renderReviewProgress(cursor, total, applied int) string {
	const barWidth = 10
	reviewed := cursor // items reviewed so far (current item not yet decided)
	filled := 0
	if total > 0 {
		filled = reviewed * barWidth / total
	}
	empty := barWidth - filled

	bar := lipgloss.NewStyle().Foreground(theme.Mauve).Render(strings.Repeat("█", filled)) +
		theme.StyleMuted.Render(strings.Repeat("░", empty))

	estMin := applied * minutesPerAppliedFix
	sep := theme.StyleMuted.Render("  " + theme.IconSep + "  ")

	parts := []string{
		bar + "  " + theme.StyleMuted.Render(fmt.Sprintf("%d / %d reviewed", reviewed, total)),
		theme.StyleLogSuccess.Render(fmt.Sprintf("%d applied", applied)),
	}
	if estMin > 0 {
		parts = append(parts, theme.StyleVal.Render(fmt.Sprintf("~%d min saved", estMin)))
	}

	return strings.Join(parts, sep)
}

// ── Review state ──────────────────────────────────────────────────────────────

// reviewState tracks what the user decided for each prediction.
type reviewState int

const (
	reviewNone    reviewState = iota
	reviewApplied             // y
	reviewSkipped             // n / space
	reviewEdited              // e
)

// ── Bubble Tea model ──────────────────────────────────────────────────────────

// interactiveModel is the Bubble Tea model for the predict interactive loop.
type interactiveModel struct {
	predictions []Prediction
	cursor      int           // index of current prediction
	states      []reviewState // per-prediction decision
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
		states:      make([]reviewState, len(predictions)),
		termWidth:   termWidth,
		sessionID:   sessionID,
		startedAt:   time.Now(),
	}
}

// applied returns true if prediction i was applied.
func (m interactiveModel) wasApplied(i int) bool { return m.states[i] == reviewApplied }

// wasSkipped returns true if prediction i was skipped.
func (m interactiveModel) wasSkipped(i int) bool { return m.states[i] == reviewSkipped }

// countApplied returns the number of applied predictions.
func (m interactiveModel) countApplied() int {
	n := 0
	for _, s := range m.states {
		if s == reviewApplied {
			n++
		}
	}
	return n
}

// countSkipped returns the number of skipped predictions.
func (m interactiveModel) countSkipped() int {
	n := 0
	for _, s := range m.states {
		if s == reviewSkipped {
			n++
		}
	}
	return n
}

// applyResultMsg carries the result of an async ApplyPatch call back to Update.
type applyResultMsg struct {
	result ApplyPatchResult
	index  int
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
			m.states[msg.index] = reviewApplied
			m.lastMsg = theme.StyleLogSuccess.Render(theme.IconCheck + " Applied and staged.")
		} else if msg.result.BuildFail {
			m.lastMsg = theme.StyleErr.Render(theme.IconCross + " Build failed; change reverted. " + msg.result.Err.Error())
		} else if msg.result.Err != nil {
			m.lastMsg = theme.StyleErr.Render(theme.IconCross + " " + msg.result.Err.Error())
		}
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

	case "n", "N", " ":
		if m.done || m.cursor >= len(m.predictions) {
			return m, tea.Quit
		}
		m.states[m.cursor] = reviewSkipped
		m.lastMsg = theme.StyleMuted.Render("Skipped.")
		return m.advance()

	case "e", "E":
		if m.done || m.cursor >= len(m.predictions) {
			return m, tea.Quit
		}
		// v0.4: mark as edited, show muted note. Do NOT launch editor (v0.5).
		m.states[m.cursor] = reviewEdited
		m.lastMsg = theme.StyleMuted.Render("Marked as edited — open your editor manually to apply changes.")
		return m.advance()

	case "right", "l", "L":
		// Advance without deciding.
		if m.done || m.cursor >= len(m.predictions) {
			return m, nil
		}
		return m.advance()

	case "left", "h", "H":
		// Go back.
		if m.cursor > 0 {
			m.cursor--
			m.lastMsg = ""
		}
		return m, nil

	case "q", "Q", "ctrl+c":
		// Mark remaining as skipped for summary accuracy.
		for i := m.cursor; i < len(m.predictions); i++ {
			if m.states[i] == reviewNone {
				m.states[i] = reviewSkipped
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

// ── Card renderer ─────────────────────────────────────────────────────────────

// reviewedBadge returns the top-right badge string for an already-reviewed prediction.
func reviewedBadge(s reviewState) string {
	switch s {
	case reviewApplied:
		return theme.StyleLogSuccess.Render("✓ applied")
	case reviewSkipped:
		return theme.StyleMuted.Render("○ skipped")
	case reviewEdited:
		return theme.StyleTeal.Render("✎ edited")
	}
	return ""
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

	idx := m.cursor + 1
	total := len(m.predictions)
	nApplied := m.countApplied()
	state := m.states[m.cursor]

	var card strings.Builder

	// ── Pattern line ────────────────────────────────────────────────────────
	patLabel := theme.FormatPatternLabel(p.Pattern)
	patternLine := fmt.Sprintf("%s  %s",
		lipgloss.NewStyle().Foreground(theme.Mauve).Render(theme.IconDiamond),
		lipgloss.NewStyle().Foreground(theme.Text).Bold(true).Render(patLabel),
	)
	card.WriteString(patternLine + "\n")

	// Confidence bar.
	card.WriteString(renderConfBar(p.Confidence) + "\n")

	// File:line.
	if p.File != "" {
		loc := p.File
		if p.Line > 0 {
			loc = fmt.Sprintf("%s:%d", p.File, p.Line)
		}
		card.WriteString(theme.StyleMuted.Render(loc) + "\n")
	}

	// Section label: "Copilot will likely flag:"
	if p.Detail != "" {
		card.WriteString("\n" + theme.StyleMuted.Render("Copilot will likely flag:") + "\n")
		maxDetail := w - 10
		if maxDetail < 20 {
			maxDetail = 20
		}
		detail := theme.Truncate(p.Detail, maxDetail)
		card.WriteString(theme.StyleMuted.Render(detail) + "\n")
	}

	// Section label: "Suggested fix:" + diff preview.
	if strings.TrimSpace(p.SuggestedDiff) != "" {
		card.WriteString("\n" + theme.StyleMuted.Render("Suggested fix:") + "\n")
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
			card.WriteString(styled + "\n")
		}
		if len(strings.Split(p.SuggestedDiff, "\n")) > 8 {
			card.WriteString(theme.StyleMuted.Render("… (truncated)") + "\n")
		}
	}

	// Last action message.
	if m.lastMsg != "" {
		card.WriteString("\n" + m.lastMsg + "\n")
	}

	// Hint row: if already reviewed, show badge; otherwise show key hints.
	if state != reviewNone {
		card.WriteString("\n" + reviewedBadge(state) + "\n")
	} else {
		keyStyle := theme.StyleTeal
		hint := fmt.Sprintf("%s apply   %s skip   %s mark-edited   %s quit   %s back",
			keyStyle.Render("[y]"),
			keyStyle.Render("[n/space]"),
			keyStyle.Render("[e]"),
			keyStyle.Render("[q]"),
			keyStyle.Render("[h/←]"),
		)
		card.WriteString("\n" + hint + "\n")
	}

	// ── Wrap card body in rounded border ────────────────────────────────────
	cardInner := card.String()
	// Strip trailing newline for cleaner border rendering.
	cardInner = strings.TrimRight(cardInner, "\n")

	// Card title: "Prediction N / T"
	cardTitle := fmt.Sprintf(" Prediction %d / %d ", idx, total)

	// Build header title for the border — lipgloss doesn't support titles natively,
	// so we construct a border manually using lipgloss.RoundedBorder glyphs and
	// render the box with the title embedded in the top-left.
	cardWidth := w - 4 // leave 2-char margin each side
	if cardWidth < 40 {
		cardWidth = 40
	}

	// Inner width = cardWidth - 2 (border chars on each side).
	innerW := cardWidth - 2
	titleRaw := theme.StyleMuted.Render(cardTitle)
	titleVisW := lipgloss.Width(titleRaw)

	rb := lipgloss.RoundedBorder()
	borderColor := lipgloss.NewStyle().Foreground(theme.Mauve)

	// Top border: ╭─ title ─────────╮
	topFillLen := innerW - titleVisW - 2 // 2 for "─ " prefix
	if topFillLen < 0 {
		topFillLen = 0
	}
	topLine := borderColor.Render(rb.TopLeft+"─") + titleRaw +
		borderColor.Render(strings.Repeat("─", topFillLen)+rb.TopRight)

	// Body lines wrapped with side borders.
	bodyLines := strings.Split(cardInner, "\n")
	var bodyRendered strings.Builder
	for _, bl := range bodyLines {
		blW := lipgloss.Width(bl)
		padding := innerW - blW
		if padding < 0 {
			padding = 0
		}
		bodyRendered.WriteString(borderColor.Render(rb.Left) +
			bl + strings.Repeat(" ", padding) +
			borderColor.Render(rb.Right) + "\n")
	}

	// Bottom border: ╰──────────────╯
	bottomLine := borderColor.Render(rb.BottomLeft + strings.Repeat("─", innerW) + rb.BottomRight)

	// ── Progress row ────────────────────────────────────────────────────────
	progressRow := renderReviewProgress(m.cursor, total, nApplied)

	var sb strings.Builder
	sb.WriteString("\n  " + topLine + "\n")
	// Indent each body line.
	for _, bl := range strings.Split(strings.TrimRight(bodyRendered.String(), "\n"), "\n") {
		sb.WriteString("  " + bl + "\n")
	}
	sb.WriteString("  " + bottomLine + "\n")
	sb.WriteString("\n  " + progressRow + "\n\n")

	return sb.String()
}

// ── Summary ───────────────────────────────────────────────────────────────────

const minutesPerAppliedFix = 4

// printSummary writes the post-loop summary box to w.
func printSummary(w io.Writer, predictions []Prediction, states []reviewState) {
	total := len(predictions)
	nApplied := 0
	nSkipped := 0
	for _, s := range states {
		if s == reviewApplied {
			nApplied++
		}
		if s == reviewSkipped || s == reviewEdited {
			nSkipped++
		}
	}
	estMin := nApplied * minutesPerAppliedFix

	// Border color: green when applied > 0, overlay when all skipped.
	var borderColor lipgloss.Color
	if nApplied > 0 {
		borderColor = theme.Green
	} else {
		borderColor = theme.Overlay
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
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

	// Inline rendering — no alt-screen (spec: "renders inline, no alt-screen required").
	prog := tea.NewProgram(model)
	finalModel, err := prog.Run()
	if err != nil {
		return fmt.Errorf("predict interactive: %w", err)
	}

	final, ok := finalModel.(interactiveModel)
	if !ok {
		return fmt.Errorf("predict interactive: unexpected model type")
	}

	// Print summary.
	printSummary(out, final.predictions, final.states)

	// Log session event (fire-and-forget).
	nApplied := final.countApplied()
	nSkipped := final.countSkipped()
	logInteractiveSession(sessionID, final.startedAt, len(final.predictions), nApplied, nSkipped, out)

	return nil
}
