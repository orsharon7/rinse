package main

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

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	stylePhaseDone    = lipgloss.NewStyle().Foreground(teal).Bold(true)
	stylePhaseErr     = lipgloss.NewStyle().Foreground(red).Bold(true)

	// Log line colours.
	styleLogInfo  = lipgloss.NewStyle().Foreground(text)
	styleLogDebug = lipgloss.NewStyle().Foreground(subtext)
	styleLogWarn  = lipgloss.NewStyle().Foreground(yellow)
	styleLogErr   = lipgloss.NewStyle().Foreground(red).Bold(true)
	styleLogIter  = lipgloss.NewStyle().Foreground(mauve).Bold(true)
	styleLogAgent = lipgloss.NewStyle().Foreground(text)

	// Reflect panel styles.
	styleReflectPanel = lipgloss.NewStyle().
				BorderStyle(lipgloss.NormalBorder()).
				BorderLeft(true).
				BorderForeground(teal).
				Padding(0, 1)
	styleReflectTitle = lipgloss.NewStyle().Foreground(teal).Bold(true)
	styleReflectLine  = lipgloss.NewStyle().Foreground(subtext)
	styleReflectNew   = lipgloss.NewStyle().Foreground(text)
)

// ansiRe strips ANSI escape sequences for pattern matching only.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[mK]`)

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

// ── Monitor model ─────────────────────────────────────────────────────────────

type monitorModel struct {
	// config
	pr      string
	repo    string
	runner  string
	model   string
	prTitle string

	// state
	width        int
	height       int
	phase        phase
	iter         int
	started      time.Time
	lines        []string // all main log lines
	reflectLines []string // lines tagged [reflect]
	renderedLog  string   // cached rendered content of lines (appended incrementally)

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

func newMonitorModel(pr, repo, runnerName, modelName, prTitle string, cmd *exec.Cmd) monitorModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(mauve)

	vp := viewport.New(80, 20)
	vp.Style = lipgloss.NewStyle().Foreground(text)

	return monitorModel{
		pr:       pr,
		repo:     repo,
		runner:   runnerName,
		model:    modelName,
		prTitle:  prTitle,
		phase:    phaseStarting,
		started:  time.Now(),
		spinner:  sp,
		viewport: vp,
		atBottom: true,
		cmd:      cmd,
	}
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
// Reserved rows: header(1) + header border(1) + breadcrumb(1) + statusbar border(1) + statusbar(1) = 5.
func (m monitorModel) logHeight() int {
	h := m.height - 5
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

// ── Word wrap ─────────────────────────────────────────────────────────────────

// wrapLine splits s into lines of at most w visible runes, breaking at spaces
// where possible.
func wrapLine(s string, w int) []string {
	if w <= 0 {
		return []string{s}
	}
	runes := []rune(s)
	var lines []string
	for len(runes) > 0 {
		if len(runes) <= w {
			lines = append(lines, string(runes))
			break
		}
		// Try to break at a space within the last 12 chars of the window.
		cut := w
		for cut > w-12 && cut > 0 && runes[cut-1] != ' ' {
			cut--
		}
		if cut <= 0 {
			cut = w // no space found — hard break
		}
		lines = append(lines, strings.TrimRight(string(runes[:cut]), " "))
		runes = runes[cut:]
		// Skip leading spaces on continuation lines.
		for len(runes) > 0 && runes[0] == ' ' {
			runes = runes[1:]
		}
	}
	return lines
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
		m.viewport.SetContent(m.renderedLog)
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
						m.statusMsg = "✓ saved → " + fname
					}
					cmds = append(cmds, tea.Tick(2*time.Second,
						func(t time.Time) tea.Msg { return clearStatusMsg{} }))
				}
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

		// Route [reflect]-tagged lines to the side panel.
		if strings.Contains(plain, "[reflect]") || strings.Contains(plain, "◎ reflect") {
			entry := extractReflectEntry(plain)
			m.reflectLines = append(m.reflectLines, entry)
		} else {
			m.lines = append(m.lines, raw)
			// Append only the new line to the cached rendered buffer (O(1) per line).
			m.renderedLog += colorLine(raw) + "\n"
		}

		m.phase = inferPhase(plain, m.phase)

		// Detect iteration number from separator lines.
		if strings.Contains(plain, "Iteration") {
			var n int
			if _, err := fmt.Sscanf(plain, "%*[^0-9]%d", &n); err == nil && n > m.iter {
				m.iter = n
			}
		}

		m.viewport.Width = m.logWidth()
		m.viewport.Height = m.logHeight()
		m.viewport.SetContent(m.renderedLog)
		if m.atBottom {
			m.viewport.GotoBottom()
		}

	case runnerDoneMsg:
		m.done = true
		m.exitCode = msg.exitCode
		if msg.exitCode == 0 {
			m.phase = phaseDone
		} else {
			m.phase = phaseError
		}
		m.viewport.SetContent(m.renderedLog)
		if m.atBottom {
			m.viewport.GotoBottom()
		}

	case clearStatusMsg:
		m.statusMsg = ""
	}

	return m, tea.Batch(cmds...)
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

	case strings.Contains(plain, "[reflect]") || strings.Contains(plain, "◎ reflect"):
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
	if idx := strings.Index(plain, "◎ reflect"); idx >= 0 {
		msg := strings.TrimSpace(plain[idx+len("◎ reflect"):])
		if strings.HasPrefix(msg, "|") || strings.HasPrefix(msg, "│") {
			msg = strings.TrimSpace(msg[1:])
		}
		return msg
	}
	return plain
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m monitorModel) View() string {
	totalW := m.width
	if totalW < 40 {
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

	showPanel := m.showReflectPanel()
	logW := m.logWidth()
	if !showPanel {
		logW = totalW
	}
	logH := m.logHeight()

	// ── Header (full width) ───────────────────────────────────────────────────
	elapsed := time.Since(m.started).Round(time.Second)
	iterStr := "-"
	if m.iter > 0 {
		iterStr = fmt.Sprintf("%d", m.iter)
	}

	titlePart := ""
	if m.prTitle != "" {
		titlePart = "  " + styleHeaderLabel.Render(`"`) +
			styleHeaderVal.Render(truncate(m.prTitle, 36)) +
			styleHeaderLabel.Render(`"`)
	}

	header := styleHeader.Width(totalW - 2).Render(
		styleHeaderLabel.Render("pr") + " " + styleHeaderVal.Render("#"+m.pr) + titlePart +
			"  " + styleHeaderLabel.Render("repo") + " " + styleHeaderVal.Render(m.repo) +
			"  " + styleHeaderLabel.Render("runner") + " " + styleHeaderVal.Render(m.runner) +
			"  " + styleHeaderLabel.Render("iter") + " " + styleHeaderVal.Render(iterStr) +
			"  " + styleHeaderLabel.Render("elapsed") + " " + styleHeaderVal.Render(elapsed.String()),
	)

	// ── Phase breadcrumb ──────────────────────────────────────────────────────
	breadcrumb := m.renderPhaseBreadcrumb()

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
	} else {
		phaseStr = m.spinner.View() + " " + m.phase.Style().Render(m.phase.String())
	}

	scrollHint := ""
	if !m.atBottom {
		scrollHint = styleMuted.Render("  ↑ scrolled  G=bottom")
	}
	keys := styleMuted.Render("  q=quit  ↑↓/jk=scroll  s=save  ?=help")

	statusBar := styleStatusBar.Width(totalW - 2).Render(phaseStr + scrollHint + keys)

	return header + "\n" + breadcrumb + "\n" + body + "\n" + statusBar
}

// renderReflectPanel builds the right-side reflection panel with word-wrapped entries.
func (m monitorModel) renderReflectPanel(h int) string {
	panelW := m.reflectPanelWidth()
	var b strings.Builder

	title := styleReflectTitle.Render("◎ reflect")
	b.WriteString(title + "\n")

	// Expand all reflect lines with word-wrap, capped at 2 display lines per entry.
	type displayLine struct {
		text          string
		isLatestEntry bool
	}
	var displayLines []displayLine
	lastIdx := len(m.reflectLines) - 1
	for i, l := range m.reflectLines {
		wrapped := wrapLine(l, panelW)
		if len(wrapped) > 2 {
			wrapped = wrapped[:2] // cap at 2 lines per entry to avoid overflow
		}
		for _, wl := range wrapped {
			displayLines = append(displayLines, displayLine{
				text:          wl,
				isLatestEntry: i == lastIdx,
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
		if dl.isLatestEntry {
			rendered = styleReflectNew.Render(dl.text)
		} else {
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
		Width(panelW).
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
	switch {
	case strings.Contains(plain, "❌") || strings.Contains(plain, "ERROR"):
		return styleLogErr.Render(plain)
	case strings.Contains(plain, "⚠️") || strings.Contains(plain, "WARN"):
		return styleLogWarn.Render(plain)
	case strings.Contains(plain, "━━━") || strings.Contains(plain, "Iteration"):
		return styleLogIter.Render(plain)
	case strings.HasPrefix(strings.TrimSpace(plain), "→") ||
		strings.HasPrefix(strings.TrimSpace(plain), "←") ||
		strings.HasPrefix(strings.TrimSpace(plain), "$") ||
		strings.Contains(plain, "DEBUG"):
		return styleLogDebug.Render(plain)
	case strings.Contains(plain, "✅") || strings.Contains(plain, "🚀") || strings.Contains(plain, "💬"):
		return styleLogInfo.Render(plain)
	default:
		return styleLogAgent.Render(plain)
	}
}

// ── RunMonitor ────────────────────────────────────────────────────────────────

// RunMonitor starts the cycle monitor TUI wrapping the given runner command.
func RunMonitor(pr, repo, runnerName, modelName, prTitle string, runnerArgs []string) error {
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

	cm := newChannelMonitor(pr, repo, runnerName, modelName, prTitle, lineCh, doneCh)

	p := tea.NewProgram(cm, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return err
	}

	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}

	return nil
}

// ── channelMonitor ────────────────────────────────────────────────────────────

type channelMonitor struct {
	monitorModel
	lineCh <-chan string
	doneCh <-chan int
}

func newChannelMonitor(pr, repo, runnerName, modelName, prTitle string,
	lineCh <-chan string, doneCh <-chan int) channelMonitor {
	return channelMonitor{
		monitorModel: newMonitorModel(pr, repo, runnerName, modelName, prTitle, nil),
		lineCh:       lineCh,
		doneCh:       doneCh,
	}
}

func (m channelMonitor) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, tick(), m.poll())
}

// poll blocks up to 50ms waiting for a line or done signal.
func (m channelMonitor) poll() tea.Cmd {
	return func() tea.Msg {
		select {
		case line, ok := <-m.lineCh:
			if !ok {
				return nil
			}
			return logLineMsg(line)
		case code, ok := <-m.doneCh:
			if !ok {
				return nil
			}
			return runnerDoneMsg{exitCode: code}
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
