package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Monitor styles ────────────────────────────────────────────────────────────

var (
	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(text).
			Background(surface).
			Padding(0, 2)

	styleHeaderLabel = lipgloss.NewStyle().
				Foreground(overlay).
				Background(surface)

	styleHeaderVal = lipgloss.NewStyle().
			Foreground(lavender).
			Bold(true).
			Background(surface)

	styleStatusBar = lipgloss.NewStyle().
			Foreground(text).
			Background(crust).
			Padding(0, 2)

	stylePhaseWaiting = lipgloss.NewStyle().Foreground(yellow).Bold(true)
	stylePhaseFixing  = lipgloss.NewStyle().Foreground(mauve).Bold(true)
	stylePhaseReflect = lipgloss.NewStyle().Foreground(teal).Bold(true)
	stylePhaseDone    = lipgloss.NewStyle().Foreground(teal).Bold(true)
	stylePhaseErr     = lipgloss.NewStyle().Foreground(red).Bold(true)

	styleLogInfo  = lipgloss.NewStyle().Foreground(lavender)
	styleLogDebug = lipgloss.NewStyle().Foreground(overlay)
	styleLogWarn  = lipgloss.NewStyle().Foreground(yellow)
	styleLogErr   = lipgloss.NewStyle().Foreground(red)
	styleLogIter  = lipgloss.NewStyle().Foreground(mauve).Bold(true)
	styleLogAgent = lipgloss.NewStyle().Foreground(subtext)

	styleDivider = lipgloss.NewStyle().Foreground(surface)
)

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

type monitorModel struct {
	// config
	pr     string
	repo   string
	runner string
	model  string

	// state
	width   int
	height  int
	phase   phase
	iter    int
	started time.Time
	lines   []string // all log lines

	// sub-components
	viewport viewport.Model
	spinner  spinner.Model
	atBottom bool

	// runner process
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
	return tea.Batch(
		m.spinner.Tick,
		tick(),
		startRunner(m.cmd),
	)
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// startRunner launches the process and streams stdout line by line.
func startRunner(cmd *exec.Cmd) tea.Cmd {
	return func() tea.Msg {
		// Already started (stdout pipe set up before Run)
		return nil
	}
}

// readLines reads from a pipe and sends each line as a tea.Msg.
func readLines(r io.Reader) tea.Cmd {
	return func() tea.Msg {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		if scanner.Scan() {
			return logLineMsg(scanner.Text())
		}
		return nil
	}
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m monitorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width
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
		}
		// Forward scroll keys to viewport
		var vpcmd tea.Cmd
		m.viewport, vpcmd = m.viewport.Update(msg)
		// If user scrolled up, disable auto-scroll
		if m.viewport.AtBottom() {
			m.atBottom = true
		} else {
			m.atBottom = false
		}
		cmds = append(cmds, vpcmd)

	case tickMsg:
		cmds = append(cmds, tick())

	case spinner.TickMsg:
		var spcmd tea.Cmd
		m.spinner, spcmd = m.spinner.Update(msg)
		cmds = append(cmds, spcmd)

	case logLineMsg:
		line := string(msg)
		m.lines = append(m.lines, line)
		m.phase = inferPhase(line, m.phase)
		if strings.Contains(line, "━━━  Iteration") || strings.Contains(line, "Iteration ") {
			// extract iteration number
			var n int
			fmt.Sscanf(line, "%*[^0-9]%d", &n)
			if n > m.iter {
				m.iter = n
			}
		}
		m.viewport.Width = m.width
		m.viewport.Height = m.logHeight()
		m.viewport.SetContent(m.renderLines())
		if m.atBottom {
			m.viewport.GotoBottom()
		}
		// Keep reading
		if m.cmd != nil && m.cmd.Process != nil {
			cmds = append(cmds, readLines(m.cmd.Stdout.(io.Reader)))
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

func inferPhase(line string, current phase) phase {
	switch {
	case strings.Contains(line, "⏳") || strings.Contains(line, "Waiting for Copilot"):
		return phaseWaiting
	case strings.Contains(line, "💬") || strings.Contains(line, "invoking opencode") || strings.Contains(line, "invoking claude"):
		return phaseFixing
	case strings.Contains(line, "reflect") || strings.Contains(line, "◎ reflect"):
		return phaseReflecting
	case strings.Contains(line, "✅") && strings.Contains(line, "APPROVED"):
		return phaseDone
	case strings.Contains(line, "❌"):
		return phaseError
	}
	return current
}

func (m monitorModel) logHeight() int {
	// header (3) + divider (1) + status bar (1) + padding (2)
	reserved := 7
	h := m.height - reserved
	if h < 5 {
		h = 5
	}
	return h
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m monitorModel) View() string {
	w := m.width
	if w < 40 {
		w = 80
	}

	var b strings.Builder

	// ── Header ────────────────────────────────────────────────────────────────
	elapsed := time.Since(m.started).Round(time.Second)

	iterStr := "-"
	if m.iter > 0 {
		iterStr = fmt.Sprintf("%d", m.iter)
	}

	header := styleHeader.Width(w).Render(
		styleHeaderLabel.Render("pr") + " " +
			styleHeaderVal.Render("#"+m.pr) +
			"  " +
			styleHeaderLabel.Render("repo") + " " +
			styleHeaderVal.Render(m.repo) +
			"  " +
			styleHeaderLabel.Render("runner") + " " +
			styleHeaderVal.Render(m.runner) +
			"  " +
			styleHeaderLabel.Render("iter") + " " +
			styleHeaderVal.Render(iterStr) +
			"  " +
			styleHeaderLabel.Render("elapsed") + " " +
			styleHeaderVal.Render(elapsed.String()),
	)
	b.WriteString(header + "\n")

	// ── Divider ───────────────────────────────────────────────────────────────
	b.WriteString(styleDivider.Render(strings.Repeat("─", w)) + "\n")

	// ── Log viewport ──────────────────────────────────────────────────────────
	m.viewport.Width = w
	m.viewport.Height = m.logHeight()
	b.WriteString(m.viewport.View() + "\n")

	// ── Divider ───────────────────────────────────────────────────────────────
	b.WriteString(styleDivider.Render(strings.Repeat("─", w)) + "\n")

	// ── Status bar ────────────────────────────────────────────────────────────
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

	statusBar := styleStatusBar.Width(w).Render(
		phaseStr + scrollHint + keys,
	)
	b.WriteString(statusBar)

	return b.String()
}

func (m monitorModel) renderLines() string {
	var b strings.Builder
	for _, line := range m.lines {
		b.WriteString(colorLine(line) + "\n")
	}
	return b.String()
}

func colorLine(line string) string {
	switch {
	case strings.Contains(line, "❌") || strings.Contains(line, "ERROR"):
		return styleLogErr.Render(line)
	case strings.Contains(line, "⚠️") || strings.Contains(line, "WARN"):
		return styleLogWarn.Render(line)
	case strings.Contains(line, "━━━") || strings.Contains(line, "Iteration"):
		return styleLogIter.Render(line)
	case strings.Contains(line, "DEBUG") || strings.HasPrefix(strings.TrimSpace(line), "→") ||
		strings.HasPrefix(strings.TrimSpace(line), "←") || strings.HasPrefix(strings.TrimSpace(line), "$"):
		return styleLogDebug.Render(line)
	case strings.Contains(line, "INFO") || strings.Contains(line, "✅") ||
		strings.Contains(line, "🚀") || strings.Contains(line, "💬"):
		return styleLogInfo.Render(line)
	default:
		return styleLogAgent.Render(line)
	}
}

// ── monitorCmd ────────────────────────────────────────────────────────────────
// monitorCmd wraps exec.Cmd and holds the stdout pipe.

type monitorCmd struct {
	*exec.Cmd
	stdout io.Reader
}

// RunMonitor starts the cycle monitor TUI wrapping the given runner command.
func RunMonitor(pr, repo, runnerName, modelName string, runnerArgs []string) error {
	cmd := exec.Command(runnerArgs[0], runnerArgs[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr // runner's stderr goes directly to terminal (behind alt screen)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start runner: %w", err)
	}

	mc := &monitorCmd{Cmd: cmd, stdout: stdoutPipe}
	_ = mc

	m := newMonitorModel(pr, repo, runnerName, modelName, nil)
	_ = m

	// Use a channel to feed lines into the Bubble Tea program
	lineCh := make(chan string, 256)
	doneCh := make(chan int, 1)

	// Goroutine: read runner stdout → lineCh
	go func() {
		scanner := bufio.NewScanner(mc.stdout)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			lineCh <- scanner.Text()
		}
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

	// Custom init that polls lineCh
	pollLines := func() tea.Msg {
		select {
		case line := <-lineCh:
			return logLineMsg(line)
		case code := <-doneCh:
			return runnerDoneMsg{exitCode: code}
		case <-time.After(50 * time.Millisecond):
			return nil
		}
	}

	// Build program with a model that uses the channel-based polling
	cm := newChannelMonitor(pr, repo, runnerName, modelName, lineCh, doneCh)

	p := tea.NewProgram(cm,
		tea.WithAltScreen(),
	)

	if _, err := p.Run(); err != nil {
		return err
	}

	// Kill process if still running (user quit)
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}

	_ = pollLines // suppress unused warning
	return nil
}

// ── channelMonitor ────────────────────────────────────────────────────────────
// A version of monitorModel that polls from channels instead of using
// the exec pipe directly (Bubble Tea's event loop is single-threaded).

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
	return tea.Batch(
		m.spinner.Tick,
		tick(),
		m.poll(),
	)
}

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
		case <-time.After(80 * time.Millisecond):
			return nil // return nil to let other messages through
		}
	}
}

func (m channelMonitor) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width
		m.viewport.Height = m.logHeight()
		m.viewport.SetContent(m.renderLines())
		if m.atBottom {
			m.viewport.GotoBottom()
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
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
			if m.viewport.AtBottom() {
				m.atBottom = true
			} else {
				m.atBottom = false
			}
			cmds = append(cmds, vpcmd)
		}

	case tickMsg:
		cmds = append(cmds, tick())

	case spinner.TickMsg:
		var spcmd tea.Cmd
		m.spinner, spcmd = m.spinner.Update(msg)
		cmds = append(cmds, spcmd)

	case logLineMsg:
		line := string(msg)
		m.lines = append(m.lines, line)
		m.phase = inferPhase(line, m.phase)

		// Detect iteration number from the separator line
		if strings.Contains(line, "Iteration") {
			var n int
			if _, err := fmt.Sscanf(line, "%*[^0-9]%d", &n); err == nil && n > m.iter {
				m.iter = n
			}
		}

		m.viewport.Width = m.width
		m.viewport.Height = m.logHeight()
		m.viewport.SetContent(m.renderLines())
		if m.atBottom {
			m.viewport.GotoBottom()
		}
		cmds = append(cmds, m.poll())

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
		// Don't quit automatically — let user read the output and press q
	}

	// nil msgs from poll timeout — re-poll
	if msg == nil {
		cmds = append(cmds, m.poll())
	}

	return m, tea.Batch(cmds...)
}

func (m channelMonitor) View() string {
	return m.monitorModel.View()
}
