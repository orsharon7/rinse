package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Splash timer message ──────────────────────────────────────────────────────

type splashDoneMsg struct{}

// ── Model ─────────────────────────────────────────────────────────────────────

type model struct {
	width  int
	height int

	view viewMode

	// splash
	splashSpinner spinner.Model
	splashReady   bool

	// auto-detected on boot
	repo          string
	path          string
	currentBranch string
	defaultBranch string

	// settings (persisted per-repo)
	runnerIdx     int
	modelOverride string
	reflect       bool
	reflectBranch string
	autoMerge     bool

	// PR picker
	prs       []pr
	prCursor  int
	prLoading bool
	prLoadErr string
	prSpinner spinner.Model // animated spinner for loading state

	// manual PR input
	input textinput.Model

	// settings overlay state
	settingsFocus         settingsField
	settingsRunnerIdx     int
	settingsModelInput    textinput.Model
	settingsReflect       bool
	settingsAutoMerge     bool
	settingsBranchInput   textinput.Model
	settingsEditingModel  bool
	settingsEditingBranch bool
	settingsBranchEdited  bool

	// selected PR
	prNum   string
	prTitle string

	// error
	errMsg string

	// final command
	finalCmd []string

	// help overlay
	help     help.Model
	showHelp bool

	// footer status message (shown in footer bar; empty = idle)
	statusMsg     string
	statusIsError bool
	itemCount     int // total PRs loaded
}

func initialModel() model {
	repo := detectRepo()

	cfg := LoadConfig()
	var rc RepoConfig
	hasRepoConfig := false
	if repo != "" {
		if loaded, ok := LoadRepoConfig(repo); ok {
			rc = loaded
			hasRepoConfig = true
		}
	}
	if !hasRepoConfig && cfg.LastRunner > 0 && cfg.LastRunner < len(runners) {
		rc.Runner = cfg.LastRunner
	}
	if rc.Model == "" {
		rc.Model = cfg.LastModel
	}

	// Apply per-repo .rinse.json defaults (created by `rinse init`) only when
	// there is no saved per-repo config yet, so persisted user choices are not
	// overwritten on subsequent runs. Resolve the git repo root so .rinse.json
	// is found even when the user runs `rinse` from a subdirectory of the repo.
	if !hasRepoConfig {
		rinseConfigDir := detectGitRoot()
		if rinseConfigDir == "" {
			rinseConfigDir = detectCWD()
		}
		if repoCfg, ok := LoadRepoRinseConfig(rinseConfigDir); ok {
			for i, r := range runners {
				if strings.EqualFold(r.name, repoCfg.Engine) {
					rc.Runner = i
					break
				}
			}
			if repoCfg.Model != "" {
				rc.Model = repoCfg.Model
			}
			rc.Reflect = repoCfg.Reflect
			if repoCfg.ReflectBranch != "" {
				rc.Branch = repoCfg.ReflectBranch
			}
			rc.AutoMerge = repoCfg.AutoMerge
			hasRepoConfig = true
		}
	}

	path := detectCWD()
	if repo == "" {
		path = rc.Path
		if path == "" {
			path = detectCWD()
		}
	}

	ti := textinput.New()
	ti.Cursor.Style = lipgloss.NewStyle().Foreground(mauve)
	ti.PromptStyle = lipgloss.NewStyle().Foreground(mauve)
	ti.TextStyle = lipgloss.NewStyle().Foreground(text)
	ti.Prompt = "  PR# " + IconArrow + " "
	ti.CharLimit = 10
	ti.Placeholder = "e.g. 42"

	mi := textinput.New()
	mi.Cursor.Style = lipgloss.NewStyle().Foreground(mauve)
	mi.PromptStyle = lipgloss.NewStyle().Foreground(mauve)
	mi.TextStyle = lipgloss.NewStyle().Foreground(text)
	mi.Prompt = "  " + IconArrow + " "
	mi.CharLimit = 80

	bi := textinput.New()
	bi.Cursor.Style = lipgloss.NewStyle().Foreground(mauve)
	bi.PromptStyle = lipgloss.NewStyle().Foreground(mauve)
	bi.TextStyle = lipgloss.NewStyle().Foreground(text)
	bi.Prompt = "  " + IconArrow + " "
	bi.CharLimit = 80

	reflectDefault := rc.Reflect
	autoMergeDefault := rc.AutoMerge
	if !hasRepoConfig {
		reflectDefault = cfg.LastReflect
		autoMergeDefault = cfg.LastAutoMerge
	}

	runnerIdx := rc.Runner
	if runnerIdx < 0 || runnerIdx >= len(runners) {
		runnerIdx = 0
	}

	reflectBranch := rc.Branch

	// Splash spinner — MiniDot like Crush
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(mauve)

	// PR list loading spinner
	ps := spinner.New()
	ps.Spinner = spinner.Dot
	ps.Style = lipgloss.NewStyle().Foreground(lavender)

	return model{
		view:          viewSplash,
		splashSpinner: sp,
		prSpinner:     ps,
		repo:          repo,
		path:          path,
		defaultBranch: "main",

		runnerIdx:     runnerIdx,
		modelOverride: rc.Model,
		reflect:       reflectDefault,
		reflectBranch: reflectBranch,
		autoMerge:     autoMergeDefault,

		prLoading: repo != "",

		input:               ti,
		settingsModelInput:  mi,
		settingsBranchInput: bi,
		help:                newHelpModel(),
	}
}

// ── Init ──────────────────────────────────────────────────────────────────────

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.splashSpinner.Tick,
		m.prSpinner.Tick,
		tea.Tick(1200*time.Millisecond, func(t time.Time) tea.Msg { return splashDoneMsg{} }),
	}
	if m.repo != "" {
		cmds = append(cmds,
			fetchPRs(m.repo),
			fetchDefaultBranch(m.repo),
			fetchCurrentBranch(),
		)
	}
	return tea.Batch(cmds...)
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case spinner.TickMsg:
		var cmds []tea.Cmd
		if m.view == viewSplash {
			var cmd tea.Cmd
			m.splashSpinner, cmd = m.splashSpinner.Update(msg)
			cmds = append(cmds, cmd)
		}
		if m.prLoading {
			var cmd tea.Cmd
			m.prSpinner, cmd = m.prSpinner.Update(msg)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case splashDoneMsg:
		m.splashReady = true
		if !m.prLoading {
			m.view = viewPRPicker
		}
		return m, nil

	case prListMsg:
		m.prs = []pr(msg)
		m.prLoading = false
		m.itemCount = len(m.prs)
		m.prCursor = 0
		if m.currentBranch != "" {
			for i, p := range m.prs {
				if p.HeadRefName == m.currentBranch {
					m.prCursor = i
					break
				}
			}
		}
		if m.view == viewSplash && m.splashReady {
			m.view = viewPRPicker
		}
		return m, nil

	case prListErrMsg:
		m.prLoading = false
		m.prLoadErr = msg.err.Error()
		m.itemCount = 0
		if m.view == viewSplash && m.splashReady {
			m.view = viewPRPicker
		}
		return m, nil

	case defaultBranchMsg:
		m.defaultBranch = string(msg)
		if m.reflectBranch == "" {
			m.reflectBranch = m.defaultBranch
		}
		if m.view == viewSettings {
			m.settingsBranchInput.Placeholder = m.defaultBranch
			if !m.settingsBranchEdited && m.settingsBranchInput.Value() == "" {
				m.settingsBranchInput.SetValue(m.defaultBranch)
			}
		}
		return m, nil

	case currentBranchMsg:
		m.currentBranch = string(msg)
		if len(m.prs) > 0 && m.currentBranch != "" {
			for i, p := range m.prs {
				if p.HeadRefName == m.currentBranch {
					m.prCursor = i
					break
				}
			}
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	// Forward to text inputs when active
	if m.view == viewManualPR {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	if m.view == viewSettings {
		if m.settingsEditingModel {
			var cmd tea.Cmd
			m.settingsModelInput, cmd = m.settingsModelInput.Update(msg)
			return m, cmd
		}
		if m.settingsEditingBranch {
			var cmd tea.Cmd
			m.settingsBranchInput, cmd = m.settingsBranchInput.Update(msg)
			return m, cmd
		}
	}

	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// ctrl+c always quits
	if key.Matches(msg, Keys.ForceQuit) {
		return m, tea.Quit
	}

	// '?' toggles the help overlay in any non-splash, non-text-input view.
	if key.Matches(msg, Keys.Help) &&
		m.view != viewSplash && m.view != viewManualPR && !m.settingsEditingModel && !m.settingsEditingBranch {
		m.showHelp = !m.showHelp
		m.help.ShowAll = m.showHelp
		return m, nil
	}

	// Close the overlay with esc/q without quitting when it's open.
	if m.showHelp {
		if key.Matches(msg, Keys.CloseHelp) {
			m.showHelp = false
			m.help.ShowAll = false
		}
		return m, nil
	}

	switch m.view {

	case viewSplash:
		if key.Matches(msg, Keys.Quit) {
			return m, tea.Quit
		}
		m.view = viewPRPicker
		return m, nil

	case viewPRPicker:
		if m.prLoading {
			if key.Matches(msg, Keys.Quit) {
				return m, tea.Quit
			}
			return m, nil
		}
		switch {
		case key.Matches(msg, Keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, Keys.ManualPR):
			m.view = viewManualPR
			m.input.SetValue("")
			m.input.Focus()
			m.errMsg = ""
			return m, textinput.Blink
		case key.Matches(msg, Keys.Settings):
			return m.openSettings()
		case key.Matches(msg, Keys.Refresh):
			if m.repo != "" {
				m.prLoading = true
				m.prs = nil
				m.itemCount = 0
				m.prLoadErr = ""
				return m, tea.Batch(fetchPRs(m.repo), m.prSpinner.Tick)
			}
		case key.Matches(msg, Keys.Up):
			if len(m.prs) > 0 && m.prCursor > 0 {
				m.prCursor--
			}
		case key.Matches(msg, Keys.Down):
			if len(m.prs) > 0 && m.prCursor < len(m.prs)-1 {
				m.prCursor++
			}
		case key.Matches(msg, Keys.Top):
			m.prCursor = 0
		case key.Matches(msg, Keys.Bottom):
			if len(m.prs) > 0 {
				m.prCursor = len(m.prs) - 1
			}
		case key.Matches(msg, Keys.Confirm):
			if len(m.prs) > 0 && m.prCursor < len(m.prs) {
				m.prNum = fmt.Sprintf("%d", m.prs[m.prCursor].Number)
				m.prTitle = m.prs[m.prCursor].Title
				return m.launch()
			}
		}

	case viewManualPR:
		switch {
		case key.Matches(msg, Keys.Back):
			m.view = viewPRPicker
			m.errMsg = ""
			return m, nil
		case key.Matches(msg, Keys.Confirm):
			val := strings.TrimSpace(m.input.Value())
			if val == "" {
				m.errMsg = "PR number is required"
				return m, nil
			}
			m.prNum = val
			m.prTitle = ""
			m.errMsg = ""
			return m.launch()
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case viewSettings:
		return m.handleSettingsKey(msg)

	case viewHelp:
		// Legacy full-screen help — redirect to overlay behaviour.
		m.view = viewPRPicker
		return m, nil
	}

	return m, nil
}

// ── Settings ──────────────────────────────────────────────────────────────────

func (m model) openSettings() (model, tea.Cmd) {
	m.view = viewSettings
	m.settingsFocus = sfRunner
	m.settingsRunnerIdx = m.runnerIdx
	m.settingsReflect = m.reflect
	m.settingsAutoMerge = m.autoMerge
	m.settingsModelInput.SetValue(m.modelOverride)
	m.settingsModelInput.Placeholder = runners[m.runnerIdx].defaultModel
	branch := m.reflectBranch
	if branch == "" {
		branch = m.defaultBranch
	}
	m.settingsBranchInput.SetValue(branch)
	m.settingsBranchInput.Placeholder = m.defaultBranch
	m.settingsEditingModel = false
	m.settingsEditingBranch = false
	m.settingsBranchEdited = false
	return m, nil
}

func (m model) handleSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.settingsEditingModel {
		switch {
		case key.Matches(msg, Keys.Confirm), key.Matches(msg, Keys.Back):
			m.settingsEditingModel = false
			m.settingsModelInput.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.settingsModelInput, cmd = m.settingsModelInput.Update(msg)
		return m, cmd
	}
	if m.settingsEditingBranch {
		switch {
		case key.Matches(msg, Keys.Confirm), key.Matches(msg, Keys.Back):
			m.settingsEditingBranch = false
			m.settingsBranchInput.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.settingsBranchInput, cmd = m.settingsBranchInput.Update(msg)
		return m, cmd
	}

	maxField := sfCancel
	switch {
	case key.Matches(msg, Keys.Back):
		m.view = viewPRPicker
		return m, nil
	case key.Matches(msg, Keys.Up):
		if m.settingsFocus > 0 {
			m.settingsFocus--
			if m.settingsFocus == sfReflectBranch && !m.settingsReflect {
				m.settingsFocus--
			}
		}
	case key.Matches(msg, Keys.Down), key.Matches(msg, Keys.Tab):
		if m.settingsFocus < maxField {
			m.settingsFocus++
			if m.settingsFocus == sfReflectBranch && !m.settingsReflect {
				m.settingsFocus++
			}
		}
	case key.Matches(msg, Keys.Left):
		if m.settingsFocus == sfRunner && m.settingsRunnerIdx > 0 {
			m.settingsRunnerIdx--
			m.settingsModelInput.Placeholder = runners[m.settingsRunnerIdx].defaultModel
		}
	case key.Matches(msg, Keys.Right):
		if m.settingsFocus == sfRunner && m.settingsRunnerIdx < len(runners)-1 {
			m.settingsRunnerIdx++
			m.settingsModelInput.Placeholder = runners[m.settingsRunnerIdx].defaultModel
		}
	case key.Matches(msg, Keys.Toggle):
		switch m.settingsFocus {
		case sfReflect:
			m.settingsReflect = !m.settingsReflect
		case sfAutoMerge:
			m.settingsAutoMerge = !m.settingsAutoMerge
		}
	case key.Matches(msg, Keys.Confirm):
		switch m.settingsFocus {
		case sfRunner:
			m.settingsRunnerIdx = (m.settingsRunnerIdx + 1) % len(runners)
			m.settingsModelInput.Placeholder = runners[m.settingsRunnerIdx].defaultModel
		case sfModel:
			m.settingsEditingModel = true
			m.settingsModelInput.Focus()
			return m, textinput.Blink
		case sfReflect:
			m.settingsReflect = !m.settingsReflect
		case sfReflectBranch:
			m.settingsEditingBranch = true
			m.settingsBranchEdited = true
			m.settingsBranchInput.Focus()
			return m, textinput.Blink
		case sfAutoMerge:
			m.settingsAutoMerge = !m.settingsAutoMerge
		case sfSave:
			m.runnerIdx = m.settingsRunnerIdx
			m.modelOverride = strings.TrimSpace(m.settingsModelInput.Value())
			m.reflect = m.settingsReflect
			m.autoMerge = m.settingsAutoMerge
			branch := strings.TrimSpace(m.settingsBranchInput.Value())
			if branch == "" {
				branch = m.defaultBranch
			}
			m.reflectBranch = branch
			m.view = viewPRPicker
			SaveConfig(Config{
				LastRepo:      m.repo,
				LastPath:      m.path,
				LastRunner:    m.runnerIdx,
				LastModel:     m.modelOverride,
				LastReflect:   m.reflect,
				LastBranch:    m.reflectBranch,
				LastAutoMerge: m.autoMerge,
			})
			return m, nil
		case sfCancel:
			m.view = viewPRPicker
			return m, nil
		}
	}

	return m, nil
}

// ── Launch ────────────────────────────────────────────────────────────────────

func (m model) launch() (model, tea.Cmd) {
	cmd, err := m.buildCmd()
	if err != nil {
		m.errMsg = err.Error()
		return m, nil
	}
	m.finalCmd = cmd
	m.view = viewDone
	return m, tea.Quit
}

func (m model) buildCmd() ([]string, error) {
	if m.repo == "" {
		return nil, fmt.Errorf("no repository detected — run from inside a git repo")
	}

	r := runners[m.runnerIdx]

	scriptDir := os.Getenv("RINSE_SCRIPT_DIR")
	if scriptDir == "" {
		scriptDir = os.Getenv("PR_REVIEW_SCRIPT_DIR")
	}
	if scriptDir == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("could not determine script directory: %w", err)
		}
		binDir := filepath.Dir(exe)
		candidates := []string{
			filepath.Join(binDir, "pr-review"),
			filepath.Join(binDir, "..", "pr-review"),
			binDir,
		}
		for _, c := range candidates {
			if _, err := os.Stat(filepath.Join(c, r.script)); err == nil {
				scriptDir = c
				break
			}
		}
		if scriptDir == "" {
			scriptDir = binDir
		}
	}
	script := filepath.Join(scriptDir, r.script)
	if _, err := os.Stat(script); err != nil {
		return nil, fmt.Errorf("runner script not found: %s", script)
	}

	path := m.path
	if path == "" {
		path = detectCWD()
	}

	cmd := []string{script, m.prNum,
		"--repo", m.repo,
		"--cwd", path,
	}
	if m.modelOverride != "" {
		cmd = append(cmd, "--model", m.modelOverride)
	}
	if m.reflect {
		branch := m.reflectBranch
		if branch == "" {
			branch = m.defaultBranch
		}
		cmd = append(cmd, "--reflect", "--reflect-main-branch", branch)
	}
	if m.autoMerge {
		cmd = append(cmd, "--auto-merge")
	}

	SaveConfig(Config{
		LastRepo:      m.repo,
		LastPath:      path,
		LastRunner:    m.runnerIdx,
		LastModel:     m.modelOverride,
		LastReflect:   m.reflect,
		LastBranch:    m.reflectBranch,
		LastAutoMerge: m.autoMerge,
	})

	return cmd, nil
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m model) View() string {
	w := m.width
	if w <= 0 {
		w = 80
	}
	h := m.height
	if h <= 0 {
		h = 24
	}

	// Splash screen occupies the full terminal — no header/footer.
	if m.view == viewSplash {
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, m.renderSplash())
	}

	header := m.renderHeader(w)
	footer := m.renderFooter(w)
	headerH := lipgloss.Height(header)
	footerH := lipgloss.Height(footer)
	contentH := h - headerH - footerH
	if contentH < 1 {
		contentH = 1
	}

	var content string
	if m.showHelp {
		return m.renderHelpOverlay(w, h)
	}
	switch m.view {
	case viewHelp:
		content = lipgloss.Place(w, contentH, lipgloss.Center, lipgloss.Center, m.renderHelp())
	case viewSettings:
		content = lipgloss.Place(w, contentH, lipgloss.Center, lipgloss.Center, m.renderSettings())
	case viewManualPR:
		content = m.renderManualPR(w, contentH)
	default:
		content = m.renderPRPicker(w)
		// Clamp and pad content to exactly contentH lines.
		lines := strings.Split(content, "\n")
		if len(lines) > contentH {
			lines = lines[:contentH]
			content = strings.Join(lines, "\n")
		}
		got := lipgloss.Height(content)
		if got < contentH {
			content += strings.Repeat("\n", contentH-got)
		}
	}

	if strings.HasSuffix(content, "\n") {
		return header + "\n" + content + footer
	}
	return header + "\n" + content + "\n" + footer
}

// renderHeader renders the persistent header bar.
//
//	rinse™ RINSE ╱╱╱╱╱╱ owner/repo • main ╱╱╱╱
func (m model) renderHeader(w int) string {
	innerW := w - styleAppHeader.GetHorizontalFrameSize()
	if innerW < 0 {
		innerW = 0
	}
	brand := renderCompactBrandWithDetails(innerW, m.headerDetails())
	return styleAppHeader.Width(w).Render(brand)
}

// headerDetails returns the contextual info shown right of the logo in the header.
func (m model) headerDetails() string {
	if m.repo != "" {
		branch := m.currentBranch
		if branch == "" {
			branch = m.defaultBranch
		}
		return m.repo + " • " + branch
	}
	return ""
}

// footerHints returns key hint text appropriate for the current view.
func (m model) footerHints() string {
	switch m.view {
	case viewSettings:
		return "esc:back"
	case viewHelp:
		return "any key:close"
	case viewManualPR:
		return "esc:back"
	default:
		return "?:help  q:quit  r:refresh"
	}
}

// truncateFooterText truncates s to at most maxWidth display cells, appending
// an ellipsis when truncation occurs.
func truncateFooterText(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	if maxWidth == 1 {
		return "…"
	}
	var b strings.Builder
	width := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if width+rw > maxWidth-1 {
			break
		}
		b.WriteRune(r)
		width += rw
	}
	b.WriteString("…")
	return b.String()
}

// renderFooter renders the persistent footer bar.
func (m model) renderFooter(w int) string {
	if w <= 0 {
		w = 80
	}

	// Derive the available content width from the footer style's horizontal
	// frame so layout stays in sync with any padding/border theme changes.
	frameW := styleAppFooter.GetHorizontalFrameSize()
	contentW := w - frameW
	if contentW <= 1 {
		return styleAppFooter.Width(w).Render("")
	}

	// Left: status message or idle indicator.
	statusText := "ready"
	statusStyle := styleFooterMuted
	if m.statusMsg != "" {
		icon := IconCheck
		statusStyle = styleFooterStatus
		if m.statusIsError {
			icon = IconCross
			statusStyle = styleFooterStatusErr
		}
		statusText = icon + " " + m.statusMsg
	}

	// Centre: item count (only when PR list is loaded).
	var countText string
	if m.itemCount > 0 && !m.prLoading {
		cur := m.prCursor + 1
		if len(m.prs) == 0 {
			cur = 0
		}
		countText = fmt.Sprintf("%d/%d items", cur, m.itemCount)
	}

	// Right: key hints (view-specific).
	hintText := m.footerHints()

	// Fit the right side first, truncating hints when necessary.
	rightText := hintText
	if countText != "" {
		rightText = countText + "  " + hintText
	}
	maxRightW := contentW - 1
	if maxRightW < 0 {
		maxRightW = 0
	}
	if lipgloss.Width(rightText) > maxRightW {
		if countText != "" {
			countW := lipgloss.Width(countText)
			if countW >= maxRightW {
				rightText = truncateFooterText(countText, maxRightW)
			} else {
				availableHintW := maxRightW - countW - 2
				if availableHintW > 0 {
					rightText = countText + "  " + truncateFooterText(hintText, availableHintW)
				} else {
					rightText = countText
				}
			}
		} else {
			rightText = truncateFooterText(hintText, maxRightW)
		}
	}

	var right string
	if countText != "" && strings.HasPrefix(rightText, countText) {
		hintOnly := strings.TrimPrefix(rightText, countText)
		right = styleFooterMuted.Render(countText) + styleFooterHint.Render(hintOnly)
	} else {
		right = styleFooterHint.Render(rightText)
	}

	// Truncate the left side to fit beside the right side.
	rightW := lipgloss.Width(right)
	maxLeftW := contentW - rightW - 1
	if maxLeftW < 0 {
		maxLeftW = 0
	}
	statusText = truncateFooterText(statusText, maxLeftW)
	left := statusStyle.Render(statusText)

	leftW := lipgloss.Width(left)
	rightW = lipgloss.Width(right)
	gap := contentW - leftW - rightW
	if gap < 1 {
		gap = 1
	}
	bar := left + strings.Repeat(" ", gap) + right
	return styleAppFooter.Width(w).Render(bar)
}

// ── Splash screen ─────────────────────────────────────────────────────────────

func (m model) renderSplash() string {
	w := m.width
	if w <= 0 {
		w = 80
	}

	var b strings.Builder

	b.WriteString(renderWordmark(w))
	b.WriteString("\n\n")

	// Tagline — centered under the logo
	tagline := styleMuted.Render("       lather") +
		styleTeal.Render(" "+IconSep+" ") +
		styleMuted.Render("rinse") +
		styleTeal.Render(" "+IconSep+" ") +
		styleMuted.Render("repeat")
	b.WriteString(tagline)
	b.WriteString("\n\n")

	// Loading status with animated spinner
	status := "       " + m.splashSpinner.View() + " "
	if m.repo != "" {
		status += styleSplashStatus.Render(m.repo)
	} else {
		status += styleSplashStatus.Render("detecting repository…")
	}
	b.WriteString(status)
	b.WriteString("\n\n")

	// Skip hint
	b.WriteString(styleMuted.Render("       press any key to skip"))

	return b.String()
}

// ── PR Picker ─────────────────────────────────────────────────────────────────

func (m model) renderPRPicker(w int) string {
	var b strings.Builder

	// ── PR list ───────────────────────────────────────────────────────────────
	if m.prLoading {
		b.WriteString("  " + m.prSpinner.View() + " " + styleMuted.Render("Fetching open PRs…") + "\n")
	} else if m.prLoadErr != "" {
		b.WriteString(styleErr.Render("  "+IconCross+" "+m.prLoadErr) + "\n")
		b.WriteString(styleMuted.Render("  Press # to enter a PR number manually") + "\n")
	} else if len(m.prs) == 0 {
		if m.repo == "" {
			b.WriteString(styleMuted.Render("  No repo detected. Run from inside a git checkout.") + "\n")
		} else {
			b.WriteString(styleMuted.Render("  No open PRs found.") + "\n")
		}
		b.WriteString(styleMuted.Render("  Press # to enter a PR number manually") + "\n")
	} else {
		// Section title with count
		count := styleMuted.Render(fmt.Sprintf("  %d open", len(m.prs)))
		b.WriteString(count + "\n")

		// Make branchW dynamic so the row never exceeds w on narrow terminals.
		// Reserve ~18 chars for the PR number + separators, then split the
		// remaining space 30/70 between branch and title. Only enforce the
		// normal minimum widths when there is enough room for both columns.
		available := max(0, w-18)
		branchW := available * 30 / 100
		if branchW > 28 {
			branchW = 28
		}
		titleW := available - branchW
		if available >= 26 {
			if branchW < 10 {
				branchW = 10
			}
			titleW = available - branchW
			if titleW < 16 {
				titleW = 16
				branchW = available - titleW
			}
		}

		for i, p := range m.prs {
			num := fmtPRNumber(p.Number)
			branch := truncate(p.HeadRefName, branchW)
			title := truncate(p.Title, titleW)
			isCurrent := p.HeadRefName == m.currentBranch

			if i == m.prCursor {
				bar := styleSelectedBar.Render(IconThickBar)
				sNum := stylePRNum.Render(fmt.Sprintf("%-6s", num))
				sBranch := styleSelected.Render(fmt.Sprintf("%-*s", branchW, branch))
				if isCurrent {
					sBranch = styleTeal.Render(fmt.Sprintf("%-*s", branchW, branch))
				}
				sTitle := lipgloss.NewStyle().Foreground(text).Render(title)
				marker := ""
				if isCurrent {
					marker = " " + styleTeal.Render(IconArrow)
				}
				b.WriteString(" " + bar + " " + sNum + " " + sBranch + "  " + sTitle + marker)
			} else {
				uNum := stylePRNumMuted.Render(fmt.Sprintf("%-6s", num))
				uBranch := styleUnselected.Render(fmt.Sprintf("%-*s", branchW, branch))
				if isCurrent {
					uBranch = styleTeal.Render(fmt.Sprintf("%-*s", branchW, branch))
				}
				uTitle := styleMuted.Render(title)
				marker := ""
				if isCurrent {
					marker = " " + styleTeal.Render(IconArrow)
				}
				b.WriteString("    " + uNum + " " + uBranch + "  " + uTitle + marker)
			}
			b.WriteString("\n")
		}
	}

	// ── Error ─────────────────────────────────────────────────────────────────
	if m.errMsg != "" {
		b.WriteString("\n" + styleErr.Render("  "+IconCross+" "+m.errMsg) + "\n")
	}

	// ── Settings ribbon ───────────────────────────────────────────────────────
	b.WriteString(m.renderRibbon(w))

	// ── Key hints via bubbles/help ────────────────────────────────────────────
	b.WriteString("\n  " + m.help.View(Keys))

	return b.String()
}

func (m model) renderRibbon(w int) string {
	rName := shortRunnerName(m.runnerIdx)

	modelStr := m.modelOverride
	if modelStr == "" {
		modelStr = runners[m.runnerIdx].defaultModel
	}

	dot := styleMuted.Render(" " + IconSep + " ")

	parts := []string{
		styleVal.Render(rName),
		styleMuted.Render(truncate(modelStr, 30)),
	}
	if m.reflect {
		branch := m.reflectBranch
		if branch == "" {
			branch = m.defaultBranch
		}
		parts = append(parts, styleTeal.Render("reflect "+IconArrow+" "+branch))
	} else {
		parts = append(parts, styleMuted.Render("reflect off"))
	}
	if m.autoMerge {
		parts = append(parts, styleTeal.Render("auto-merge on"))
	} else {
		parts = append(parts, styleMuted.Render("auto-merge off"))
	}

	ribbonW := clamp(w-2, 0, 200)
	return "\n" + styleRibbon.Width(ribbonW).Render(strings.Join(parts, dot))
}

// ── Manual PR ─────────────────────────────────────────────────────────────────

func (m model) renderManualPR(w, h int) string {
	var b strings.Builder

	b.WriteString(styleStep.Render("  Enter PR number"))
	b.WriteString("\n\n")
	b.WriteString(m.input.View())
	b.WriteString("\n")

	if m.errMsg != "" {
		b.WriteString("\n" + styleErr.Render("  "+IconCross+" "+m.errMsg) + "\n")
	}

	dot := styleMuted.Render(" " + IconSep + " ")
	hints := renderKeyHint("enter", "launch") + dot + renderKeyHint("esc", "back")
	b.WriteString("\n  " + hints)

	return lipgloss.Place(w, h, lipgloss.Left, lipgloss.Top, b.String())
}

// ── Settings overlay ──────────────────────────────────────────────────────────

func (m model) renderSettings() string {
	title := gradientString("SETTINGS", mauve, lavender, true)

	// Runner — show name + description
	r := runners[m.settingsRunnerIdx]
	runnerVal := styleMuted.Render("◂ ") +
		styleVal.Render(r.name) +
		styleMuted.Render("  "+r.desc) +
		styleMuted.Render(" ▸")

	// Model
	var modelVal string
	if m.settingsEditingModel {
		modelVal = m.settingsModelInput.View()
	} else {
		v := m.settingsModelInput.Value()
		if v == "" {
			modelVal = styleMuted.Render(runners[m.settingsRunnerIdx].defaultModel) +
				styleMuted.Render("  (default)")
		} else {
			modelVal = styleVal.Render(v)
		}
	}

	// Reflect toggle
	reflectVal := styleMuted.Render(IconRadioOff + " off")
	if m.settingsReflect {
		reflectVal = styleTeal.Render(IconRadioOn+" on") +
			styleMuted.Render("  extract coding rules after each cycle")
	}

	// Branch
	var branchVal string
	if m.settingsEditingBranch {
		branchVal = m.settingsBranchInput.View()
	} else {
		v := m.settingsBranchInput.Value()
		if v == "" {
			v = m.defaultBranch
		}
		branchVal = styleVal.Render(v)
	}

	// Auto-merge toggle
	amVal := styleMuted.Render(IconRadioOff + " off")
	if m.settingsAutoMerge {
		amVal = styleTeal.Render(IconRadioOn+" on") +
			styleMuted.Render("  merge PR automatically when approved")
	}

	type srow struct {
		label string
		value string
		field settingsField
	}

	rows := []srow{
		{"runner", runnerVal, sfRunner},
		{"model", modelVal, sfModel},
		{"reflect", reflectVal, sfReflect},
	}
	if m.settingsReflect {
		rows = append(rows, srow{"  branch", branchVal, sfReflectBranch})
	}
	rows = append(rows, srow{"auto-merge", amVal, sfAutoMerge})

	var lines []string
	for _, r := range rows {
		cursor := "  "
		if r.field == m.settingsFocus {
			cursor = styleSelected.Render(IconArrow + " ")
		}
		lines = append(lines, cursor+styleKey.Render(r.label)+"  "+r.value)
	}

	lines = append(lines, "")

	saveCursor := "  "
	if m.settingsFocus == sfSave {
		saveCursor = styleSelected.Render(IconArrow + " ")
	}
	cancelCursor := "  "
	if m.settingsFocus == sfCancel {
		cancelCursor = styleSelected.Render(IconArrow + " ")
	}
	lines = append(lines, saveCursor+styleTeal.Render(IconCheck+" save"))
	lines = append(lines, cancelCursor+styleMuted.Render(IconCross+" cancel"))

	dot := styleMuted.Render(" " + IconSep + " ")
	hints := "\n  " + strings.Join([]string{
		renderKeyHint("↑↓", "move"),
		renderKeyHint("←→", "cycle"),
		renderKeyHint("space", "toggle"),
		renderKeyHint("enter", "edit"),
		renderKeyHint("esc", "back"),
	}, dot)

	return styleSettingsBox.Render(title + "\n\n" + strings.Join(lines, "\n") + hints)
}

// ── Help overlay ──────────────────────────────────────────────────────────────

func (m model) renderHelp() string {
	helpStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(surface).
		Padding(1, 4)

	title := gradientString("KEYBOARD SHORTCUTS", mauve, lavender, true)

	type krow struct{ key, desc string }
	rows := []krow{
		{"enter", "launch review cycle on selected PR"},
		{"↑↓ / jk", "navigate PR list"},
		{"g / G", "jump to top / bottom"},
		{"s", "open settings"},
		{"#", "type PR number manually"},
		{"r", "refresh PR list from GitHub"},
		{"?", "toggle this help"},
		{"q / ^C", "close help / quit rinse"},
	}

	var lines []string
	for _, r := range rows {
		lines = append(lines,
			styleHintKey.Render(fmt.Sprintf("  %-10s", r.key))+"  "+
				lipgloss.NewStyle().Foreground(subtext).Render(r.desc))
	}

	return helpStyle.Render(title + "\n\n" + strings.Join(lines, "\n"))
}

// renderHelpOverlay renders the bubbles/help overlay centered on the screen.
func (m model) renderHelpOverlay(w, h int) string {
	overlayStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(mauve).
		Background(crust).
		Padding(1, 3)

	title := gradientString("KEYBOARD SHORTCUTS", mauve, lavender, true)
	helpContent := m.help.View(Keys)
	content := overlayStyle.Render(title + "\n\n" + helpContent + "\n\n" +
		styleHintDesc.Render("press ?, q, or esc to close"))

	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, content)
}
