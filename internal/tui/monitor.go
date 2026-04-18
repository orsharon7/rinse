package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/orsharon7/rinse/internal/notify"
	"github.com/orsharon7/rinse/internal/session"
	"github.com/orsharon7/rinse/internal/theme"
)

// ansiRe strips ANSI escape sequences for pattern matching only.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[mK]`)

// commentCountRe matches "N comment(s)" to extract the comment count.
var commentCountRe = regexp.MustCompile(`(\d+)\s+comment\(s\)`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// ── Timing helpers ────────────────────────────────────────────────────────────

// formatElapsed formats d per spec:
//
//	0 – 59m59s  → mm:ss        (e.g. "04:37")
//	1h – 99h59m → h:mm:ss      (e.g. "1:04:37")   hours NOT zero-padded
//	100h+       → hh:mm:ss     (e.g. "100:04:37")
func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	totalSec := int(d.Seconds())
	h := totalSec / 3600
	m := (totalSec % 3600) / 60
	s := totalSec % 60
	if h == 0 {
		return fmt.Sprintf("%02d:%02d", m, s)
	}
	return fmt.Sprintf("%d:%02d:%02d", h, m, s)
}

// etaState describes which ETA display branch applies.
type etaState int

const (
	etaHidden     etaState = iota // phase not yet started (pending)
	etaUnknown                    // running but no estimatedEndAt
	etaComputable                 // ETA today
	etaFutureDay                  // ETA tomorrow or later
	etaOverdue                    // past estimated end
	etaCompleted                  // cycle done
	etaError                      // cycle error
)

// resolveETA computes the ETA display state given current phase and optional
// server-supplied estimatedEndAt. now is the clock-offset-adjusted current time.
func resolveETA(p phase, estimatedEndAt *time.Time, now time.Time) (etaState, time.Time) {
	switch p {
	case phaseStarting:
		return etaHidden, time.Time{}
	case phaseDone:
		return etaCompleted, time.Time{}
	case phaseError:
		return etaError, time.Time{}
	}
	// Active phases (waiting/fixing/reflecting).
	if estimatedEndAt == nil {
		return etaUnknown, time.Time{}
	}
	eta := *estimatedEndAt
	if eta.Before(now) {
		return etaOverdue, eta
	}
	etaLocal := eta.Local()
	nowLocal := now.Local()
	if etaLocal.Year() == nowLocal.Year() && etaLocal.YearDay() == nowLocal.YearDay() {
		return etaComputable, eta
	}
	return etaFutureDay, eta
}

// ── Phase ─────────────────────────────────────────────────────────────────────

type phase int

const (
	phaseStarting phase = iota
	phaseWaiting
	phaseFixing
	phaseReflecting
	phaseDone
	phaseError
	phaseStalled   // Copilot review timed out — amber
	phaseCancelled // cycle cancelled by user or runner — silver
)

func (p phase) String() string {
	switch p {
	case phaseStarting:
		return "starting"
	case phaseWaiting:
		return "waiting for Copilot"
	case phaseFixing:
		return "fixing comments"
	case phaseReflecting:
		return "reflecting"
	case phaseDone:
		return "done"
	case phaseError:
		return "error"
	case phaseStalled:
		return "stalled"
	case phaseCancelled:
		return "cancelled"
	}
	return ""
}

func (p phase) Style() lipgloss.Style {
	switch p {
	case phaseStarting:
		return theme.StylePhaseWaiting
	case phaseWaiting:
		return theme.StylePhaseWaiting
	case phaseFixing:
		return theme.StylePhaseFixing
	case phaseReflecting:
		return theme.StylePhaseReflect
	case phaseDone:
		return theme.StylePhaseDone
	case phaseError:
		return theme.StylePhaseErr
	case phaseStalled:
		return theme.StylePhaseStalled
	case phaseCancelled:
		return theme.StylePhaseCancelled
	}
	return theme.StylePhaseWaiting
}

// ── Messages ──────────────────────────────────────────────────────────────────

type logLineMsg string
type runnerDoneMsg struct{ exitCode int }
type tickMsg time.Time
type clearStatusMsg struct{}
type clearToastMsg struct{}
type actionDoneMsg struct {
	output string
	err    error
}

// ── Iteration history ─────────────────────────────────────────────────────────

type iterResult int

const (
	iterRunning  iterResult = iota
	iterFixed               // comments fixed, pushed
	iterClean               // clean review (0 comments)
	iterApproved            // Copilot approved
	iterError               // error during this iteration
)

type iterEntry struct {
	num      int
	result   iterResult
	comments int
}

// ── Post-cycle menu ───────────────────────────────────────────────────────────

type postCycleOption struct {
	label string
}

func buildPostCycleOptions(defaultBranch string) []postCycleOption {
	return []postCycleOption{
		{label: "Merge PR + delete remote & local branch + checkout " + theme.IconArrow + " " + defaultBranch},
		{label: "Merge PR only"},
		{label: "Open PR in browser"},
		{label: "Do nothing (exit)"},
	}
}

// ── Monitor model ─────────────────────────────────────────────────────────────

type monitorModel struct {
	// config
	pr        string
	repo      string
	runner    string
	model     string
	prTitle   string
	cwd       string
	autoMerge bool

	// state
	width        int
	height       int
	phase        phase
	iter         int
	started      time.Time
	lines        []string
	reflectLines []string
	renderedLog  *strings.Builder

	// stats tracking
	totalComments   int
	rulesExtracted  int
	iterHistory     []iterEntry
	currentComments int

	// wait progress
	waitElapsed int
	waitMax     int
	waitLabel   string

	// toast notification
	toastMsg string

	// post-cycle menu
	readyToMerge       bool
	showPostCycleMenu  bool
	postCycleCursor    int
	postCycleOptions   []postCycleOption
	postCycleDefaultBr string

	// sub-components
	viewport    viewport.Model
	spinner     spinner.Model
	atBottom    bool
	showHelp    bool
	showHistory bool
	showTooltip bool
	statusMsg   string

	// runner process
	cmd          *exec.Cmd
	exitCode     int
	done         bool
	cancelReason string // first cancel/stalled log line, for edge-case screens

	// timing: server-driven time derivation per UX spec (RIN-42).
	// clockOffset is computed once per connection as serverNow - Date.now().
	// For the local runner, it remains zero (runner IS the server).
	clockOffset        time.Duration
	lastStateChangedAt time.Time     // wall-clock of most recent phase transition
	frozenElapsed      *time.Duration // set when entering done/error; nil while active
	estimatedEndAt     *time.Time     // server-supplied ETA (nil until runner emits it)
	overdueAnnounced   bool           // prevents repeated toast on overdue crossing

	// timing tooltip (toggled by 't' key)
	showTimingTooltip bool
}

func newMonitorModel(pr, repo, runnerName, modelName, prTitle, cwd string, autoMerge bool, cmd *exec.Cmd) monitorModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(theme.Mauve)

	vp := viewport.New(80, 20)
	vp.Style = lipgloss.NewStyle().Foreground(theme.Text)

	defaultBr := detectLocalDefaultBranch(cwd)

	return monitorModel{
		pr:                 pr,
		repo:               repo,
		runner:             runnerName,
		model:              modelName,
		prTitle:            prTitle,
		cwd:                cwd,
		autoMerge:          autoMerge,
		phase:              phaseStarting,
		started:            time.Now(),
		spinner:            sp,
		viewport:           vp,
		atBottom:           true,
		cmd:                cmd,
		renderedLog:        &strings.Builder{},
		postCycleDefaultBr: defaultBr,
		lastStateChangedAt: time.Now(),
	}
}

func detectLocalDefaultBranch(cwd string) string {
	if cwd == "" {
		return "main"
	}
	out, err := exec.Command("git", "-C", cwd,
		"symbolic-ref", "refs/remotes/origin/HEAD").Output()
	if err != nil {
		return "main"
	}
	ref := strings.TrimSpace(string(out))
	if idx := strings.LastIndex(ref, "/"); idx >= 0 {
		return ref[idx+1:]
	}
	return "main"
}

// ── Init ──────────────────────────────────────────────────────────────────────

// elapsedForDisplay returns the duration to show in the elapsed badge.
// Returns frozenElapsed when the cycle has ended; ticks live otherwise.
// Returns 0 during phaseStarting (not yet shown per state-mapping spec).
func (m monitorModel) elapsedForDisplay() time.Duration {
	if m.frozenElapsed != nil {
		return *m.frozenElapsed
	}
	if m.phase == phaseStarting {
		return 0
	}
	// Display elapsed runtime using the local/monotonic clock so clockOffset
	// does not skew the duration.
	elapsed := time.Since(m.started)
	return elapsed.Round(time.Second)
}

// nowAdjusted returns the clock-offset-adjusted current time.
func (m monitorModel) nowAdjusted() time.Time {
	return time.Now().Add(m.clockOffset)
}

// applyPhaseChange records a phase transition: updates lastStateChangedAt and
// freezes elapsed when entering a terminal phase. Returns the updated model.
func (m monitorModel) applyPhaseChange(newPhase phase) monitorModel {
	if newPhase == m.phase {
		return m
	}
	m.lastStateChangedAt = time.Now()
	if newPhase == phaseDone || newPhase == phaseError || newPhase == phaseStalled || newPhase == phaseCancelled {
		d := (time.Since(m.started) + m.clockOffset).Round(time.Second)
		m.frozenElapsed = &d
	}
	m.phase = newPhase
	return m
}

func (m monitorModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, tick())
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// ── Layout helpers ────────────────────────────────────────────────────────────

func (m monitorModel) reflectPanelWidth() int {
	w := m.width * 28 / 100
	if w < 35 {
		w = 35
	}
	if w > 55 {
		w = 55
	}
	return w
}

func (m monitorModel) logWidth() int {
	if m.width <= 0 {
		return 80
	}
	if !m.showReflectPanel() {
		return m.width
	}
	rpw := m.reflectPanelWidth()
	w := m.width - (rpw + 3)
	if w < 1 {
		w = 1
	}
	return w
}

func (m monitorModel) showReflectPanel() bool {
	return m.width > 90
}

// logHeight — reserved: header(2) + header border(1) + breadcrumb(1) + statusbar(1) + statusbar border(1) = 6.
func (m monitorModel) logHeight() int {
	h := m.height - 6
	if h < 4 {
		h = 4
	}
	return h
}

// ── Phase breadcrumb ──────────────────────────────────────────────────────────

func (m monitorModel) renderPhaseBreadcrumb() string {
	ordered := []phase{phaseStarting, phaseWaiting, phaseFixing, phaseReflecting, phaseDone}
	names := []string{"start", "waiting", "fixing", "reflect", "done"}

	currentPhase := m.phase
	if currentPhase == phaseError || currentPhase == phaseStalled || currentPhase == phaseCancelled {
		currentPhase = phaseDone
	}

	var parts []string
	for i, p := range ordered {
		var part string
		switch {
		case m.phase == phaseError && p == phaseDone:
			part = theme.StylePhaseErr.Render(theme.IconCross + " error")
		case m.phase == phaseStalled && p == phaseDone:
			part = theme.StylePhaseStalled.Render("⚠ stalled")
		case m.phase == phaseCancelled && p == phaseDone:
			part = theme.StylePhaseCancelled.Render("○ cancelled")
		case p < currentPhase:
			part = theme.StyleLogDebug.Render(theme.IconCheck + " " + names[i])
		case p == currentPhase:
			part = m.phase.Style().Render(theme.IconRadioOn + " " + names[i])
		default:
			part = theme.StyleMuted.Render(theme.IconCircle + " " + names[i])
		}
		parts = append(parts, part)
	}

	sep := theme.StyleMuted.Render(" › ")
	return "  " + strings.Join(parts, sep)
}

// ── Help overlay ──────────────────────────────────────────────────────────────

func (m monitorModel) renderHelp() string {
	helpStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Surface).
		Padding(1, 4)

	title := theme.GradientString("MONITOR SHORTCUTS", theme.Mauve, theme.Lavender, true)

	type krow struct{ key, desc string }
	rows := []krow{
		{"↑ / k", "scroll up"},
		{"↓ / j", "scroll down"},
		{"g", "jump to top"},
		{"G", "jump to bottom"},
		{"t", "timing tooltip"},
		{"S", "save full session log"},
		{"s", "save reflect log"},
		{"esc / q", "close this help"},
		{"?", "toggle this help"},
		{"ctrl+c", "quit rinse"},
	}

	var lines []string
	for _, r := range rows {
		lines = append(lines,
			theme.StyleHintKey.Render(fmt.Sprintf("  %-10s", r.key))+"  "+
				lipgloss.NewStyle().Foreground(theme.Subtext).Render(r.desc))
	}

	return helpStyle.Render(title + "\n\n" + strings.Join(lines, "\n"))
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m monitorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = m.logWidth()
		m.viewport.Height = m.logHeight()
		m.viewport.SetContent(m.renderedLog.String())
		if m.atBottom {
			m.viewport.GotoBottom()
		}

	case tea.KeyMsg:
		// When the help overlay is open: CloseHelp (esc/q) or Help (?) closes it;
		// ForceQuit (ctrl+c) always quits regardless of overlay state.
		if m.showHelp {
			if key.Matches(msg, Keys.CloseHelp) || key.Matches(msg, Keys.Help) {
				m.showHelp = false
			} else if key.Matches(msg, Keys.ForceQuit) {
				if m.cmd != nil && m.cmd.Process != nil {
					_ = m.cmd.Process.Kill()
				}
				return m, tea.Quit
			}
			return m, nil
		}

		// When the timing tooltip is open: any key closes it (except ctrl+c which quits).
		if m.showTimingTooltip {
			if key.Matches(msg, Keys.ForceQuit) {
				if m.cmd != nil && m.cmd.Process != nil {
					_ = m.cmd.Process.Kill()
				}
				return m, tea.Quit
			}
			m.showTimingTooltip = false
			return m, nil
		}

		// Outside the help overlay: q or ctrl+c quits.
		if key.Matches(msg, Keys.Quit) {
			if m.cmd != nil && m.cmd.Process != nil {
				_ = m.cmd.Process.Kill()
			}
			return m, tea.Quit
		}

		if m.showPostCycleMenu {
			return m.handlePostCycleKey(msg)
		}

		if key.Matches(msg, Keys.Help) {
			m.showHelp = true
		} else if key.Matches(msg, Keys.TimingInfo) {
			m.showTimingTooltip = !m.showTimingTooltip
		} else {
			switch msg.String() {
			case "G":
				m.atBottom = true
				m.viewport.GotoBottom()
			case "g":
				m.atBottom = false
				m.viewport.GotoTop()
			case "h":
				m.showHistory = !m.showHistory
			case "t":
				m.showTooltip = !m.showTooltip
			case "s":
				if len(m.reflectLines) > 0 {
					fname := fmt.Sprintf("rinse-reflect-%s.txt",
						time.Now().Format("20060102-150405"))
					content := strings.Join(m.reflectLines, "\n") + "\n"
					if err := os.WriteFile(fname, []byte(content), 0o644); err != nil {
						m.statusMsg = theme.IconCross + " save failed"
					} else {
						m.statusMsg = theme.IconCheck + " reflect log " + theme.IconArrow + " " + fname
					}
					cmds = append(cmds, tea.Tick(2*time.Second,
						func(t time.Time) tea.Msg { return clearStatusMsg{} }))
				} else {
					m.statusMsg = "no reflect lines to save"
					cmds = append(cmds, tea.Tick(2*time.Second,
						func(t time.Time) tea.Msg { return clearStatusMsg{} }))
				}
			case "S":
				ts := time.Now().Format("20060102-150405")
				mainFname := fmt.Sprintf("rinse-log-%s.txt", ts)
				mainContent := m.renderedLog.String()
				if mainContent != "" && !strings.HasSuffix(mainContent, "\n") {
					mainContent += "\n"
				}
				var savedParts []string
				if err := os.WriteFile(mainFname, []byte(mainContent), 0o644); err != nil {
					m.statusMsg = theme.IconCross + " save failed"
				} else {
					m.statusMsg = theme.IconCheck + " reflect log " + theme.IconArrow + " " + fname
				}
				cmds = append(cmds, tea.Tick(2*time.Second,
					func(t time.Time) tea.Msg { return clearStatusMsg{} }))
			} else {
				m.statusMsg = "no reflect lines to save"
				cmds = append(cmds, tea.Tick(2*time.Second,
					func(t time.Time) tea.Msg { return clearStatusMsg{} }))
			}
		} else if key.Matches(msg, Keys.SaveAll) {
			ts := time.Now().Format("20060102-150405")
			mainFname := fmt.Sprintf("rinse-log-%s.txt", ts)
			mainContent := m.renderedLog.String()
			if mainContent != "" && !strings.HasSuffix(mainContent, "\n") {
				mainContent += "\n"
			}
			var savedParts []string
			if err := os.WriteFile(mainFname, []byte(mainContent), 0o644); err != nil {
				m.statusMsg = theme.IconCross + " save failed"
			} else {
				savedParts = append(savedParts, mainFname)
			}
			if len(m.reflectLines) > 0 {
				refFname := fmt.Sprintf("rinse-reflect-%s.txt", ts)
				refContent := strings.Join(m.reflectLines, "\n") + "\n"
				if err := os.WriteFile(refFname, []byte(refContent), 0o644); err == nil {
					savedParts = append(savedParts, refFname)
				}
			}
			if len(savedParts) > 0 {
				m.statusMsg = theme.IconCheck + " saved " + theme.IconArrow + " " + strings.Join(savedParts, ", ")
			}
			cmds = append(cmds, tea.Tick(3*time.Second,
				func(t time.Time) tea.Msg { return clearStatusMsg{} }))
		} else {
			var vpcmd tea.Cmd
			m.viewport, vpcmd = m.viewport.Update(msg)
			m.atBottom = m.viewport.AtBottom()
			cmds = append(cmds, vpcmd)
		}

	case tickMsg:
		cmds = append(cmds, tick())
		// Overdue detection: fire a toast once when ETA is crossed.
		if !m.overdueAnnounced {
			etaSt, _ := resolveETA(m.phase, m.estimatedEndAt, m.nowAdjusted())
			if etaSt == etaOverdue {
				m.toastMsg = "⚠ overdue"
				m.overdueAnnounced = true
				cmds = append(cmds, tea.Tick(3*time.Second,
					func(time.Time) tea.Msg { return clearToastMsg{} }))
			}
		}

	case spinner.TickMsg:
		var spcmd tea.Cmd
		m.spinner, spcmd = m.spinner.Update(msg)
		cmds = append(cmds, spcmd)

	case logLineMsg:
		raw := string(msg)
		plain := stripANSI(raw)

		// Detect "ready to merge" signals.
		if isReadyToMerge(plain) {
			m.readyToMerge = true
			m.toastMsg = theme.IconCheck + "  PR ready to merge!"
			cmds = append(cmds, tea.Tick(4*time.Second,
				func(t time.Time) tea.Msg { return clearToastMsg{} }))
		}

		// Track comment counts.
		if strings.Contains(plain, "comment(s) in review") {
			var n int
			if sub := plain; true {
				if idx := strings.Index(sub, "💬"); idx >= 0 {
					sub = sub[idx:]
				}
				if m := commentCountRe.FindStringSubmatch(sub); len(m) == 2 {
					fmt.Sscanf(m[1], "%d", &n)
				}
			}
			if n > 0 {
				m.currentComments = n
				m.totalComments += n
			}
		}

		// Track rule extraction.
		if strings.Contains(plain, "rule(s) pushed") {
			var n int
			for _, word := range strings.Fields(plain) {
				cleaned := strings.TrimPrefix(word, "+")
				if _, err := fmt.Sscanf(cleaned, "%d", &n); err == nil && n > 0 {
					break
				}
			}
			if n > 0 {
				m.rulesExtracted += n
				m.toastMsg = fmt.Sprintf(theme.IconRadioOn+"  +%d rule(s) extracted", n)
				cmds = append(cmds, tea.Tick(3*time.Second,
					func(t time.Time) tea.Msg { return clearToastMsg{} }))
			}
		}

		// Track iteration results.
		if strings.Contains(plain, "complete") && strings.Contains(plain, "Iteration") {
			iterNum := m.iter
			fields := strings.Fields(plain)
			for i, word := range fields {
				if word == "Iteration" && i+1 < len(fields) {
					var parsedIter int
					if _, err := fmt.Sscanf(fields[i+1], "%d", &parsedIter); err == nil && parsedIter > 0 {
						iterNum = parsedIter
						m.iter = parsedIter
					}
					break
				}
			}
			entry := iterEntry{num: iterNum, result: iterFixed, comments: m.currentComments}
			m.iterHistory = append(m.iterHistory, entry)
			m.currentComments = 0
		}
		if strings.Contains(plain, "APPROVED") {
			if len(m.iterHistory) == 0 || m.iterHistory[len(m.iterHistory)-1].result != iterApproved {
				entry := iterEntry{num: m.iter, result: iterApproved}
				m.iterHistory = append(m.iterHistory, entry)
			}
			m.toastMsg = theme.IconCheck + "  Copilot APPROVED!"
			cmds = append(cmds, tea.Tick(5*time.Second,
				func(t time.Time) tea.Msg { return clearToastMsg{} }))
		}
		if strings.Contains(plain, "Clean review") {
			if len(m.iterHistory) == 0 || m.iterHistory[len(m.iterHistory)-1].result != iterClean {
				entry := iterEntry{num: m.iter, result: iterClean}
				m.iterHistory = append(m.iterHistory, entry)
			}
		}

		// Suppress wait-tick lines — render as progress bar instead.
		if isWaitTickLine(plain) {
			var e, mx int
			if _, err := fmt.Sscanf(extractWaitProgress(plain), "%ds / %ds", &e, &mx); err == nil {
				m.waitElapsed = e
				m.waitMax = mx
			}
			if idx := strings.Index(plain, "⏳"); idx >= 0 {
				after := strings.TrimSpace(plain[idx+len("⏳"):])
				if dotIdx := strings.Index(after, "..."); dotIdx > 0 {
					m.waitLabel = after[:dotIdx]
				}
			}
			m = m.applyPhaseChange(inferPhase(plain, m.phase))
			break
		}

		nextPhase := inferPhase(plain, m.phase)
		if m.waitMax > 0 && nextPhase != phaseWaiting {
			m.waitElapsed = 0
			m.waitMax = 0
			m.waitLabel = ""
		}

		if isReflectLine(plain) {
			entry := extractReflectEntry(plain)
			m.reflectLines = append(m.reflectLines, entry)
			if !m.showReflectPanel() {
				m.lines = append(m.lines, raw)
				m.renderedLog.WriteString(colorLine(raw) + "\n")
			}
		} else {
			m.lines = append(m.lines, raw)
			m.renderedLog.WriteString(colorLine(raw) + "\n")
		}

		m = m.applyPhaseChange(nextPhase)

		// Capture cancel/stall reason from first matching line
		if m.cancelReason == "" && (nextPhase == phaseCancelled || nextPhase == phaseStalled) {
			m.cancelReason = strings.TrimSpace(plain)
		}

		if strings.Contains(plain, "Iteration") {
			var n int
			if _, err := fmt.Sscanf(plain, "%*[^0-9]%d", &n); err == nil && n > m.iter {
				m.iter = n
			}
		}

		m.viewport.Width = m.logWidth()
		m.viewport.Height = m.logHeight()
		m.viewport.SetContent(m.renderedLog.String())
		if m.atBottom {
			m.viewport.GotoBottom()
		}

	case runnerDoneMsg:
		m.done = true
		m.exitCode = msg.exitCode
		if msg.exitCode == 0 {
			m = m.applyPhaseChange(phaseDone)
			if m.readyToMerge && !m.autoMerge {
				m.showPostCycleMenu = true
				m.postCycleCursor = 0
				m.postCycleOptions = buildPostCycleOptions(m.postCycleDefaultBr)
			}
		} else {
			m = m.applyPhaseChange(phaseError)
		}
		m.viewport.SetContent(m.renderedLog.String())
		if m.atBottom {
			m.viewport.GotoBottom()
		}

	case actionDoneMsg:
		if msg.output != "" {
			for _, ln := range strings.Split(strings.TrimRight(msg.output, "\n"), "\n") {
				m.renderedLog.WriteString(colorLine(ln) + "\n")
			}
		}
		if msg.err != nil {
			m.renderedLog.WriteString(colorLine("❌ action failed: "+msg.err.Error()) + "\n")
		}
		m.viewport.SetContent(m.renderedLog.String())
		m.viewport.GotoBottom()
		m.atBottom = true

	case clearStatusMsg:
		m.statusMsg = ""

	case clearToastMsg:
		m.toastMsg = ""
	}

	return m, tea.Batch(cmds...)
}

func isReadyToMerge(plain string) bool {
	return strings.Contains(plain, "Clean review") ||
		strings.Contains(plain, "ready to merge") ||
		(strings.Contains(plain, "APPROVED") && strings.Contains(plain, "PR"))
}

func (m monitorModel) handlePostCycleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(m.postCycleOptions)
	switch {
	case key.Matches(msg, Keys.Up):
		if m.postCycleCursor > 0 {
			m.postCycleCursor--
		}
	case key.Matches(msg, Keys.Down):
		if m.postCycleCursor < n-1 {
			m.postCycleCursor++
		}
	case key.Matches(msg, Keys.Confirm):
		return m.executePostCycleAction(m.postCycleCursor)
	case key.Matches(msg, Keys.Back):
		m.showPostCycleMenu = false
		return m, nil
	}
	return m, nil
}

func (m monitorModel) executePostCycleAction(choice int) (tea.Model, tea.Cmd) {
	pr := m.pr
	repo := m.repo
	cwd := m.cwd
	defaultBr := m.postCycleDefaultBr

	m.showPostCycleMenu = false

	switch choice {
	case 0:
		return m, func() tea.Msg {
			out, err := runShell("gh", "pr", "merge", pr, "--repo", repo, "--merge", "--delete-branch", "--yes")
			if err != nil {
				return actionDoneMsg{output: out, err: err}
			}
			localBranch, revErr := runShell("git", "-C", cwd, "rev-parse", "--abbrev-ref", "HEAD")
			if revErr != nil {
				return actionDoneMsg{output: theme.IconCross + " Merged, remote branch deleted. (local cleanup skipped)", err: revErr}
			}
			localBranch = strings.TrimSpace(localBranch)
			if localBranch == "" || localBranch == defaultBr {
				return actionDoneMsg{output: theme.IconCheck + " Merged, remote branch deleted."}
			}
			if _, coErr := runShell("git", "-C", cwd, "checkout", defaultBr); coErr != nil {
				return actionDoneMsg{output: theme.IconCross + " Merged, remote branch deleted. (checkout " + defaultBr + " failed)", err: coErr}
			}
			if _, delErr := runShell("git", "-C", cwd, "branch", "-d", localBranch); delErr != nil {
				if _, delErrF := runShell("git", "-C", cwd, "branch", "-D", localBranch); delErrF != nil {
					return actionDoneMsg{output: theme.IconCross + " Merged, remote branch deleted. (local branch delete failed)", err: delErrF}
				}
			}
			return actionDoneMsg{output: theme.IconCheck + " Merged, remote branch deleted, local branch deleted."}
		}
	case 1:
		return m, func() tea.Msg {
			out, err := runShell("gh", "pr", "merge", pr, "--repo", repo, "--merge", "--yes")
			return actionDoneMsg{output: out, err: err}
		}
	case 2:
		return m, func() tea.Msg {
			out, err := runShell("gh", "pr", "view", pr, "--repo", repo, "--web")
			return actionDoneMsg{output: out, err: err}
		}
	default:
		return m, tea.Quit
	}
}

func runShell(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func isReflectLine(plain string) bool {
	return strings.Contains(plain, "[reflect]") ||
		strings.Contains(plain, "◎ reflect") ||
		strings.Contains(plain, "✗ reflect") ||
		strings.Contains(plain, "○ reflect")
}

func isWaitTickLine(plain string) bool {
	return strings.Contains(plain, "⏳") &&
		strings.Contains(plain, "reviewing") &&
		strings.Contains(plain, "s /")
}

func extractWaitProgress(plain string) string {
	if idx := strings.LastIndex(plain, "("); idx >= 0 {
		if end := strings.Index(plain[idx:], ")"); end >= 0 {
			return plain[idx+1 : idx+end]
		}
	}
	return ""
}

func (m monitorModel) renderWaitProgress() string {
	label := m.waitLabel
	if label == "" {
		label = "Copilot reviewing"
	}
	elapsed := m.waitElapsed
	mx := m.waitMax
	if mx < 1 {
		mx = 1
	}

	barW := 20
	filled := elapsed * barW / mx
	if filled > barW {
		filled = barW
	}
	empty := barW - filled
	if empty < 0 {
		empty = 0
	}
	pct := elapsed * 100 / mx

	bar := theme.StylePhaseWaiting.Render(strings.Repeat("━", filled)) +
		theme.StyleMuted.Render(strings.Repeat("─", empty))

	return m.spinner.View() + " " +
		theme.StylePhaseWaiting.Render(label) + "  " +
		bar + "  " +
		theme.StyleMuted.Render(fmt.Sprintf("%ds / %ds (%d%%)", elapsed, mx, pct))
}

// ── Edge-case screens ─────────────────────────────────────────────────────────

// renderStalledScreen renders an amber banner when Copilot review has stalled.
func (m monitorModel) renderStalledScreen(w int) string {
	elapsed := theme.StyleMuted.Render(fmt.Sprintf("(%s elapsed)", formatElapsed(m.elapsedForDisplay())))
	title := theme.StylePhaseStalled.Render("⚠  Copilot review stalled")
	body := lipgloss.JoinVertical(lipgloss.Left,
		title,
		"",
		theme.StyleMuted.Render("The review has not responded within the expected window."),
		theme.StyleMuted.Render("RINSE will retry automatically. "+elapsed),
	)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Peach).
		Padding(1, 2).
		Width(min(w-4, 60)).
		Render(body)
	return lipgloss.NewStyle().Width(w).Align(lipgloss.Center).Render(box)
}

// renderCancelledScreen renders a greyed card when the cycle is cancelled.
func (m monitorModel) renderCancelledScreen(w int) string {
	reason := m.cancelReason
	if reason == "" {
		reason = "Cycle cancelled."
	}
	title := theme.StylePhaseCancelled.Render("○  Cycle cancelled")
	body := lipgloss.JoinVertical(lipgloss.Left,
		title,
		"",
		theme.StyleMuted.Render(reason),
		"",
		theme.StyleMuted.Render("Press q to quit, or start a new cycle."),
	)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Overlay).
		Padding(1, 2).
		Width(min(w-4, 60)).
		Render(body)
	return lipgloss.NewStyle().Width(w).Align(lipgloss.Center).Render(box)
}

// renderFailedScreen renders a red card with the last log lines on non-zero exit.
func (m monitorModel) renderFailedScreen(w int) string {
	title := theme.StylePhaseErr.Render("✗  Cycle failed")
	// Show up to last 5 log lines as context
	lines := m.lines
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	var renderedLines []string
	for _, l := range lines {
		plain := strings.ToLower(stripANSI(l))
		if strings.Contains(plain, "error") || strings.Contains(plain, "failed") ||
			strings.Contains(plain, "fatal") || strings.Contains(plain, "❌") {
			renderedLines = append(renderedLines, theme.StyleLogErr.Render(l))
		} else {
			renderedLines = append(renderedLines, theme.StyleMuted.Render(l))
		}
	}
	logSnippet := strings.Join(renderedLines, "\n")
	body := lipgloss.JoinVertical(lipgloss.Left,
		title,
		"",
		theme.StyleMuted.Render(fmt.Sprintf("Exit code: %d", m.exitCode)),
		"",
		logSnippet,
	)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Red).
		Padding(1, 2).
		Width(min(w-4, 70)).
		Render(body)
	return lipgloss.NewStyle().Width(w).Align(lipgloss.Center).Render(box)
}

func inferPhase(plain string, current phase) phase {
	switch {
	case current == phaseDone || current == phaseStalled || current == phaseCancelled:
		return current
	case strings.Contains(plain, "APPROVED"):
		return phaseDone
	case strings.Contains(plain, "stalled") || strings.Contains(plain, "Stalled"):
		return phaseStalled
	case strings.Contains(plain, "cancelled") || strings.Contains(plain, "Cancelled") ||
		strings.Contains(plain, "CANCELLED"):
		return phaseCancelled
	case strings.Contains(plain, "❌") || strings.Contains(plain, "Timed out"):
		return phaseError
	case isReflectLine(plain):
		return phaseReflecting
	case strings.Contains(plain, "invoking opencode") ||
		strings.Contains(plain, "invoking claude") ||
		strings.Contains(plain, "💬"):
		return phaseFixing
	case strings.Contains(plain, "Waiting for Copilot") ||
		strings.Contains(plain, "Copilot reviewing") ||
		strings.Contains(plain, "⏳"):
		return phaseWaiting
	case current == phaseStarting && (strings.Contains(plain, "Starting") ||
		strings.Contains(plain, "🚀") ||
		strings.Contains(plain, "Repo:") ||
		strings.Contains(plain, "Model:")):
		return phaseWaiting
	}
	return current
}

func extractReflectEntry(plain string) string {
	if idx := strings.Index(plain, "[reflect]"); idx >= 0 {
		msg := strings.TrimSpace(plain[idx+len("[reflect]"):])
		return msg
	}
	for _, prefix := range []string{"◎ reflect", "✗ reflect", "○ reflect"} {
		if idx := strings.Index(plain, prefix); idx >= 0 {
			msg := strings.TrimSpace(plain[idx+len(prefix):])
			if len(msg) > 0 && (msg[0] == '|' || strings.HasPrefix(msg, "│")) {
				if msg[0] == '|' {
					msg = strings.TrimSpace(msg[1:])
				} else {
					msg = strings.TrimSpace(strings.TrimPrefix(msg, "│"))
				}
			}
			return msg
		}
	}
	return plain
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m monitorModel) View() string {
	totalW := m.width
	if totalW <= 0 {
		totalW = 80
	}

	if m.showHelp {
		h := m.height
		if h <= 0 {
			h = 24
		}
		return lipgloss.Place(totalW, h, lipgloss.Center, lipgloss.Center, m.renderHelp())
	}

	if m.showTimingTooltip {
		h := m.height
		if h <= 0 {
			h = 24
		}
		return lipgloss.Place(totalW, h, lipgloss.Center, lipgloss.Center, m.renderTimingTooltip())
	}

	if m.showPostCycleMenu {
		h := m.height
		if h <= 0 {
			h = 24
		}
		return lipgloss.Place(totalW, h, lipgloss.Center, lipgloss.Center, m.renderPostCycleMenu())
	}

	// Edge-case screens: stalled, cancelled, failed (done with non-zero exit).
	if m.phase == phaseStalled {
		h := m.height
		if h <= 0 {
			h = 24
		}
		return lipgloss.Place(totalW, h, lipgloss.Center, lipgloss.Center, m.renderStalledScreen(totalW))
	}
	if m.phase == phaseCancelled {
		h := m.height
		if h <= 0 {
			h = 24
		}
		return lipgloss.Place(totalW, h, lipgloss.Center, lipgloss.Center, m.renderCancelledScreen(totalW))
	}
	if m.done && m.exitCode != 0 {
		h := m.height
		if h <= 0 {
			h = 24
		}
		return lipgloss.Place(totalW, h, lipgloss.Center, lipgloss.Center, m.renderFailedScreen(totalW))
	}

	showPanel := m.showReflectPanel()
	logW := m.logWidth()
	if !showPanel {
		logW = totalW
	}
	logH := m.logHeight()

	// ── Header line 1: Compact brand with PR context ─────────────────────────
	elapsed := m.elapsedForDisplay()

	prCtx := "#" + m.pr
	if m.repo != "" {
		prCtx += " " + theme.IconSep + " " + m.repo
	}
	if m.runner != "" {
		prCtx += " " + theme.IconSep + " " + m.runner
	}
	headerLine1 := theme.RenderCompactBrandWithDetails(totalW-2, prCtx)
	if m.prTitle != "" {
		headerLine1 += "\n  " + theme.StyleMuted.Render(`"`) +
			lipgloss.NewStyle().Foreground(theme.Text).Italic(true).Render(theme.Truncate(m.prTitle, 50)) +
			theme.StyleMuted.Render(`"`)
	}

	// ── Header line 2: Compact stat badges ────────────────────────────────────
	iterStr := "-"
	if m.iter > 0 {
		iterStr = fmt.Sprintf("%d", m.iter)
	}

	// Elapsed badge: hidden during phaseStarting; dimmed when done/cancelled.
	var elapsedBadge string
	if m.phase != phaseStarting {
		elapsedStr := formatElapsed(elapsed)
		elapsedBadge = theme.StyleBadgeTime.Render(fmt.Sprintf(" %s ", elapsedStr))
	}

	badges := []string{
		theme.StyleBadgeIter.Render(fmt.Sprintf(" iter %s ", iterStr)),
	}
	if elapsedBadge != "" {
		badges = append(badges, elapsedBadge)
	}

	// ETA badge: state-driven per UX spec.
	etaSt, etaTime := resolveETA(m.phase, m.estimatedEndAt, m.nowAdjusted())
	switch etaSt {
	case etaUnknown:
		badges = append(badges, theme.StyleBadgeETA.Render(" ETA — "))
	case etaComputable:
		badges = append(badges, theme.StyleBadgeETA.Render(" ETA "+etaTime.Local().Format("15:04")+" "))
	case etaFutureDay:
		badges = append(badges, theme.StyleBadgeETA.Render(" ETA "+etaTime.Local().Format("Mon 15:04")+" "))
	case etaOverdue:
		overdueDur := m.nowAdjusted().Sub(etaTime).Round(time.Second)
		badges = append(badges, theme.StyleBadgeOverdue.Render(" +"+formatElapsed(overdueDur)+" "))
	case etaCompleted:
		badges = append(badges, theme.StyleBadgeETA.Render(" Completed "))
	case etaError:
		badges = append(badges, theme.StyleBadgeETA.Render(" ETA — "))
	// etaHidden and etaCancelled: nothing added
	}
	if m.totalComments > 0 {
		badges = append(badges,
			theme.StyleBadgeComment.Render(fmt.Sprintf(" %d comments ", m.totalComments)))
	}
	if m.rulesExtracted > 0 {
		badges = append(badges,
			theme.StyleBadgeRules.Render(fmt.Sprintf(" +%d rules ", m.rulesExtracted)))
	}

	headerLine2 := "  " + strings.Join(badges, "  ")
	if len(m.iterHistory) > 0 {
		headerLine2 += "   " + m.renderIterTimeline()
	}

	headerWidth := totalW - 2
	if headerWidth < 0 {
		headerWidth = 0
	}
	header := theme.StyleHeader.Width(headerWidth).Render(headerLine1 + "\n" + headerLine2)

	// ── Phase breadcrumb ──────────────────────────────────────────────────────
	breadcrumb := m.renderPhaseBreadcrumb()

	// ── Toast notification ────────────────────────────────────────────────────
	if m.toastMsg != "" {
		toastRendered := theme.StyleToast.Render(m.toastMsg)
		toastW := lipgloss.Width(toastRendered)
		breadcrumbW := lipgloss.Width(breadcrumb)
		gap := totalW - breadcrumbW - toastW - 2
		if gap > 0 {
			breadcrumb = breadcrumb + strings.Repeat(" ", gap) + toastRendered
		}
	}

	// ── Main log viewport ─────────────────────────────────────────────────────
	m.viewport.Width = logW
	m.viewport.Height = logH
	logView := m.viewport.View()

	// ── Reflect panel ─────────────────────────────────────────────────────────
	var body string
	if showPanel {
		reflectView := m.renderReflectPanel(logH)
		body = lipgloss.JoinHorizontal(lipgloss.Top, logView, reflectView)
	} else {
		body = logView
	}

	// ── Status bar ────────────────────────────────────────────────────────────
	var phaseStr string
	if m.statusMsg != "" {
		phaseStr = theme.StyleTeal.Render(m.statusMsg)
	} else if m.done {
		if m.exitCode == 0 {
			phaseStr = theme.StylePhaseDone.Render(theme.IconCheck + " done")
		} else {
			phaseStr = theme.StylePhaseErr.Render(fmt.Sprintf(theme.IconCross+" exited %d", m.exitCode))
		}
	} else if m.phase == phaseWaiting && m.waitMax > 0 {
		phaseStr = m.renderWaitProgress()
	} else {
		phaseStr = m.spinner.View() + " " + renderStatusBadge(m.phase)
	}

	scrollHint := ""
	if !m.atBottom {
		scrollHint = theme.StyleMuted.Render("  ↑ scrolled  G=bottom")
	}

	dot := theme.StyleMuted.Render(" " + theme.IconSep + " ")
	keys := "  " + strings.Join([]string{
		theme.RenderKeyHint("q", "quit"),
		theme.RenderKeyHint("↑↓/jk", "scroll"),
		theme.RenderKeyHint("t", "timing"),
		theme.RenderKeyHint("s", "save reflect"),
		theme.RenderKeyHint("S", "save all"),
		theme.RenderKeyHint("?", "help"),
	}, dot)

	statusBarWidth := totalW - 2
	if statusBarWidth < 0 {
		statusBarWidth = 0
	}
	statusBar := theme.StyleStatusBar.Width(statusBarWidth).Render(phaseStr + scrollHint + keys)

	// ── Tooltip overlay ───────────────────────────────────────────────────────
	var tooltipLine string
	if m.showTooltip {
		tip := renderStatusBadge(m.phase) + "  " +
			theme.StyleMuted.Render(m.phase.String()+" — press t to dismiss")
		tooltipLine = "  " + tip + "\n"
	}

	// ── History panel overlay ─────────────────────────────────────────────────
	var historyBlock string
	if m.showHistory {
		histPanel := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(theme.Mauve).
			Padding(0, 2).
			Render(m.renderHistoryPanel())
		historyBlock = histPanel + "\n"
	}

	return header + "\n" + breadcrumb + "\n" + tooltipLine + historyBlock + body + "\n" + statusBar
}

// lastStateChangedAtForDisplay returns the last state change timestamp on the
// same clock basis used for ETA/elapsed calculations.
func (m monitorModel) lastStateChangedAtForDisplay() time.Time {
	if m.clockOffset == 0 {
		return m.lastStateChangedAt
	}
	return m.lastStateChangedAt.Add(m.clockOffset)
}

// renderTimingTooltip renders the last-state-change tooltip overlay.
// Shows timestamp in UTC and local timezone, matching the UX spec (RIN-42 §3).
func (m monitorModel) renderTimingTooltip() string {
	t := m.lastStateChangedAt
	utcStr := t.UTC().Format("Mon, 02 Jan 2006  15:04:05 UTC")
	localStr := t.Local().Format("15:04:05 MST")

	elapsedStr := formatElapsed(m.elapsedForDisplay())
	elapsedLabel := theme.StyleMuted.Render("Elapsed:") + " " +
		lipgloss.NewStyle().Foreground(theme.Text).Render(elapsedStr)

	label := theme.StyleMuted.Render("Last state change")
	utcLine := lipgloss.NewStyle().Foreground(theme.Text).Render(utcStr)
	localLine := theme.StyleMuted.Render("(local: " + localStr + ")")

	dismissHint := theme.StyleMuted.Render("  any key to dismiss")

	content := label + "\n" + utcLine + "\n" + localLine + "\n\n" + elapsedLabel + "\n\n" + dismissHint

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Surface).
		Padding(1, 3).
		Render(content)

	return box
}

// renderIterTimeline renders a compact horizontal timeline of iteration results.
func (m monitorModel) renderIterTimeline() string {
	var parts []string
	for _, e := range m.iterHistory {
		switch e.result {
		case iterFixed:
			dot := theme.StyleTimelineDot.Render(theme.IconDot)
			if e.comments > 0 {
				dot = theme.StyleTimelineDot.Render(fmt.Sprintf(theme.IconDot+"%d", e.comments))
			}
			parts = append(parts, dot)
		case iterClean:
			parts = append(parts, theme.StyleTimelineDone.Render(theme.IconCircle))
		case iterApproved:
			parts = append(parts, theme.StyleTimelineDone.Render(theme.IconCheck))
		case iterError:
			parts = append(parts, theme.StyleTimelineErr.Render(theme.IconCross))
		case iterRunning:
			parts = append(parts, theme.StyleTimelineCurrent.Render(theme.IconRunning))
		}
	}
	return theme.StyleMuted.Render("history ") + strings.Join(parts, theme.StyleMuted.Render("›"))
}

// renderStatusBadge returns a coloured chip with icon for the given phase.
func renderStatusBadge(p phase) string {
	switch p {
	case phaseStarting:
		return theme.StyleBadgeQueued.Render(theme.IconPending + " queued")
	case phaseWaiting:
		return theme.StyleBadgeStalled.Render(theme.IconRunning + " waiting")
	case phaseFixing:
		return theme.StyleBadgeRunning.Render(theme.IconRadioOn + " fixing")
	case phaseReflecting:
		return theme.StyleBadgeRunning.Render(theme.IconRadioOn + " reflecting")
	case phaseDone:
		return theme.StyleBadgeCompleted.Render(theme.IconCheck + " done")
	case phaseError:
		return theme.StyleBadgeFailed.Render(theme.IconCross + " error")
	}
	return theme.StyleBadgeCancelled.Render(theme.IconCircle + " unknown")
}

// renderHistoryPanel renders a collapsible panel listing iterHistory entries.
func (m monitorModel) renderHistoryPanel() string {
	title := theme.StyleReflectTitle.Render(theme.IconDot + " iteration history")
	if len(m.iterHistory) == 0 {
		return title + "\n" + theme.StyleMuted.Render("  no iterations yet")
	}

	var lines []string
	for _, e := range m.iterHistory {
		var icon, result string
		switch e.result {
		case iterFixed:
			icon = theme.StyleTimelineDot.Render(theme.IconDot)
			result = theme.StylePhaseFixing.Render("fixed")
		case iterClean:
			icon = theme.StyleTimelineDone.Render(theme.IconCircle)
			result = theme.StylePhaseDone.Render("clean")
		case iterApproved:
			icon = theme.StyleTimelineDone.Render(theme.IconCheck)
			result = theme.StylePhaseDone.Render("approved")
		case iterError:
			icon = theme.StyleTimelineErr.Render(theme.IconCross)
			result = theme.StylePhaseErr.Render("error")
		case iterRunning:
			icon = theme.StyleTimelineCurrent.Render(theme.IconRunning)
			result = theme.StylePhaseWaiting.Render("running")
		}
		commentStr := ""
		if e.comments > 0 {
			commentStr = "  " + theme.StyleMuted.Render(fmt.Sprintf("%d comment(s)", e.comments))
		}
		lines = append(lines, fmt.Sprintf("  %s  iter %-3d  %s%s", icon, e.num, result, commentStr))
	}

	return title + "\n" + strings.Join(lines, "\n")
}

// renderPostCycleMenu renders the post-cycle action menu with rounded border.
func (m monitorModel) renderPostCycleMenu() string {
	title := theme.GradientString("PR READY TO MERGE", theme.Mauve, theme.Lavender, true)
	subtitle := theme.StyleMuted.Render("  What would you like to do?")

	var lines []string
	for i, opt := range m.postCycleOptions {
		if i == m.postCycleCursor {
			lines = append(lines, theme.StyleSelected.Render("  "+theme.IconArrow+" "+opt.label))
		} else {
			lines = append(lines, theme.StyleUnselected.Render("    "+opt.label))
		}
	}

	dot := theme.StyleMuted.Render(" " + theme.IconSep + " ")
	hints := "  " + strings.Join([]string{
		theme.RenderKeyHint("↑↓/jk", "move"),
		theme.RenderKeyHint("enter", "confirm"),
		theme.RenderKeyHint("q", "quit"),
	}, dot)

	content := title + "\n" + subtitle + "\n\n" + strings.Join(lines, "\n") + "\n" + hints
	return theme.StyleMenuBox.Render(content)
}

// renderReflectPanel builds the right-side reflection panel.
func (m monitorModel) renderReflectPanel(h int) string {
	panelW := m.reflectPanelWidth()
	var b strings.Builder

	title := theme.StyleReflectTitle.Render(theme.IconRadioOn + " reflect")
	if m.rulesExtracted > 0 {
		title += "  " + theme.StyleBadgeRules.Render(fmt.Sprintf(" +%d ", m.rulesExtracted))
	}
	b.WriteString(title + "\n")

	type displayLine struct {
		text          string
		isLatestEntry bool
		isError       bool
	}
	var displayLines []displayLine
	lastIdx := len(m.reflectLines) - 1
	for i, l := range m.reflectLines {
		isErr := strings.Contains(l, "exited non-zero") ||
			strings.Contains(l, "killed") ||
			strings.Contains(l, "failed")

		var icon string
		switch {
		case isErr:
			icon = theme.StyleReflectFail.Render(theme.IconCross + " ")
		case strings.Contains(l, "complete") || strings.Contains(l, "pushed") || strings.Contains(l, "done"):
			icon = theme.StyleReflectOK.Render(theme.IconCheck + " ")
		case strings.Contains(l, "starting"):
			icon = theme.StyleMuted.Render(theme.IconRunning + " ")
		case strings.Contains(l, "No changes") || strings.Contains(l, "No top-level") || strings.Contains(l, "nothing"):
			icon = theme.StyleMuted.Render(theme.IconCircle + " ")
		default:
			icon = theme.StyleMuted.Render("  ")
		}

		contentW := panelW - 2
		if contentW < 10 {
			contentW = 10
		}
		wrapped := theme.WrapLine(l, contentW)
		if len(wrapped) > 2 {
			wrapped = wrapped[:2]
		}
		for j, wl := range wrapped {
			prefix := "  "
			if j == 0 {
				prefix = icon
			}
			displayLines = append(displayLines, displayLine{
				text:          prefix + wl,
				isLatestEntry: i == lastIdx,
				isError:       isErr,
			})
		}
	}

	maxLines := h - 1
	if maxLines < 1 {
		maxLines = 1
	}
	if len(displayLines) > maxLines {
		displayLines = displayLines[len(displayLines)-maxLines:]
	}

	for _, dl := range displayLines {
		var rendered string
		switch {
		case dl.isError:
			rendered = theme.StyleReflectFail.Render(dl.text)
		case dl.isLatestEntry:
			rendered = theme.StyleReflectNew.Render(dl.text)
		default:
			rendered = theme.StyleReflectLine.Render(dl.text)
		}
		b.WriteString(rendered + "\n")
	}

	written := 1 + len(displayLines)
	for i := written; i < h; i++ {
		b.WriteString("\n")
	}

	return theme.StyleReflectPanel.
		Width(panelW + 3).
		Height(h).
		Render(b.String())
}

func colorLine(line string) string {
	plain := stripANSI(line)
	trimmed := strings.TrimSpace(plain)
	switch {
	case strings.Contains(plain, "❌") || strings.Contains(plain, "ERROR"):
		return theme.StyleLogErr.Render(plain)
	case strings.Contains(plain, "⚠️") || strings.Contains(plain, "WARN"):
		return theme.StyleLogWarn.Render(plain)
	case strings.Contains(plain, "━━━") || strings.Contains(plain, "Iteration"):
		return theme.StyleLogIter.Render(plain)
	case strings.Contains(plain, "✅") || strings.Contains(plain, "APPROVED") ||
		strings.Contains(plain, "Clean review") || strings.Contains(plain, "ready to merge"):
		return theme.StyleLogSuccess.Render(plain)
	case strings.Contains(plain, "git") && (strings.Contains(plain, "push") ||
		strings.Contains(plain, "commit") || strings.Contains(plain, "checkout")):
		return theme.StyleLogGit.Render(plain)
	case strings.Contains(plain, "gh api") || strings.Contains(plain, "gh pr") ||
		strings.Contains(plain, "Copilot review requested"):
		return theme.StyleLogAPI.Render(plain)
	case strings.HasPrefix(trimmed, "→") ||
		strings.HasPrefix(trimmed, "←") ||
		strings.HasPrefix(trimmed, "$") ||
		strings.Contains(plain, "DEBUG"):
		return theme.StyleLogDebug.Render(plain)
	case strings.Contains(plain, "🚀") || strings.Contains(plain, "💬"):
		return theme.StyleLogInfo.Render(plain)
	default:
		return theme.StyleLogAgent.Render(plain)
	}
}

// ── Session extraction ─────────────────────────────────────────────────────────

// extractPatterns derives a best-effort list of top fix patterns from the
// reflect lines collected during the cycle. It de-duplicates short keyword
// phrases and returns up to maxPatterns unique entries.
func extractPatterns(reflectLines []string, maxPatterns int) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, l := range reflectLines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		// Trim common prefixes/suffixes to get the rule description.
		for _, prefix := range []string{"✓ ", "✗ ", "• ", "- ", "* "} {
			l = strings.TrimPrefix(l, prefix)
		}
		// Skip status-only lines.
		lower := strings.ToLower(l)
		if lower == "complete" || lower == "done" || lower == "starting" ||
			strings.Contains(lower, "exited non-zero") ||
			strings.Contains(lower, "rule(s) pushed") ||
			strings.Contains(lower, "no changes") ||
			strings.Contains(lower, "nothing to") {
			continue
		}
		// Truncate to a readable length.
		if len(l) > 60 {
			l = l[:57] + "..."
		}
		if _, ok := seen[l]; ok {
			continue
		}
		seen[l] = struct{}{}
		out = append(out, l)
		if len(out) >= maxPatterns {
			break
		}
	}
	return out
}

// ── RunMonitor ────────────────────────────────────────────────────────────────

// RunMonitor starts the runner script and displays the live TUI monitor.
// When sendNotify is true, a native desktop notification is sent on cycle completion.
func RunMonitor(pr, repo, runnerName, modelName, prTitle, cwd string, autoMerge, sendNotify bool, runnerArgs []string) error {
	cmd := exec.Command(runnerArgs[0], runnerArgs[1:]...)
	cmd.Stdin = os.Stdin

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start runner: %w", err)
	}

	lineCh := make(chan string, 512)
	doneCh := make(chan int, 1)

	readPipe := func(r io.Reader, wg *sync.WaitGroup) {
		defer wg.Done()
		reader := bufio.NewReader(r)
		for {
			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				lineCh <- strings.TrimRight(line, "\r\n")
			}
			if err != nil {
				if err != io.EOF {
					lineCh <- fmt.Sprintf("[monitor] pipe read error: %v", err)
				}
				return
			}
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go readPipe(stdoutPipe, &wg)
	go readPipe(stderrPipe, &wg)

	go func() {
		wg.Wait()
		close(lineCh)
		exitCode := 0
		if err := cmd.Wait(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 1
			}
		}
		doneCh <- exitCode
	}()

	cm := newChannelMonitor(pr, repo, runnerName, modelName, prTitle, cwd, autoMerge, lineCh, doneCh)

	p := tea.NewProgram(cm, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return err
	}

	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}

	// ── Post-cycle insight summary ────────────────────────────────────────────
	// Build and persist a session record, then print the human-readable summary.
	if fm, ok := finalModel.(channelMonitor); ok {
		mm := fm.monitorModel
		// Only record sessions for terminal outcomes (done or error).
		if mm.done {
			commentsByRound := make([]int, len(mm.iterHistory))
			for i, e := range mm.iterHistory {
				commentsByRound[i] = e.comments
			}
			patterns := extractPatterns(mm.reflectLines, 5)
			sess := session.Session{
				PR:              mm.pr,
				Repo:            mm.repo,
				RunnerName:      mm.runner,
				StartedAt:       mm.started,
				EndedAt:         time.Now(),
				Approved:        mm.phase == phaseDone && mm.exitCode == 0,
				Iterations:      mm.iter,
				TotalComments:   mm.totalComments,
				RulesExtracted:  mm.rulesExtracted,
				CommentsByRound: commentsByRound,
				Patterns:        patterns,
			}
			// Persist — non-fatal on failure.
			if saveErr := sess.Save(); saveErr != nil {
				fmt.Fprintf(os.Stderr, "rinse: could not save session: %v\n", saveErr)
			}
			// Print the summary only on successful completion.
			if mm.exitCode == 0 {
				session.PrintSummary(sess, false)
			}

			// Send desktop notification (best-effort, opt-in via --notify).
			if sendNotify {
				elapsed := sess.EndedAt.Sub(sess.StartedAt)
				var result notify.CycleResult
				switch {
				case sess.Approved:
					result = notify.ResultApproved
				case mm.exitCode != 0:
					result = notify.ResultError
				default:
					result = notify.ResultMaxIterations
				}
				notify.CycleNotification(true, notify.CycleParams{
					PR:            pr,
					Repo:          repo,
					Result:        result,
					Iterations:    sess.Iterations,
					CommentsFixed: sess.TotalComments,
					CommentsLeft:  mm.currentComments,
					Elapsed:       elapsed,
				})
			}
		}
	}

	return nil
}

// ── channelMonitor ────────────────────────────────────────────────────────────

type channelMonitor struct {
	monitorModel
	lineCh <-chan string
	doneCh <-chan int
}

func newChannelMonitor(pr, repo, runnerName, modelName, prTitle, cwd string, autoMerge bool,
	lineCh <-chan string, doneCh <-chan int) channelMonitor {
	return channelMonitor{
		monitorModel: newMonitorModel(pr, repo, runnerName, modelName, prTitle, cwd, autoMerge, nil),
		lineCh:       lineCh,
		doneCh:       doneCh,
	}
}

func (m channelMonitor) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, tick(), m.poll())
}

func (m channelMonitor) poll() tea.Cmd {
	return func() tea.Msg {
		select {
		case line, ok := <-m.lineCh:
			if !ok {
				code := <-m.doneCh
				return runnerDoneMsg{exitCode: code}
			}
			return logLineMsg(line)
		case <-time.After(50 * time.Millisecond):
			return nil
		}
	}
}

func (m channelMonitor) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	updated, cmd := m.monitorModel.Update(msg)
	m.monitorModel = updated.(monitorModel)
	cmds = append(cmds, cmd)

	switch msg.(type) {
	case tea.KeyMsg:
	case logLineMsg:
		cmds = append(cmds, m.poll())
	case runnerDoneMsg:
	default:
		cmds = append(cmds, m.poll())
	}

	return m, tea.Batch(cmds...)
}

func (m channelMonitor) View() string {
	return m.monitorModel.View()
}

// sessionOutcome maps the final monitor state to a stats.Outcome string.
func sessionOutcome(m monitorModel) stats.Outcome {
	if !m.done {
		return stats.OutcomeAborted
	}
	// Scan final log lines for terminal signals before falling back to exit code
	// or iterHistory, because some runner outcomes are communicated via logs.
	for i := len(m.lines) - 1; i >= 0 && i >= len(m.lines)-10; i-- {
		plain := stripANSI(m.lines[i])
		lower := strings.ToLower(plain)
		if strings.Contains(lower, "[dry run] exiting") {
			return stats.OutcomeDryRun
		}
		if strings.Contains(lower, "pr merged") || strings.Contains(plain, "🎉") {
			return stats.OutcomeMerged
		}
		if strings.Contains(lower, "pr closed") || strings.Contains(plain, "📕") {
			return stats.OutcomeClosed
		}
		if strings.Contains(lower, "max iterations") || strings.Contains(lower, "max iteration") {
			return stats.OutcomeMaxIter
		}
	}
	if m.exitCode != 0 {
		return stats.OutcomeError
	}
	if len(m.iterHistory) == 0 {
		return stats.OutcomeClean
	}
	last := m.iterHistory[len(m.iterHistory)-1].result
	switch last {
	case iterApproved:
		return stats.OutcomeApproved
	case iterClean:
		return stats.OutcomeClean
	default:
		return stats.OutcomeError
	}
}
