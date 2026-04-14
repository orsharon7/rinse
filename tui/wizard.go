package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	splashReady   bool // true once PR data has loaded

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

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(mauve)

	return model{
		view:          viewSplash,
		splashSpinner: sp,
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
	}
}

// ── Init ──────────────────────────────────────────────────────────────────────

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.splashSpinner.Tick,
		// Minimum splash duration so the branding is visible.
		tea.Tick(1500*time.Millisecond, func(t time.Time) tea.Msg { return splashDoneMsg{} }),
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
		if m.view == viewSplash {
			var cmd tea.Cmd
			m.splashSpinner, cmd = m.splashSpinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case splashDoneMsg:
		m.splashReady = true
		// Data already arrived while splash was showing → transition now.
		if !m.prLoading {
			m.view = viewPRPicker
		}
		return m, nil

	case prListMsg:
		m.prs = []pr(msg)
		m.prLoading = false
		m.prCursor = 0
		if m.currentBranch != "" {
			for i, p := range m.prs {
				if p.HeadRefName == m.currentBranch {
					m.prCursor = i
					break
				}
			}
		}
		// Splash timer already elapsed → transition.
		if m.view == viewSplash && m.splashReady {
			m.view = viewPRPicker
		}
		return m, nil

	case prListErrMsg:
		m.prLoading = false
		m.prLoadErr = msg.err.Error()
		// Transition even on error.
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
	key := msg.String()

	if key == "ctrl+c" {
		return m, tea.Quit
	}

	switch m.view {

	case viewSplash:
		if key == "q" {
			return m, tea.Quit
		}
		// Any key skips the splash.
		m.view = viewPRPicker
		return m, nil

	case viewPRPicker:
		if m.prLoading {
			if key == "q" {
				return m, tea.Quit
			}
			return m, nil
		}
		switch key {
		case "q":
			return m, tea.Quit
		case "?":
			m.view = viewHelp
			return m, nil
		case "#":
			m.view = viewManualPR
			m.input.SetValue("")
			m.input.Focus()
			m.errMsg = ""
			return m, textinput.Blink
		case "s":
			return m.openSettings()
		case "r":
			if m.repo != "" {
				m.prLoading = true
				m.prs = nil
				m.prLoadErr = ""
				return m, fetchPRs(m.repo)
			}
		case "up", "k":
			if len(m.prs) > 0 && m.prCursor > 0 {
				m.prCursor--
			}
		case "down", "j":
			if len(m.prs) > 0 && m.prCursor < len(m.prs)-1 {
				m.prCursor++
			}
		case "enter":
			if len(m.prs) > 0 && m.prCursor < len(m.prs) {
				m.prNum = fmt.Sprintf("%d", m.prs[m.prCursor].Number)
				m.prTitle = m.prs[m.prCursor].Title
				return m.launch()
			}
		}

	case viewManualPR:
		switch key {
		case "esc":
			m.view = viewPRPicker
			m.errMsg = ""
			return m, nil
		case "enter":
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
		return m.handleSettingsKey(key, msg)

	case viewHelp:
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

func (m model) handleSettingsKey(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.settingsEditingModel {
		switch key {
		case "enter", "esc":
			m.settingsEditingModel = false
			m.settingsModelInput.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.settingsModelInput, cmd = m.settingsModelInput.Update(msg)
		return m, cmd
	}
	if m.settingsEditingBranch {
		switch key {
		case "enter", "esc":
			m.settingsEditingBranch = false
			m.settingsBranchInput.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.settingsBranchInput, cmd = m.settingsBranchInput.Update(msg)
		return m, cmd
	}

	maxField := sfCancel
	switch key {
	case "esc":
		m.view = viewPRPicker
		return m, nil
	case "up", "k":
		if m.settingsFocus > 0 {
			m.settingsFocus--
			if m.settingsFocus == sfReflectBranch && !m.settingsReflect {
				m.settingsFocus--
			}
		}
	case "down", "j", "tab":
		if m.settingsFocus < maxField {
			m.settingsFocus++
			if m.settingsFocus == sfReflectBranch && !m.settingsReflect {
				m.settingsFocus++
			}
		}
	case "left", "h":
		if m.settingsFocus == sfRunner && m.settingsRunnerIdx > 0 {
			m.settingsRunnerIdx--
			m.settingsModelInput.Placeholder = runners[m.settingsRunnerIdx].defaultModel
		}
	case "right", "l":
		if m.settingsFocus == sfRunner && m.settingsRunnerIdx < len(runners)-1 {
			m.settingsRunnerIdx++
			m.settingsModelInput.Placeholder = runners[m.settingsRunnerIdx].defaultModel
		}
	case " ":
		switch m.settingsFocus {
		case sfReflect:
			m.settingsReflect = !m.settingsReflect
		case sfAutoMerge:
			m.settingsAutoMerge = !m.settingsAutoMerge
		}
	case "enter":
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
		// Scripts live in pr-review/ which is a sibling of tui/ (the binary's dir).
		// Search: <binDir>/pr-review/, <binDir>/../pr-review/, <binDir> itself.
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

	switch m.view {
	case viewSplash:
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, m.renderSplash())
	case viewHelp:
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, m.renderHelp())
	case viewSettings:
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, m.renderSettings())
	case viewManualPR:
		return m.renderManualPR(w, h)
	default:
		return m.renderPRPicker(w)
	}
}

// ── Splash screen ─────────────────────────────────────────────────────────────

func (m model) renderSplash() string {
	var b strings.Builder

	// ASCII art logo
	b.WriteString(styleSplashBox.Render(splashLogo))
	b.WriteString("\n\n")

	// Version
	b.WriteString(styleSplashVersion.Render("          v" + version))
	b.WriteString("\n\n")

	// Loading status with spinner
	status := m.splashSpinner.View() + " "
	if m.repo != "" {
		status += styleSplashStatus.Render("loading " + m.repo + "…")
	} else {
		status += styleSplashStatus.Render("detecting repository…")
	}
	b.WriteString("          " + status)

	return b.String()
}

// ── PR Picker (home screen) ───────────────────────────────────────────────────
// Redesigned with Crush-style thick left border and cleaner layout.

func (m model) renderPRPicker(w int) string {
	var b strings.Builder

	contentW := clamp(w-4, 1, 140)

	// ── Logo header ───────────────────────────────────────────────────────────
	b.WriteString("\n")
	b.WriteString(renderLogo(m.repo, m.currentBranch))
	b.WriteString("\n")
	b.WriteString("  " + renderSeparator(contentW))
	b.WriteString("\n")

	// ── PR list ───────────────────────────────────────────────────────────────
	if m.prLoading {
		b.WriteString(styleMuted.Render("  Loading PRs…") + "\n")
	} else if m.prLoadErr != "" {
		b.WriteString(styleErr.Render("  "+IconCross+" "+m.prLoadErr) + "\n")
		b.WriteString(styleMuted.Render("  Press # to enter a PR number manually") + "\n")
	} else if len(m.prs) == 0 {
		if m.repo == "" {
			b.WriteString(styleMuted.Render("  No repo detected. Run from inside a git checkout.") + "\n")
		} else {
			b.WriteString(styleMuted.Render("  No open PRs") + "\n")
		}
		b.WriteString(styleMuted.Render("  Press # to enter a PR number manually") + "\n")
	} else {
		branchW := 28
		titleW := w - branchW - 18
		if titleW < 16 {
			titleW = 16
		}

		for i, p := range m.prs {
			num := fmtPRNumber(p.Number)
			branch := truncate(p.HeadRefName, branchW)
			title := truncate(p.Title, titleW)
			isCurrent := p.HeadRefName == m.currentBranch

			if i == m.prCursor {
				// ▌ selected item — thick left bar (Crush-style)
				bar := styleSelectedBar.Render(IconThickBar)
				sNum := stylePRNum.Render(fmt.Sprintf("%-6s", num))
				sBranch := styleSelected.Render(fmt.Sprintf("%-*s", branchW, branch))
				if isCurrent {
					sBranch = styleTeal.Render(fmt.Sprintf("%-*s", branchW, branch))
				}
				sTitle := lipgloss.NewStyle().Foreground(text).Render(title)
				marker := ""
				if isCurrent {
					marker = " " + styleTeal.Render("←")
				}
				b.WriteString(" " + bar + " " + sNum + " " + sBranch + "  " + sTitle + marker)
			} else {
				// Normal item — 3-space indent to align with ▌ items
				uNum := stylePRNumMuted.Render(fmt.Sprintf("%-6s", num))
				uBranch := styleUnselected.Render(fmt.Sprintf("%-*s", branchW, branch))
				if isCurrent {
					uBranch = styleTeal.Render(fmt.Sprintf("%-*s", branchW, branch))
				}
				uTitle := styleMuted.Render(title)
				marker := ""
				if isCurrent {
					marker = " " + styleTeal.Render("←")
				}
				b.WriteString("    " + uNum + " " + uBranch + "  " + uTitle + marker)
			}
			b.WriteString("\n")
		}
		b.WriteString("  " + renderSeparator(contentW))
		b.WriteString("\n")
	}

	// ── Error ─────────────────────────────────────────────────────────────────
	if m.errMsg != "" {
		b.WriteString("\n" + styleErr.Render("  "+IconCross+" "+m.errMsg) + "\n")
	}

	// ── Settings ribbon ───────────────────────────────────────────────────────
	b.WriteString(m.renderRibbon(w))

	// ── Key hints ─────────────────────────────────────────────────────────────
	hints := strings.Join([]string{
		renderKeyHint("enter", "launch"),
		renderKeyHint("s", "settings"),
		renderKeyHint("#", "type PR"),
		renderKeyHint("r", "refresh"),
		renderKeyHint("?", "help"),
		renderKeyHint("q", "quit"),
	}, "  ")
	b.WriteString("\n  " + hints)

	return b.String()
}

func (m model) renderRibbon(w int) string {
	rName := shortRunnerName(m.runnerIdx)

	modelStr := m.modelOverride
	if modelStr == "" {
		modelStr = runners[m.runnerIdx].defaultModel
	}

	sep := styleMuted.Render(" " + IconSep + " ")

	parts := []string{
		styleVal.Render(rName),
		styleVal.Render(truncate(modelStr, 30)),
	}
	if m.reflect {
		branch := m.reflectBranch
		if branch == "" {
			branch = m.defaultBranch
		}
		parts = append(parts, styleMuted.Render("reflect ")+styleTeal.Render("on "+IconArrow+" "+branch))
	} else {
		parts = append(parts, styleMuted.Render("reflect off"))
	}
	if m.autoMerge {
		parts = append(parts, styleMuted.Render("auto-merge ")+styleTeal.Render("on"))
	} else {
		parts = append(parts, styleMuted.Render("auto-merge off"))
	}

	ribbonW := clamp(w-2, 0, 200)
	return "\n" + styleRibbon.Width(ribbonW).Render(strings.Join(parts, sep))
}

// ── Manual PR ─────────────────────────────────────────────────────────────────

func (m model) renderManualPR(w, h int) string {
	content := "\n" + styleStep.Render("  Enter PR number") + "\n\n" +
		m.input.View() + "\n"
	if m.errMsg != "" {
		content += "\n" + styleErr.Render("  "+IconCross+" "+m.errMsg)
	}
	hints := "  " + renderKeyHint("enter", "launch") + "  " + renderKeyHint("esc", "back")
	content += "\n\n" + hints
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, content)
}

// ── Settings overlay ──────────────────────────────────────────────────────────

func (m model) renderSettings() string {
	title := styleStep.Render(IconDiamond + "  settings")

	// Runner — show name + description
	r := runners[m.settingsRunnerIdx]
	runnerVal := styleMuted.Render("◂ ") + styleVal.Render(r.name) + styleMuted.Render(" — "+r.desc) + styleMuted.Render(" ▸")

	// Model
	var modelVal string
	if m.settingsEditingModel {
		modelVal = m.settingsModelInput.View()
	} else {
		v := m.settingsModelInput.Value()
		if v == "" {
			modelVal = styleMuted.Render(runners[m.settingsRunnerIdx].defaultModel + " (default)")
		} else {
			modelVal = styleVal.Render(v)
		}
	}

	// Reflect toggle
	reflectVal := styleMuted.Render(IconRadioOff + " off")
	if m.settingsReflect {
		reflectVal = styleTeal.Render(IconRadioOn + " on")
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
		amVal = styleTeal.Render(IconRadioOn + " on")
	}

	type srow struct {
		label string
		value string
		field settingsField
	}

	rows := []srow{
		{"runner", runnerVal, sfRunner},
		{"model", modelVal, sfModel},
		{"reflection", reflectVal, sfReflect},
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
	lines = append(lines, saveCursor+styleTeal.Render(IconCheck+" Save"))
	lines = append(lines, cancelCursor+styleMuted.Render(IconCross+" Cancel"))

	hints := "\n  " + strings.Join([]string{
		renderKeyHint("↑↓", "move"),
		renderKeyHint("←→", "cycle runner"),
		renderKeyHint("space", "toggle"),
		renderKeyHint("enter", "edit"),
		renderKeyHint("esc", "cancel"),
	}, "  ")

	return styleSettingsBox.Render(title + "\n\n" + strings.Join(lines, "\n") + hints)
}

// ── Help overlay ──────────────────────────────────────────────────────────────

func (m model) renderHelp() string {
	helpStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(mauve).
		Padding(1, 4)

	title := styleStep.Render(IconDiamond + "  keyboard shortcuts")

	type krow struct{ key, desc string }
	rows := []krow{
		{"enter", "launch PR review cycle"},
		{"↑↓ / jk", "navigate PR list"},
		{"s", "open settings"},
		{"#", "type PR number manually"},
		{"r", "refresh PR list"},
		{"?", "this help (any key to close)"},
		{"q / ^C", "quit"},
	}

	var lines []string
	for _, r := range rows {
		lines = append(lines,
			styleHintKey.Render(fmt.Sprintf("%-10s", r.key))+"  "+styleVal.Render(r.desc))
	}

	return helpStyle.Render(title + "\n\n" + strings.Join(lines, "\n"))
}
