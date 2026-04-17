package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Additional palette ────────────────────────────────────────────────────────

var (
	green = lipgloss.Color("#A6DA95")
	peach = lipgloss.Color("#F5A97F")
	sky   = lipgloss.Color("#91D7E3")
)

// ── Monitor styles ────────────────────────────────────────────────────────────

var (
	// Header: borderBottom separates it from the log area cleanly.
	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(text).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(overlay).
			Padding(0, 1)

	styleHeaderLabel = lipgloss.NewStyle().Foreground(overlay)
	styleHeaderVal   = lipgloss.NewStyle().Foreground(lavender).Bold(true)

	// Status bar: borderTop, no background.
	styleStatusBar = lipgloss.NewStyle().
			Foreground(subtext).
			BorderStyle(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(overlay).
			Padding(0, 1)

	stylePhaseWaiting = lipgloss.NewStyle().Foreground(yellow).Bold(true)
	stylePhaseFixing  = lipgloss.NewStyle().Foreground(mauve).Bold(true)
	stylePhaseReflect = lipgloss.NewStyle().Foreground(teal).Bold(true)
	stylePhaseDone    = lipgloss.NewStyle().Foreground(green).Bold(true)
	stylePhaseErr     = lipgloss.NewStyle().Foreground(red).Bold(true)

	// Log line colours — semantic categories.
	styleLogInfo    = lipgloss.NewStyle().Foreground(text)
	styleLogDebug   = lipgloss.NewStyle().Foreground(subtext)
	styleLogWarn    = lipgloss.NewStyle().Foreground(yellow)
	styleLogErr     = lipgloss.NewStyle().Foreground(red).Bold(true)
	styleLogIter    = lipgloss.NewStyle().Foreground(mauve).Bold(true)
	styleLogAgent   = lipgloss.NewStyle().Foreground(text)
	styleLogSuccess = lipgloss.NewStyle().Foreground(green).Bold(true)
	styleLogGit     = lipgloss.NewStyle().Foreground(peach)
	styleLogAPI     = lipgloss.NewStyle().Foreground(sky)

	// Stat badge styles.
	styleBadge = lipgloss.NewStyle().
			Foreground(crust).
			Padding(0, 1)
	styleBadgeIter    = styleBadge.Background(mauve)
	styleBadgeComment = styleBadge.Background(yellow)
	styleBadgeRules   = styleBadge.Background(teal)
	styleBadgeTime    = styleBadge.Background(lavender)

	// Reflect panel styles.
	styleReflectPanel = lipgloss.NewStyle().
				BorderStyle(lipgloss.NormalBorder()).
				BorderLeft(true).
				BorderForeground(teal).
				Padding(0, 1)
	styleReflectTitle = lipgloss.NewStyle().Foreground(teal).Bold(true)
	styleReflectLine  = lipgloss.NewStyle().Foreground(subtext)
	styleReflectNew   = lipgloss.NewStyle().Foreground(text)
	styleReflectOK    = lipgloss.NewStyle().Foreground(green)
	styleReflectFail  = lipgloss.NewStyle().Foreground(red)

	// Iteration timeline styles.
	styleTimelineDot     = lipgloss.NewStyle().Foreground(mauve)
	styleTimelineDone    = lipgloss.NewStyle().Foreground(green)
	styleTimelineErr     = lipgloss.NewStyle().Foreground(red)
	styleTimelineCurrent = lipgloss.NewStyle().Foreground(yellow).Bold(true)

	// Toast notification style.
	styleToast = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(green).
			Padding(0, 2).
			Foreground(text).
			Bold(true)
)

// ansiRe strips ANSI escape sequences for pattern matching only.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[mK]`)

// commentCountRe matches "N comment(s)" to extract the comment count.
// Anchoring to the number immediately before "comment(s)" avoids mistaking
// timestamp digits (e.g. "15:04:05") for the count.
var commentCountRe = regexp.MustCompile(`(\d+)\s+comment\(s\)`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// ── Phase ─────────────────────────────────────────────────────────────────────

type phase int

const (
	phaseStarting phase = iota
	phaseWaiting
	phaseFixing
	phaseReflecting
	phaseDone
	phaseError
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
	}
	return ""
}

func (p phase) Style() lipgloss.Style {
	switch p {
	case phaseStarting:
		return stylePhaseWaiting
	case phaseWaiting:
		return stylePhaseWaiting
	case phaseFixing:
		return stylePhaseFixing
	case phaseReflecting:
		return stylePhaseReflect
	case phaseDone:
		return stylePhaseDone
	case phaseError:
		return stylePhaseErr
	}
	return stylePhaseWaiting
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
	comments int // number of comments in this iteration
}

// ── Post-cycle menu ───────────────────────────────────────────────────────────

// postCycleOption describes one action in the post-cycle Bubble Tea menu.
type postCycleOption struct {
	label string
}

// postCycleMenuOptions are shown when the runner exits 0 and signals "ready to merge".
// The CWD and default branch are filled in at runtime.
func buildPostCycleOptions(defaultBranch string) []postCycleOption {
	return []postCycleOption{
		{label: "Merge PR + delete remote & local branch + checkout → " + defaultBranch},
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
	cwd       string // local checkout path (for post-cycle git ops)
	autoMerge bool   // when true, runner handles merge; suppress TUI post-cycle menu

	// state
	width        int
	height       int
	phase        phase
	iter         int
	started      time.Time
	lines        []string         // all main log lines
	reflectLines []string         // lines tagged [reflect]
	renderedLog  *strings.Builder // cached rendered content of lines (appended incrementally, O(1) amortized)

	// stats tracking
	totalComments   int         // total comments seen across all iterations
	rulesExtracted  int         // rules pushed by reflect agent
	iterHistory     []iterEntry // result of each completed iteration
	currentComments int         // comments in current iteration

	// wait progress (Copilot reviewing)
	waitElapsed int    // seconds elapsed in current wait
	waitMax     int    // max wait seconds (e.g. 300)
	waitLabel   string // e.g. "Copilot reviewing"

	// toast notification
	toastMsg string

	// post-cycle menu
	readyToMerge       bool // runner signalled "ready to merge"
	showPostCycleMenu  bool // display the Bubble Tea merge menu
	postCycleCursor    int  // selected menu item index
	postCycleOptions   []postCycleOption
	postCycleDefaultBr string // default branch detected from log / git

	// sub-components
	viewport  viewport.Model
	spinner   spinner.Model
	atBottom  bool
	showHelp  bool
	statusMsg string

	// runner process (only used in base model for q-quit)
	cmd      *exec.Cmd
	exitCode int
	done     bool
}

func newMonitorModel(pr, repo, runnerName, modelName, prTitle, cwd string, autoMerge bool, cmd *exec.Cmd) monitorModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(mauve)

	vp := viewport.New(80, 20)
	vp.Style = lipgloss.NewStyle().Foreground(text)

	// Try to detect default branch from local git; fall back to "main".
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
	}
}

// detectLocalDefaultBranch returns the default branch name from the local git
// worktree at cwd, falling back to "main" if detection fails.
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
	// ref looks like "refs/remotes/origin/main"
	if idx := strings.LastIndex(ref, "/"); idx >= 0 {
		return ref[idx+1:]
	}
	return "main"
}

// ── Init ──────────────────────────────────────────────────────────────────────

func (m monitorModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, tick())
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// ── Layout helpers ────────────────────────────────────────────────────────────

// reflectPanelWidth returns the inner content width of the reflect panel,
// proportional to the terminal width (28%, clamped 35–55).
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

// logWidth returns the width available to the main log viewport.
// When the reflect panel is hidden, the full terminal width is used.
func (m monitorModel) logWidth() int {
	if m.width <= 0 {
		return 80
	}
	if !m.showReflectPanel() {
		return m.width
	}
	rpw := m.reflectPanelWidth()
	w := m.width - (rpw + 3) // 3 = border(1) + padding(2)
	if w < 1 {
		w = 1
	}
	return w
}

// showReflectPanel reports whether the terminal is wide enough for the split view.
func (m monitorModel) showReflectPanel() bool {
	return m.width > 90
}

// logHeight returns viewport height.
// Reserved rows: header(2) + header border(1) + breadcrumb(1) + statusbar border(1) + statusbar(1) = 6.
func (m monitorModel) logHeight() int {
	h := m.height - 6
	if h < 4 {
		h = 4
	}
	return h
}

// ── Phase breadcrumb ──────────────────────────────────────────────────────────

// renderPhaseBreadcrumb renders a horizontal timeline showing all phases,
// marking completed ones with ✓, the current one with ◉, and future ones with ○.
func (m monitorModel) renderPhaseBreadcrumb() string {
	ordered := []phase{phaseStarting, phaseWaiting, phaseFixing, phaseReflecting, phaseDone}
	names := []string{"start", "waiting", "fixing", "reflect", "done"}

	// For ordering, phaseError occupies the phaseDone slot.
	currentPhase := m.phase
	if currentPhase == phaseError {
		currentPhase = phaseDone
	}

	var parts []string
	for i, p := range ordered {
		var part string
		switch {
		case m.phase == phaseError && p == phaseDone:
			part = stylePhaseErr.Render("✗ error")
		case p < currentPhase:
			part = styleLogDebug.Render("✓ " + names[i])
		case p == currentPhase:
			part = m.phase.Style().Render("◉ " + names[i])
		default:
			part = styleMuted.Render("○ " + names[i])
		}
		parts = append(parts, part)
	}

	sep := styleMuted.Render("  ›  ")
	return "  " + strings.Join(parts, sep)
}

// ── Help overlay ──────────────────────────────────────────────────────────────

func (m monitorModel) renderHelp() string {
	helpStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(mauve).
		Padding(1, 4)

	title := styleStep.Render("keyboard shortcuts")

	type krow struct{ key, desc string }
	rows := []krow{
		{"↑ / k", "scroll up"},
		{"↓ / j", "scroll down"},
		{"g", "jump to top"},
		{"G", "jump to bottom"},
		{"S", "save full session log to file"},
		{"s", "save reflect log to file"},
		{"?", "toggle this help"},
		{"q / ^C", "quit"},
	}

	var lines []string
	for _, r := range rows {
		lines = append(lines,
			styleMuted.Render(fmt.Sprintf("%-10s", r.key))+"  "+styleVal.Render(r.desc))
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
		key := msg.String()
		// Always handle quit.
		if key == "ctrl+c" || key == "q" {
			if m.cmd != nil && m.cmd.Process != nil {
				_ = m.cmd.Process.Kill()
			}
			return m, tea.Quit
		}

		// Post-cycle menu captures all other keys when visible.
		if m.showPostCycleMenu {
			return m.handlePostCycleKey(key)
		}

		// Toggle help overlay.
		if key == "?" {
			m.showHelp = !m.showHelp
		} else if m.showHelp {
			// Any other key dismisses the overlay.
			m.showHelp = false
		} else {
			// Normal key handling when help is not shown.
			switch key {
			case "G":
				m.atBottom = true
				m.viewport.GotoBottom()
			case "g":
				m.atBottom = false
				m.viewport.GotoTop()
			case "s":
				if len(m.reflectLines) > 0 {
					fname := fmt.Sprintf("pr-review-reflect-%s.txt",
						time.Now().Format("20060102-150405"))
					content := strings.Join(m.reflectLines, "\n") + "\n"
					if err := os.WriteFile(fname, []byte(content), 0o644); err != nil {
						m.statusMsg = "✗ save failed"
					} else {
						m.statusMsg = "✓ reflect log → " + fname
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
				// Save the rendered main log so the file matches what was shown
				// in the viewport, including lines appended outside m.lines.
				mainFname := fmt.Sprintf("pr-review-log-%s.txt", ts)
				mainContent := m.renderedLog.String()
				if mainContent != "" && !strings.HasSuffix(mainContent, "\n") {
					mainContent += "\n"
				}
				var savedParts []string
				if err := os.WriteFile(mainFname, []byte(mainContent), 0o644); err != nil {
					m.statusMsg = "✗ save failed"
				} else {
					savedParts = append(savedParts, mainFname)
				}
				// Save reflect log if present
				if len(m.reflectLines) > 0 {
					refFname := fmt.Sprintf("pr-review-reflect-%s.txt", ts)
					refContent := strings.Join(m.reflectLines, "\n") + "\n"
					if err := os.WriteFile(refFname, []byte(refContent), 0o644); err == nil {
						savedParts = append(savedParts, refFname)
					}
				}
				if len(savedParts) > 0 {
					m.statusMsg = "✓ saved → " + strings.Join(savedParts, ", ")
				}
				cmds = append(cmds, tea.Tick(3*time.Second,
					func(t time.Time) tea.Msg { return clearStatusMsg{} }))
			default:
				var vpcmd tea.Cmd
				m.viewport, vpcmd = m.viewport.Update(msg)
				m.atBottom = m.viewport.AtBottom()
				cmds = append(cmds, vpcmd)
			}
		}

	case tickMsg:
		cmds = append(cmds, tick())

	case spinner.TickMsg:
		var spcmd tea.Cmd
		m.spinner, spcmd = m.spinner.Update(msg)
		cmds = append(cmds, spcmd)

	case logLineMsg:
		raw := string(msg)
		plain := stripANSI(raw)

		// Detect "ready to merge" signals from the runner.
		if isReadyToMerge(plain) {
			m.readyToMerge = true
			m.toastMsg = "✅  PR ready to merge!"
			cmds = append(cmds, tea.Tick(4*time.Second,
				func(t time.Time) tea.Msg { return clearToastMsg{} }))
		}

		// Track comment counts from log lines.
		if strings.Contains(plain, "comment(s) in review") {
			// Parse "N comment(s)" by scanning the substring after "💬" so that
			// timestamps like "[2006-01-02 15:04:05]" earlier in the line cannot
			// be mistaken for the count.
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

		// Track rule extraction from reflect agent.
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
				m.toastMsg = fmt.Sprintf("◎  +%d rule(s) extracted", n)
				cmds = append(cmds, tea.Tick(3*time.Second,
					func(t time.Time) tea.Msg { return clearToastMsg{} }))
			}
		}

		// Track iteration results for the timeline.
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
			m.toastMsg = "🎉  Copilot APPROVED!"
			cmds = append(cmds, tea.Tick(5*time.Second,
				func(t time.Time) tea.Msg { return clearToastMsg{} }))
		}
		if strings.Contains(plain, "Clean review") {
			if len(m.iterHistory) == 0 || m.iterHistory[len(m.iterHistory)-1].result != iterClean {
				entry := iterEntry{num: m.iter, result: iterClean}
				m.iterHistory = append(m.iterHistory, entry)
			}
		}

		// Suppress noisy poll-tick lines ("⏳ Copilot reviewing... (Xs / Ys)") from
		// the log viewport. Instead, parse the progress and render it as an
		// animated progress bar in the status bar.
		if isWaitTickLine(plain) {
			var e, mx int
			if _, err := fmt.Sscanf(extractWaitProgress(plain), "%ds / %ds", &e, &mx); err == nil {
				m.waitElapsed = e
				m.waitMax = mx
			}
			// Extract the label (e.g. "Copilot reviewing" / "Copilot reviewing (retry)")
			if idx := strings.Index(plain, "⏳"); idx >= 0 {
				after := strings.TrimSpace(plain[idx+len("⏳"):])
				if dotIdx := strings.Index(after, "..."); dotIdx > 0 {
					m.waitLabel = after[:dotIdx]
				}
			}
			// Don't append to log — it's shown in the status bar.
			// Still update phase.
			m.phase = inferPhase(plain, m.phase)
			// Skip the rest of the logLineMsg handler for this line.
			break
		}

		// Clear wait progress only when the phase transitions away from waiting.
		nextPhase := inferPhase(plain, m.phase)
		if m.waitMax > 0 && nextPhase != phaseWaiting {
			m.waitElapsed = 0
			m.waitMax = 0
			m.waitLabel = ""
		}

		// Route [reflect]-tagged lines to the side panel when it is visible.
		// When the panel is hidden (narrow terminal or before first WindowSizeMsg),
		// also send them to the main log so they remain visible.
		if isReflectLine(plain) {
			entry := extractReflectEntry(plain)
			m.reflectLines = append(m.reflectLines, entry)
			if !m.showReflectPanel() {
				// Panel is hidden — keep reflect lines visible in the main log too.
				m.lines = append(m.lines, raw)
				m.renderedLog.WriteString(colorLine(raw) + "\n")
			}
		} else {
			m.lines = append(m.lines, raw)
			// Append only the new line to the cached rendered buffer (O(1) amortized per line).
			m.renderedLog.WriteString(colorLine(raw) + "\n")
		}

		m.phase = nextPhase

		// Detect iteration number from separator lines.
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
			m.phase = phaseDone
			// Show the Bubble Tea post-cycle menu when the runner signalled readiness,
			// but only when auto-merge is off (auto-merge means the runner already
			// handled merge + cleanup itself).
			if m.readyToMerge && !m.autoMerge {
				m.showPostCycleMenu = true
				m.postCycleCursor = 0
				m.postCycleOptions = buildPostCycleOptions(m.postCycleDefaultBr)
			}
		} else {
			m.phase = phaseError
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

// isReadyToMerge returns true when the log line signals that the PR is approved
// / clean and ready for the user to act on.
func isReadyToMerge(plain string) bool {
	return strings.Contains(plain, "Clean review") ||
		strings.Contains(plain, "ready to merge") ||
		(strings.Contains(plain, "APPROVED") && strings.Contains(plain, "PR"))
}

// handlePostCycleKey processes keyboard input when the post-cycle menu is shown.
func (m monitorModel) handlePostCycleKey(key string) (tea.Model, tea.Cmd) {
	n := len(m.postCycleOptions)
	switch key {
	case "up", "k":
		if m.postCycleCursor > 0 {
			m.postCycleCursor--
		}
	case "down", "j":
		if m.postCycleCursor < n-1 {
			m.postCycleCursor++
		}
	case "enter":
		return m.executePostCycleAction(m.postCycleCursor)
	case "esc":
		// Dismiss menu — return to the completed log view; press q to fully quit.
		m.showPostCycleMenu = false
		return m, nil
	}
	return m, nil
}

// executePostCycleAction runs the chosen merge action asynchronously.
func (m monitorModel) executePostCycleAction(choice int) (tea.Model, tea.Cmd) {
	pr := m.pr
	repo := m.repo
	cwd := m.cwd
	defaultBr := m.postCycleDefaultBr

	m.showPostCycleMenu = false

	switch choice {
	case 0: // Full cleanup: merge + delete remote branch + checkout default branch
		return m, func() tea.Msg {
			out, err := runShell("gh", "pr", "merge", pr, "--repo", repo, "--merge", "--delete-branch", "--yes")
			if err != nil {
				return actionDoneMsg{output: out, err: err}
			}
			// Detect local branch; only attempt checkout+delete when detection succeeds.
			localBranch, revErr := runShell("git", "-C", cwd, "rev-parse", "--abbrev-ref", "HEAD")
			if revErr != nil {
				return actionDoneMsg{output: "✅ Merged, remote branch deleted. (local branch cleanup skipped: " + strings.TrimSpace(localBranch) + ")"}
			}
			localBranch = strings.TrimSpace(localBranch)
			if localBranch == "" || localBranch == defaultBr {
				return actionDoneMsg{output: "✅ Merged, remote branch deleted."}
			}
			if _, coErr := runShell("git", "-C", cwd, "checkout", defaultBr); coErr != nil {
				return actionDoneMsg{output: "✅ Merged, remote branch deleted. (checkout " + defaultBr + " failed: " + coErr.Error() + ")"}
			}
			if _, delErr := runShell("git", "-C", cwd, "branch", "-d", localBranch); delErr != nil {
				// Fall back to force-delete (-D) like the bash implementation.
				if _, delErrF := runShell("git", "-C", cwd, "branch", "-D", localBranch); delErrF != nil {
					return actionDoneMsg{output: "✅ Merged, remote branch deleted. (local branch delete failed: " + delErrF.Error() + ")"}
				}
			}
			return actionDoneMsg{output: "✅ Merged, remote branch deleted, local branch deleted."}
		}
	case 1: // Merge PR only
		return m, func() tea.Msg {
			out, err := runShell("gh", "pr", "merge", pr, "--repo", repo, "--merge", "--yes")
			return actionDoneMsg{output: out, err: err}
		}
	case 2: // Open in browser
		return m, func() tea.Msg {
			out, err := runShell("gh", "pr", "view", pr, "--repo", repo, "--web")
			return actionDoneMsg{output: out, err: err}
		}
	default: // Do nothing
		return m, tea.Quit
	}
}

// runShell executes a command and returns combined stdout+stderr output.
func runShell(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// isReflectLine returns true when the log line is from the reflection agent,
// regardless of which icon variant (◎ success, ✗ error, ○ skip) was used.
func isReflectLine(plain string) bool {
	return strings.Contains(plain, "[reflect]") ||
		strings.Contains(plain, "◎ reflect") ||
		strings.Contains(plain, "✗ reflect") ||
		strings.Contains(plain, "○ reflect")
}

// isWaitTickLine returns true for the repeating "⏳ Copilot reviewing... (Xs / Ys)" poll lines.
func isWaitTickLine(plain string) bool {
	return strings.Contains(plain, "⏳") &&
		strings.Contains(plain, "reviewing") &&
		strings.Contains(plain, "s /")
}

// extractWaitProgress returns the "(Xs / Ys)" portion from a wait tick line.
func extractWaitProgress(plain string) string {
	if idx := strings.LastIndex(plain, "("); idx >= 0 {
		if end := strings.Index(plain[idx:], ")"); end >= 0 {
			return plain[idx+1 : idx+end]
		}
	}
	return ""
}

// renderWaitProgress renders a compact animated progress bar for the status bar.
// Format: ⏳ Copilot reviewing  ████████░░░░░░░░░░  45s / 300s (15%)
func (m monitorModel) renderWaitProgress() string {
	label := m.waitLabel
	if label == "" {
		label = "Copilot reviewing"
	}
	elapsed := m.waitElapsed
	max := m.waitMax
	if max < 1 {
		max = 1
	}

	// Bar dimensions
	barW := 20
	filled := elapsed * barW / max
	if filled > barW {
		filled = barW
	}
	empty := barW - filled
	if empty < 0 {
		empty = 0
	}
	pct := elapsed * 100 / max

	bar := stylePhaseWaiting.Render(strings.Repeat("█", filled)) +
		styleMuted.Render(strings.Repeat("░", empty))

	return m.spinner.View() + " " +
		stylePhaseWaiting.Render(label) + "  " +
		bar + "  " +
		styleMuted.Render(fmt.Sprintf("%ds / %ds (%d%%)", elapsed, max, pct))
}

// inferPhase maps plain-text log line content to a phase.
func inferPhase(plain string, current phase) phase {
	switch {
	case current == phaseDone:
		return phaseDone

	case strings.Contains(plain, "APPROVED"):
		return phaseDone

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

// extractReflectEntry trims the timestamp/prefix from a [reflect] line.
func extractReflectEntry(plain string) string {
	if idx := strings.Index(plain, "[reflect]"); idx >= 0 {
		msg := strings.TrimSpace(plain[idx+len("[reflect]"):])
		return msg
	}
	// Match all icon variants: ◎ (success), ✗ (error), ○ (skip)
	for _, prefix := range []string{"◎ reflect", "✗ reflect", "○ reflect"} {
		if idx := strings.Index(plain, prefix); idx >= 0 {
			msg := strings.TrimSpace(plain[idx+len(prefix):])
			if len(msg) > 0 && (msg[0] == '|' || strings.HasPrefix(msg, "│")) {
				if msg[0] == '|' {
					msg = strings.TrimSpace(msg[1:])
				} else {
					// │ is multi-byte UTF-8
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

	// Help overlay replaces the entire view.
	if m.showHelp {
		h := m.height
		if h <= 0 {
			h = 24
		}
		return lipgloss.Place(totalW, h, lipgloss.Center, lipgloss.Center, m.renderHelp())
	}

	// Post-cycle menu overlay replaces the entire view.
	if m.showPostCycleMenu {
		h := m.height
		if h <= 0 {
			h = 24
		}
		return lipgloss.Place(totalW, h, lipgloss.Center, lipgloss.Center, m.renderPostCycleMenu())
	}

	showPanel := m.showReflectPanel()
	logW := m.logWidth()
	if !showPanel {
		logW = totalW
	}
	logH := m.logHeight()

	// ── Header row 1: PR info ─────────────────────────────────────────────────
	elapsed := time.Since(m.started).Round(time.Second)

	titlePart := ""
	if m.prTitle != "" {
		titlePart = "  " + styleHeaderLabel.Render(`"`) +
			styleHeaderVal.Render(truncate(m.prTitle, 36)) +
			styleHeaderLabel.Render(`"`)
	}

	headerWidth := totalW - 2
	if headerWidth < 0 {
		headerWidth = 0
	}

	headerLine1 := styleHeaderLabel.Render("pr") + " " + styleHeaderVal.Render("#"+m.pr) + titlePart +
		"  " + styleHeaderLabel.Render("repo") + " " + styleHeaderVal.Render(m.repo) +
		"  " + styleHeaderLabel.Render("runner") + " " + styleHeaderVal.Render(m.runner)

	// ── Header row 2: Stats badges ────────────────────────────────────────────
	iterStr := "-"
	if m.iter > 0 {
		iterStr = fmt.Sprintf("%d", m.iter)
	}

	badges := []string{
		styleBadgeIter.Render(fmt.Sprintf(" iter %s ", iterStr)),
		styleBadgeTime.Render(fmt.Sprintf(" %s ", elapsed.String())),
	}
	if m.totalComments > 0 {
		badges = append(badges,
			styleBadgeComment.Render(fmt.Sprintf(" %d comments ", m.totalComments)))
	}
	if m.rulesExtracted > 0 {
		badges = append(badges,
			styleBadgeRules.Render(fmt.Sprintf(" +%d rules ", m.rulesExtracted)))
	}

	headerLine2 := "  " + strings.Join(badges, "  ")
	if len(m.iterHistory) > 0 {
		headerLine2 += "   " + m.renderIterTimeline()
	}

	header := styleHeader.Width(headerWidth).Render(headerLine1 + "\n" + headerLine2)

	// ── Phase breadcrumb ──────────────────────────────────────────────────────
	breadcrumb := m.renderPhaseBreadcrumb()

	// ── Toast notification (floating, overlaid on breadcrumb line) ─────────────
	if m.toastMsg != "" {
		toastRendered := styleToast.Render(m.toastMsg)
		// Right-align the toast on the breadcrumb line
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

	// ── Reflect panel (only when terminal is wide enough) ─────────────────────
	var body string
	if showPanel {
		reflectView := m.renderReflectPanel(logH)
		body = lipgloss.JoinHorizontal(lipgloss.Top, logView, reflectView)
	} else {
		body = logView
	}

	// ── Status bar (full width) ───────────────────────────────────────────────
	var phaseStr string
	if m.statusMsg != "" {
		phaseStr = styleTeal.Render(m.statusMsg)
	} else if m.done {
		if m.exitCode == 0 {
			phaseStr = stylePhaseDone.Render("✓ done")
		} else {
			phaseStr = stylePhaseErr.Render(fmt.Sprintf("✗ exited %d", m.exitCode))
		}
	} else if m.phase == phaseWaiting && m.waitMax > 0 {
		// Render animated progress bar for the wait phase.
		phaseStr = m.renderWaitProgress()
	} else {
		phaseStr = m.spinner.View() + " " + m.phase.Style().Render(m.phase.String())
	}

	scrollHint := ""
	if !m.atBottom {
		scrollHint = styleMuted.Render("  ↑ scrolled  G=bottom")
	}
	keys := styleMuted.Render("  q=quit  ↑↓/jk=scroll  s=save reflect  S=save all  ?=help")

	statusBarWidth := totalW - 2
	if statusBarWidth < 0 {
		statusBarWidth = 0
	}
	statusBar := styleStatusBar.Width(statusBarWidth).Render(phaseStr + scrollHint + keys)

	return header + "\n" + breadcrumb + "\n" + body + "\n" + statusBar
}

// renderIterTimeline renders a compact horizontal timeline of iteration results.
// Each iteration is rendered as: ● or ●N (fixed), ○ (clean), ✓ (approved), ✗ (error), ◌ (running).
func (m monitorModel) renderIterTimeline() string {
	var parts []string
	for _, e := range m.iterHistory {
		switch e.result {
		case iterFixed:
			dot := styleTimelineDot.Render("●")
			if e.comments > 0 {
				dot = styleTimelineDot.Render(fmt.Sprintf("●%d", e.comments))
			}
			parts = append(parts, dot)
		case iterClean:
			parts = append(parts, styleTimelineDone.Render("○"))
		case iterApproved:
			parts = append(parts, styleTimelineDone.Render("✓"))
		case iterError:
			parts = append(parts, styleTimelineErr.Render("✗"))
		case iterRunning:
			parts = append(parts, styleTimelineCurrent.Render("◌"))
		}
	}
	return styleMuted.Render("history ") + strings.Join(parts, styleMuted.Render("→"))
}

// renderPostCycleMenu renders the centered Bubble Tea post-cycle action menu.
func (m monitorModel) renderPostCycleMenu() string {
	menuStyle := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(teal).
		Padding(1, 4)

	title := styleTeal.Render("✅  PR ready to merge — what would you like to do?")

	var lines []string
	for i, opt := range m.postCycleOptions {
		if i == m.postCycleCursor {
			lines = append(lines, styleSelected.Render("  ❯ "+opt.label))
		} else {
			lines = append(lines, styleUnselected.Render("    "+opt.label))
		}
	}

	hint := styleMuted.Render("\n  ↑↓ / jk to move · enter to confirm · q to quit")
	content := title + "\n\n" + strings.Join(lines, "\n") + hint
	return menuStyle.Render(content)
}

// renderReflectPanel builds the right-side reflection panel with word-wrapped entries.
func (m monitorModel) renderReflectPanel(h int) string {
	panelW := m.reflectPanelWidth()
	var b strings.Builder

	// Title with rules badge
	title := styleReflectTitle.Render("◎ reflect")
	if m.rulesExtracted > 0 {
		title += "  " + styleBadgeRules.Render(fmt.Sprintf(" +%d ", m.rulesExtracted))
	}
	b.WriteString(title + "\n")

	// Expand all reflect lines with word-wrap, capped at 2 display lines per entry.
	type displayLine struct {
		text          string
		isLatestEntry bool
		isError       bool
	}
	var displayLines []displayLine
	lastIdx := len(m.reflectLines) - 1
	for i, l := range m.reflectLines {
		// Determine if this line indicates an error
		isErr := strings.Contains(l, "exited non-zero") ||
			strings.Contains(l, "killed") ||
			strings.Contains(l, "failed")

		// Prepend status icon to the raw line
		var icon string
		switch {
		case isErr:
			icon = styleReflectFail.Render("✗ ")
		case strings.Contains(l, "complete") || strings.Contains(l, "pushed") || strings.Contains(l, "done"):
			icon = styleReflectOK.Render("✓ ")
		case strings.Contains(l, "starting"):
			icon = styleMuted.Render("◌ ")
		case strings.Contains(l, "No changes") || strings.Contains(l, "No top-level") || strings.Contains(l, "nothing"):
			icon = styleMuted.Render("○ ")
		default:
			icon = styleMuted.Render("  ")
		}

		// Word-wrap the rest
		contentW := panelW - 2
		if contentW < 10 {
			contentW = 10
		}
		wrapped := wrapLine(l, contentW)
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

	// Show the last (h-1) display lines so it fills the panel height.
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
			rendered = styleReflectFail.Render(dl.text)
		case dl.isLatestEntry:
			rendered = styleReflectNew.Render(dl.text)
		default:
			rendered = styleReflectLine.Render(dl.text)
		}
		b.WriteString(rendered + "\n")
	}

	// Pad remaining rows so the panel always fills logHeight.
	written := 1 + len(displayLines)
	for i := written; i < h; i++ {
		b.WriteString("\n")
	}

	return styleReflectPanel.
		Width(panelW + 3).
		Height(h).
		Render(b.String())
}

func (m monitorModel) renderLines() string {
	var b strings.Builder
	for _, line := range m.lines {
		b.WriteString(colorLine(line) + "\n")
	}
	return b.String()
}

func colorLine(line string) string {
	plain := stripANSI(line)
	trimmed := strings.TrimSpace(plain)
	switch {
	case strings.Contains(plain, "❌") || strings.Contains(plain, "ERROR"):
		return styleLogErr.Render(plain)
	case strings.Contains(plain, "⚠️") || strings.Contains(plain, "WARN"):
		return styleLogWarn.Render(plain)
	case strings.Contains(plain, "━━━") || strings.Contains(plain, "Iteration"):
		return styleLogIter.Render(plain)
	case strings.Contains(plain, "✅") || strings.Contains(plain, "APPROVED") ||
		strings.Contains(plain, "Clean review") || strings.Contains(plain, "ready to merge"):
		return styleLogSuccess.Render(plain)
	case strings.Contains(plain, "git") && (strings.Contains(plain, "push") ||
		strings.Contains(plain, "commit") || strings.Contains(plain, "checkout")):
		return styleLogGit.Render(plain)
	case strings.Contains(plain, "gh api") || strings.Contains(plain, "gh pr") ||
		strings.Contains(plain, "Copilot review requested"):
		return styleLogAPI.Render(plain)
	case strings.HasPrefix(trimmed, "→") ||
		strings.HasPrefix(trimmed, "←") ||
		strings.HasPrefix(trimmed, "$") ||
		strings.Contains(plain, "DEBUG"):
		return styleLogDebug.Render(plain)
	case strings.Contains(plain, "🚀") || strings.Contains(plain, "💬"):
		return styleLogInfo.Render(plain)
	default:
		return styleLogAgent.Render(plain)
	}
}

// ── RunMonitor ────────────────────────────────────────────────────────────────

// webhookPayload is the JSON body POSTed to RINSE_WEBHOOK_URL on cycle completion.
type webhookPayload struct {
	Event     string `json:"event"`
	PR        string `json:"pr"`
	Repo      string `json:"repo"`
	ExitCode  int    `json:"exit_code"`
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

// fireWebhook POSTs a cycle-complete notification to webhookURL.
// Errors are printed to stderr but never fatal — the webhook is best-effort.
func fireWebhook(webhookURL, pr, repo string, exitCode int, p phase) {
	status := "done"
	if exitCode != 0 {
		status = "error"
	}
	payload := webhookPayload{
		Event:     "cycle_complete",
		PR:        pr,
		Repo:      repo,
		ExitCode:  exitCode,
		Status:    status,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[rinse] webhook marshal error: %v\n", err)
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "[rinse] webhook POST error: %v\n", err)
		return
	}
	_ = resp.Body.Close()
}

// RunMonitor starts the cycle monitor TUI wrapping the given runner command.
// cwd is the local checkout path used for post-cycle git operations.
// autoMerge signals that the runner will handle merge/cleanup automatically;
// the TUI post-cycle menu is suppressed in that case.
// notify enables a native desktop notification when the cycle completes.
//
// Exit codes:
//
//	0 — cycle completed successfully
//	1 — runner exited with an error
func RunMonitor(pr, repo, runnerName, modelName, prTitle, cwd string, autoMerge, notify bool, runnerArgs []string) (int, error) {
	cmd := exec.Command(runnerArgs[0], runnerArgs[1:]...)
	cmd.Stdin = os.Stdin

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return 1, fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return 1, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return 1, fmt.Errorf("start runner: %w", err)
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
		close(lineCh) // signal that all pipe output has been flushed
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
	final, err := p.Run()
	if err != nil {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return 1, err
	}

	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}

	fm := final.(channelMonitor)
	exitCode := fm.exitCode
	if !fm.done {
		// User killed the TUI before the runner finished — treat as error.
		exitCode = 1
	}

	// Fire webhook if configured (best-effort, non-blocking).
	if webhookURL := os.Getenv("RINSE_WEBHOOK_URL"); webhookURL != "" {
		fireWebhook(webhookURL, pr, repo, exitCode, fm.phase)
	}

	// Send desktop notification if enabled (best-effort, non-blocking).
	CycleNotification(notify, pr, repo, exitCode, time.Since(cm.started))

	return exitCode, nil
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

// poll blocks up to 50ms waiting for a line or done signal.
// lineCh is closed by RunMonitor once both pipe readers have finished, so poll
// continues draining buffered lines until the channel is closed; only then does
// it read the exit code from doneCh. This prevents final stdout/stderr lines
// from being dropped when doneCh and lineCh become ready at the same time.
func (m channelMonitor) poll() tea.Cmd {
	return func() tea.Msg {
		select {
		case line, ok := <-m.lineCh:
			if !ok {
				// lineCh closed — all pipe output has been flushed; wait for exit code.
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
		// q/ctrl+c handled in base; no extra poll needed.
	case logLineMsg:
		// Got a line — immediately queue the next poll to drain fast.
		cmds = append(cmds, m.poll())
	case runnerDoneMsg:
		// Runner finished — no more polling needed.
	default:
		// Covers: nil (poll timeout), tickMsg, spinner.TickMsg, WindowSizeMsg, clearStatusMsg.
		cmds = append(cmds, m.poll())
	}

	return m, tea.Batch(cmds...)
}

func (m channelMonitor) View() string {
	return m.monitorModel.View()
}
