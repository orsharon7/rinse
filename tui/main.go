package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Palette (Catppuccin Macchiato) ────────────────────────────────────────────

var (
	mauve    = lipgloss.Color("#C6A0F6")
	lavender = lipgloss.Color("#B7BDF8")
	teal     = lipgloss.Color("#8BD5CA")
	red      = lipgloss.Color("#ED8796")
	yellow   = lipgloss.Color("#EED49F")
	surface  = lipgloss.Color("#363A4F")
	overlay  = lipgloss.Color("#6E738D")
	text     = lipgloss.Color("#CAD3F5")
	subtext  = lipgloss.Color("#A5ADCB")
	crust    = lipgloss.Color("#181926")
)

var (
	styleBanner = lipgloss.NewStyle().
			Bold(true).
			Foreground(mauve).
			Padding(0, 1)

	styleKey   = lipgloss.NewStyle().Foreground(overlay).Width(16)
	styleVal   = lipgloss.NewStyle().Foreground(lavender).Bold(true)
	styleMuted = lipgloss.NewStyle().Foreground(overlay)
	styleStep  = lipgloss.NewStyle().Foreground(mauve).Bold(true)
	styleErr   = lipgloss.NewStyle().Foreground(red)
	styleTeal  = lipgloss.NewStyle().Foreground(teal).Bold(true)

	styleSelected   = lipgloss.NewStyle().Foreground(mauve).Bold(true)
	styleUnselected = lipgloss.NewStyle().Foreground(subtext)

	styleRibbon = lipgloss.NewStyle().
			Foreground(subtext).
			BorderStyle(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(overlay).
			Padding(0, 1)

	styleSettingsBox = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(mauve).
				Padding(1, 3)
)

// ── PR list (fetched async) ───────────────────────────────────────────────────

type pr struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	HeadRefName string `json:"headRefName"`
}

type prListMsg []pr
type prListErrMsg struct{ err error }
type defaultBranchMsg string
type currentBranchMsg string

func fetchPRs(repo string) tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command("gh", "pr", "list",
			"--repo", repo,
			"--json", "number,title,headRefName",
			"--limit", "20",
		).Output()
		if err != nil {
			return prListErrMsg{err}
		}
		var prs []pr
		if err := json.Unmarshal(out, &prs); err != nil {
			return prListErrMsg{err}
		}
		return prListMsg(prs)
	}
}

func fetchDefaultBranch(repo string) tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command("gh", "repo", "view", repo,
			"--json", "defaultBranchRef",
			"--jq", ".defaultBranchRef.name",
		).Output()
		if err != nil {
			return defaultBranchMsg("main")
		}
		return defaultBranchMsg(strings.TrimSpace(string(out)))
	}
}

func fetchCurrentBranch() tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command("git", "branch", "--show-current").Output()
		if err != nil {
			return currentBranchMsg("")
		}
		return currentBranchMsg(strings.TrimSpace(string(out)))
	}
}

func detectRepo() string {
	out, err := exec.Command("gh", "repo", "view",
		"--json", "nameWithOwner",
		"--jq", ".nameWithOwner",
	).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func detectCWD() string {
	d, _ := os.Getwd()
	return d
}

// ── Runner options ────────────────────────────────────────────────────────────

type runner struct {
	label        string
	script       string
	defaultModel string
}

var runners = []runner{
	{"opencode — GitHub Copilot · no API key needed  ✦", "pr-review-opencode.sh", "github-copilot/claude-sonnet-4.6"},
	{"claude  — Claude Code · requires Anthropic key", "pr-review-claude-v2.sh", "claude-sonnet-4-6"},
}

// ── View mode ─────────────────────────────────────────────────────────────────

type viewMode int

const (
	viewPRPicker viewMode = iota
	viewManualPR
	viewSettings
	viewHelp
	viewDone
)

// ── Settings field focus ──────────────────────────────────────────────────────

type settingsField int

const (
	sfRunner settingsField = iota
	sfModel
	sfReflect
	sfReflectBranch
	sfAutoMerge
	sfSave
	sfCancel
)

// ── Model ─────────────────────────────────────────────────────────────────────

type model struct {
	width  int
	height int

	view viewMode

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
	settingsBranchEdited  bool // true once the user has entered branch edit mode

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

	// When repo detection succeeded, it was derived from the current checkout,
	// so keep path aligned with the current working directory. Only fall back
	// to a persisted path when no repo was detected from CWD.
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
	ti.Prompt = "  PR# ❯ "
	ti.CharLimit = 10
	ti.Placeholder = "e.g. 42"

	mi := textinput.New()
	mi.Cursor.Style = lipgloss.NewStyle().Foreground(mauve)
	mi.PromptStyle = lipgloss.NewStyle().Foreground(mauve)
	mi.TextStyle = lipgloss.NewStyle().Foreground(text)
	mi.Prompt = "  ❯ "
	mi.CharLimit = 80

	bi := textinput.New()
	bi.Cursor.Style = lipgloss.NewStyle().Foreground(mauve)
	bi.PromptStyle = lipgloss.NewStyle().Foreground(mauve)
	bi.TextStyle = lipgloss.NewStyle().Foreground(text)
	bi.Prompt = "  ❯ "
	bi.CharLimit = 80

	// If a repo config exists, use its values verbatim; otherwise fall back to
	// the global last-used values so a repo with reflect:false isn't forced on.
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

	// Use per-repo branch if set; leave empty so the detected defaultBranch
	// will be applied once defaultBranchMsg arrives — never borrow a branch
	// from a different repo via cfg.LastBranch.
	reflectBranch := rc.Branch

	return model{
		view:          viewPRPicker,
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
	if m.repo != "" {
		return tea.Batch(
			fetchPRs(m.repo),
			fetchDefaultBranch(m.repo),
			fetchCurrentBranch(),
		)
	}
	return nil
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
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
		return m, nil

	case prListErrMsg:
		m.prLoading = false
		m.prLoadErr = msg.err.Error()
		return m, nil

	case defaultBranchMsg:
		m.defaultBranch = string(msg)
		if m.reflectBranch == "" {
			m.reflectBranch = m.defaultBranch
		}
		// If the settings overlay is already open, sync the branch input so the
		// user sees the correct default rather than the "main" placeholder that
		// was set at init time.
		if m.view == viewSettings {
			m.settingsBranchInput.Placeholder = m.defaultBranch
			// Only auto-fill when the user has not explicitly edited the branch field.
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

	// ── PR Picker (home) ──────────────────────────────────────────────────────
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

	// ── Manual PR ─────────────────────────────────────────────────────────────
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

	// ── Settings ──────────────────────────────────────────────────────────────
	case viewSettings:
		return m.handleSettingsKey(key, msg)

	// ── Help ──────────────────────────────────────────────────────────────────
	case viewHelp:
		m.view = viewPRPicker
		return m, nil
	}

	return m, nil
}

// ── Settings overlay ──────────────────────────────────────────────────────────

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
	// Text field editing mode
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

	scriptDir := os.Getenv("PR_REVIEW_SCRIPT_DIR")
	if scriptDir == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("could not determine script directory: %w", err)
		}
		binDir := filepath.Dir(exe)
		// Scripts live in pr-review/ which is a sibling of tui/ (the binary's dir).
		// Try: <binDir>/pr-review/, then <binDir>/../pr-review/, then <binDir> itself.
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

// ── PR Picker (home screen) ───────────────────────────────────────────────────

func (m model) renderPRPicker(w int) string {
	var b strings.Builder

	// Header
	repoStr := m.repo
	if repoStr == "" {
		repoStr = "(no repo detected)"
	}
	header := styleBanner.Render("pr-review") +
		styleMuted.Render("  ─  ") +
		styleVal.Render(repoStr)
	if m.currentBranch != "" {
		header += styleMuted.Render("  on ") + styleTeal.Render(m.currentBranch)
	}
	b.WriteString("\n  " + header + "\n\n")

	// PR list
	if m.prLoading {
		b.WriteString(styleMuted.Render("  Fetching open PRs…") + "\n")
	} else if m.prLoadErr != "" {
		b.WriteString(styleErr.Render("  ✗ "+m.prLoadErr) + "\n")
		b.WriteString(styleMuted.Render("  Press # to enter a PR number manually") + "\n")
	} else if len(m.prs) == 0 {
		if m.repo == "" {
			b.WriteString(styleMuted.Render("  No repo detected. Run from inside a git checkout.") + "\n")
		} else {
			b.WriteString(styleMuted.Render("  No open PRs") + "\n")
		}
		b.WriteString(styleMuted.Render("  Press # to enter a PR number manually") + "\n")
	} else {
		sep := styleMuted.Render("  " + strings.Repeat("─", clamp(w-4, 0, 120)))
		b.WriteString(sep + "\n")

		for i, p := range m.prs {
			branchW := 26
			titleW := w - branchW - 16
			if titleW < 20 {
				titleW = 20
			}

			num := fmt.Sprintf("#%-4d", p.Number)
			branch := truncate(p.HeadRefName, branchW)
			title := truncate(p.Title, titleW)

			isCurrent := p.HeadRefName == m.currentBranch

			if i == m.prCursor {
				sNum := styleSelected.Render(fmt.Sprintf("%-6s", num))
				sBranch := styleSelected.Render(fmt.Sprintf("%-*s", branchW, branch))
				if isCurrent {
					sBranch = styleTeal.Bold(true).Render(fmt.Sprintf("%-*s", branchW, branch))
				}
				sTitle := lipgloss.NewStyle().Foreground(text).Render(title)
				marker := ""
				if isCurrent {
					marker = styleTeal.Render(" ←")
				}
				b.WriteString(styleSelected.Render("  ❯ ") + sNum + "  " + sBranch + "  " + sTitle + marker)
			} else {
				uNum := styleUnselected.Render(fmt.Sprintf("%-6s", num))
				uBranch := styleUnselected.Render(fmt.Sprintf("%-*s", branchW, branch))
				if isCurrent {
					uBranch = styleTeal.Render(fmt.Sprintf("%-*s", branchW, branch))
				}
				uTitle := styleMuted.Render(title)
				marker := ""
				if isCurrent {
					marker = styleTeal.Render(" ←")
				}
				b.WriteString("    " + uNum + "  " + uBranch + "  " + uTitle + marker)
			}
			b.WriteString("\n")
		}
		b.WriteString(sep + "\n")
	}

	// Error
	if m.errMsg != "" {
		b.WriteString("\n" + styleErr.Render("  ✗ "+m.errMsg) + "\n")
	}

	// Settings ribbon
	b.WriteString(m.renderRibbon(w))

	// Key hints
	b.WriteString("\n" + styleMuted.Render("  enter=launch  s=settings  #=type PR  r=refresh  ?=help  q=quit"))

	return b.String()
}

func (m model) renderRibbon(w int) string {
	r := runners[m.runnerIdx]
	rName, _, found := strings.Cut(r.label, " —")
	if !found {
		rName = r.label
	}
	rName = strings.TrimSpace(rName)

	modelStr := m.modelOverride
	if modelStr == "" {
		modelStr = r.defaultModel
	}

	parts := []string{
		styleMuted.Render("runner ") + styleVal.Render(rName),
		styleMuted.Render("model ") + styleVal.Render(truncate(modelStr, 30)),
	}
	if m.reflect {
		branch := m.reflectBranch
		if branch == "" {
			branch = m.defaultBranch
		}
		parts = append(parts, styleMuted.Render("reflect ")+styleTeal.Render("on → "+branch))
	} else {
		parts = append(parts, styleMuted.Render("reflect ")+styleMuted.Render("off"))
	}
	if m.autoMerge {
		parts = append(parts, styleMuted.Render("auto-merge ")+styleTeal.Render("on"))
	} else {
		parts = append(parts, styleMuted.Render("auto-merge ")+styleMuted.Render("off"))
	}

	ribbonW := clamp(w-2, 0, 200)
	return "\n" + styleRibbon.Width(ribbonW).Render(strings.Join(parts, "  ·  "))
}

// ── Manual PR ─────────────────────────────────────────────────────────────────

func (m model) renderManualPR(w, h int) string {
	content := "\n" + styleStep.Render("  Enter PR number") + "\n\n" +
		m.input.View() + "\n"
	if m.errMsg != "" {
		content += "\n" + styleErr.Render("  ✗ "+m.errMsg)
	}
	content += "\n\n" + styleMuted.Render("  enter=launch  esc=back")
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, content)
}

// ── Settings overlay ──────────────────────────────────────────────────────────

func (m model) renderSettings() string {
	title := styleStep.Render("⚙  settings")

	// Runner
	rName, _, found := strings.Cut(runners[m.settingsRunnerIdx].label, " —")
	if !found {
		rName = runners[m.settingsRunnerIdx].label
	}
	runnerVal := "◂ " + strings.TrimSpace(rName) + " ▸"

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
	reflectVal := styleMuted.Render("○ off")
	if m.settingsReflect {
		reflectVal = styleTeal.Render("● on")
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
	amVal := styleMuted.Render("○ off")
	if m.settingsAutoMerge {
		amVal = styleTeal.Render("● on")
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
			cursor = styleSelected.Render("❯ ")
		}
		lines = append(lines, cursor+styleKey.Render(r.label)+"  "+r.value)
	}

	lines = append(lines, "")

	saveCursor := "  "
	if m.settingsFocus == sfSave {
		saveCursor = styleSelected.Render("❯ ")
	}
	cancelCursor := "  "
	if m.settingsFocus == sfCancel {
		cancelCursor = styleSelected.Render("❯ ")
	}
	lines = append(lines, saveCursor+styleTeal.Render("✓ Save"))
	lines = append(lines, cancelCursor+styleMuted.Render("✗ Cancel"))

	hint := "\n" + styleMuted.Render("↑↓=move  ←→=cycle runner  space=toggle  enter=edit  esc=cancel")

	return styleSettingsBox.Render(title + "\n\n" + strings.Join(lines, "\n") + hint)
}

// ── Help overlay ──────────────────────────────────────────────────────────────

func (m model) renderHelp() string {
	helpStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(mauve).
		Padding(1, 4)

	title := styleStep.Render("keyboard shortcuts")

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
			styleMuted.Render(fmt.Sprintf("%-10s", r.key))+"  "+styleVal.Render(r.desc))
	}

	return helpStyle.Render(title + "\n\n" + strings.Join(lines, "\n"))
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 0 {
		return ""
	}
	if n == 1 {
		return "…"
	}
	return string(runes[:n-1]) + "…"
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	m := initialModel()

	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	final, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	fm := final.(model)
	if fm.view != viewDone || len(fm.finalCmd) == 0 {
		os.Exit(0)
	}

	r := runners[fm.runnerIdx]
	rName, _, found := strings.Cut(r.label, " —")
	if !found {
		rName = r.label
	}

	runnerCmd := append(fm.finalCmd, "--no-interactive")

	if err := RunMonitor(fm.prNum, fm.repo, strings.TrimSpace(rName), fm.modelOverride, fm.prTitle, fm.path, fm.autoMerge, runnerCmd); err != nil {
		fmt.Fprintln(os.Stderr, "monitor error:", err)
		os.Exit(1)
	}
}
