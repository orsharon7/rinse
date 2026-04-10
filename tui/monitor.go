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

	// Log line colours — bright on dark background.
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

// ── Monitor model ─────────────────────────────────────────────────────────────

const reflectPanelWidth = 32 // visible chars (excluding border)

type monitorModel struct {
	// config
	pr     string
	repo   string
	runner string
	model  string

	// state
	width        int
	height       int
	phase        phase
	iter         int
	started      time.Time
	lines        []string // all main log lines
	reflectLines []string // lines tagged [reflect]

	// sub-components
	viewport viewport.Model
	spinner  spinner.Model
	atBottom bool

	// runner process (only used in base model for q-quit)
	cmd      *exec.Cmd
	exitCode int
	done     bool
}

func newMonitorModel(pr, repo, runnerName, modelName string, cmd *exec.Cmd) monitorModel {
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

// logWidth returns the width available to the main log viewport.
// Reflect panel: reflectPanelWidth + 2 (border char + padding) = 34 cols.
func (m monitorModel) logWidth() int {
	if m.width <= 0 {
		return 80
	}
	w := m.width - (reflectPanelWidth + 3) // 3 = border(1) + padding(2)
	if w < 40 {
		w = m.width // terminal too narrow — drop panel
	}
	return w
}

// logHeight returns viewport height.
// Reserved: header content(1) + header border(1) + statusbar border(1) + statusbar content(1) = 4.
func (m monitorModel) logHeight() int {
	h := m.height - 4
	if h < 5 {
		h = 5
	}
	return h
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
		m.viewport.SetContent(m.renderLines())
		if m.atBottom {
			m.viewport.GotoBottom()
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if m.cmd != nil && m.cmd.Process != nil {
				_ = m.cmd.Process.Kill()
			}
			return m, tea.Quit
		case "G":
			m.atBottom = true
			m.viewport.GotoBottom()
		case "g":
			m.atBottom = false
			m.viewport.GotoTop()
		default:
			var vpcmd tea.Cmd
			m.viewport, vpcmd = m.viewport.Update(msg)
			m.atBottom = m.viewport.AtBottom()
			cmds = append(cmds, vpcmd)
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

		// Route [reflect]-tagged lines to the side panel; keep rest in main log.
		if strings.Contains(plain, "[reflect]") {
			entry := extractReflectEntry(plain)
			m.reflectLines = append(m.reflectLines, entry)
		} else {
			m.lines = append(m.lines, raw)
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
		m.viewport.SetContent(m.renderLines())
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
		m.viewport.SetContent(m.renderLines())
		if m.atBottom {
			m.viewport.GotoBottom()
		}
	}

	return m, tea.Batch(cmds...)
}

// inferPhase maps plain-text log line content to a phase.
// Works on ANSI-stripped text so ANSI colour codes don't prevent matching.
func inferPhase(plain string, current phase) phase {
	switch {
	// Approved — terminal state, never step back from it.
	case current == phaseDone:
		return phaseDone

	case strings.Contains(plain, "APPROVED"):
		return phaseDone

	case strings.Contains(plain, "❌") || strings.Contains(plain, "Timed out"):
		return phaseError

	// Reflection runs alongside fixing — treat as its own phase.
	case strings.Contains(plain, "[reflect]"):
		return phaseReflecting

	// Active fix phase: agent is being invoked.
	case strings.Contains(plain, "invoking opencode") ||
		strings.Contains(plain, "invoking claude") ||
		strings.Contains(plain, "💬"):
		return phaseFixing

	// Waiting for Copilot to re-review.
	case strings.Contains(plain, "Waiting for Copilot") ||
		strings.Contains(plain, "Copilot reviewing") ||
		strings.Contains(plain, "⏳"):
		return phaseWaiting

	// Loop just started — move out of "starting" to "waiting" so the label
	// updates as soon as the runner emits its first meaningful line.
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
	// Format: "[2026-04-10 11:33:41] [reflect] some message"
	// or:     "◎ reflect | some message"
	if idx := strings.Index(plain, "[reflect]"); idx >= 0 {
		msg := strings.TrimSpace(plain[idx+len("[reflect]"):])
		return msg
	}
	if idx := strings.Index(plain, "◎ reflect"); idx >= 0 {
		msg := strings.TrimSpace(plain[idx+len("◎ reflect"):])
		msg = strings.TrimPrefix(msg, "|")
		return strings.TrimSpace(msg)
	}
	return plain
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m monitorModel) View() string {
	totalW := m.width
	if totalW < 40 {
		totalW = 80
	}

	logW := m.logWidth()
	logH := m.logHeight()

	// ── Header (full width) ───────────────────────────────────────────────────
	elapsed := time.Since(m.started).Round(time.Second)
	iterStr := "-"
	if m.iter > 0 {
		iterStr = fmt.Sprintf("%d", m.iter)
	}

	header := styleHeader.Width(totalW - 2).Render( // -2 for padding
		styleHeaderLabel.Render("pr") + " " + styleHeaderVal.Render("#"+m.pr) +
			"  " + styleHeaderLabel.Render("repo") + " " + styleHeaderVal.Render(m.repo) +
			"  " + styleHeaderLabel.Render("runner") + " " + styleHeaderVal.Render(m.runner) +
			"  " + styleHeaderLabel.Render("iter") + " " + styleHeaderVal.Render(iterStr) +
			"  " + styleHeaderLabel.Render("elapsed") + " " + styleHeaderVal.Render(elapsed.String()),
	)

	// ── Main log viewport ─────────────────────────────────────────────────────
	m.viewport.Width = logW
	m.viewport.Height = logH
	logView := m.viewport.View()

	// ── Reflect panel ─────────────────────────────────────────────────────────
	reflectView := m.renderReflectPanel(logH)

	// Join log + panel side by side, line by line.
	body := lipgloss.JoinHorizontal(lipgloss.Top, logView, reflectView)

	// ── Status bar (full width) ───────────────────────────────────────────────
	var phaseStr string
	if m.done {
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
	keys := styleMuted.Render("  q=quit  ↑↓/jk=scroll  G=bottom  g=top")

	statusBar := styleStatusBar.Width(totalW - 2).Render(phaseStr + scrollHint + keys)

	return header + "\n" + body + "\n" + statusBar
}

// renderReflectPanel builds the right-side reflection panel.
func (m monitorModel) renderReflectPanel(h int) string {
	panelW := reflectPanelWidth // inner content width
	var b strings.Builder

	title := styleReflectTitle.Render("◎ reflect")
	b.WriteString(title + "\n")

	// Show the last (h-1) lines so it fills the panel height.
	lines := m.reflectLines
	maxLines := h - 1 // reserve one row for title
	if maxLines < 1 {
		maxLines = 1
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	for i, l := range lines {
		// Truncate to panel width.
		if len([]rune(l)) > panelW {
			runes := []rune(l)
			l = string(runes[:panelW-1]) + "…"
		}
		var rendered string
		if i == len(lines)-1 {
			rendered = styleReflectNew.Render(l) // latest entry highlighted
		} else {
			rendered = styleReflectLine.Render(l)
		}
		b.WriteString(rendered + "\n")
	}

	// Pad remaining rows so the panel always fills logHeight.
	written := 1 + len(lines)
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
func RunMonitor(pr, repo, runnerName, modelName string, runnerArgs []string) error {
	cmd := exec.Command(runnerArgs[0], runnerArgs[1:]...)
	cmd.Stdin = os.Stdin
	// Capture BOTH stdout and stderr into the viewport so nothing is
	// silently swallowed behind the alt screen.

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

	// readPipe drains one pipe, forwarding every line to lineCh.
	readPipe := func(r io.Reader, wg *sync.WaitGroup) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			lineCh <- scanner.Text()
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go readPipe(stdoutPipe, &wg)
	go readPipe(stderrPipe, &wg)

	// Wait for both pipes to finish, then send exit code.
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

	cm := newChannelMonitor(pr, repo, runnerName, modelName, lineCh, doneCh)

	p := tea.NewProgram(cm, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return err
	}

	// Kill process if still running (user quit early).
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}

	return nil
}

// ── channelMonitor ────────────────────────────────────────────────────────────
// channelMonitor wraps monitorModel and polls lineCh/doneCh via Bubble Tea cmds.

type channelMonitor struct {
	monitorModel
	lineCh <-chan string
	doneCh <-chan int
}

func newChannelMonitor(pr, repo, runnerName, modelName string, lineCh <-chan string, doneCh <-chan int) channelMonitor {
	return channelMonitor{
		monitorModel: newMonitorModel(pr, repo, runnerName, modelName, nil),
		lineCh:       lineCh,
		doneCh:       doneCh,
	}
}

func (m channelMonitor) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, tick(), m.poll())
}

// poll blocks up to 50ms waiting for a line or done signal, then returns.
// Returning nil allows other tea.Msgs (keys, ticks) to be processed.
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

	// Delegate common handling to the base model first.
	updated, cmd := m.monitorModel.Update(msg)
	m.monitorModel = updated.(monitorModel)
	cmds = append(cmds, cmd)

	switch msg.(type) {
	case tea.KeyMsg:
		// q/ctrl+c already handled in base; no extra poll needed.
	case logLineMsg:
		// Got a line — immediately queue the next poll to drain fast.
		cmds = append(cmds, m.poll())
	case runnerDoneMsg:
		// Runner finished — no more polling needed.
	default:
		// Covers: nil (poll timeout), tickMsg, spinner.TickMsg, WindowSizeMsg.
		// Always re-poll so we never stop draining the channel.
		cmds = append(cmds, m.poll())
	}

	return m, tea.Batch(cmds...)
}

func (m channelMonitor) View() string {
	return m.monitorModel.View()
}
