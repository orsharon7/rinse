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
			Border(lipgloss.DoubleBorder()).
			BorderForeground(mauve).
			Padding(1, 4).
			Align(lipgloss.Center)

	styleSummaryBox = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(surface).
			Padding(0, 2)

	styleKey   = lipgloss.NewStyle().Foreground(overlay).Width(18)
	styleVal   = lipgloss.NewStyle().Foreground(lavender).Bold(true)
	styleMuted = lipgloss.NewStyle().Foreground(overlay)
	styleStep  = lipgloss.NewStyle().Foreground(mauve).Bold(true)
	styleErr   = lipgloss.NewStyle().Foreground(red)
	styleTeal  = lipgloss.NewStyle().Foreground(teal).Bold(true)

	styleSelected   = lipgloss.NewStyle().Foreground(mauve).Bold(true)
	styleUnselected = lipgloss.NewStyle().Foreground(subtext)

	styleLaunchBox = lipgloss.NewStyle().
			Bold(true).
			Border(lipgloss.DoubleBorder()).
			BorderForeground(teal).
			Padding(1, 4).
			Align(lipgloss.Center)

	styleConfirmBox = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(mauve).
			Padding(1, 2)
)

// ── Step definitions ──────────────────────────────────────────────────────────

type step int

const (
	stepRepo step = iota
	stepPR
	stepPath
	stepRunner
	stepModel
	stepReflect
	stepConfirm
	stepDone
)

var stepLabels = map[step]string{
	stepRepo:    "repository",
	stepPR:      "pull request",
	stepPath:    "local clone path",
	stepRunner:  "runner",
	stepModel:   "model",
	stepReflect: "reflection",
	stepConfirm: "review & launch",
}

// ── PR list (fetched async) ───────────────────────────────────────────────────

type pr struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	HeadRefName string `json:"headRefName"`
}

type prListMsg []pr
type prListErrMsg struct{ err error }
type defaultBranchMsg string

func fetchPRs(repo string) tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command("gh", "pr", "list",
			"--repo", repo,
			"--json", "number,title,headRefName",
			"--limit", "15",
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
	{"opencode  — GitHub Copilot · no API key needed  ✦", "pr-review-opencode.sh", "github-copilot/claude-sonnet-4.6"},
	{"claude v2 — Claude Code · requires Anthropic key", "pr-review-claude-v2.sh", "claude-sonnet-4-6"},
	{"claude v1 — legacy runner", "pr-review-claude.sh", ""},
}

// ── Model ─────────────────────────────────────────────────────────────────────

type model struct {
	width  int
	height int

	step step

	// fields
	repo          string
	prNum         string
	prTitle       string // title of the selected PR (empty if typed manually)
	path          string
	pathDefault   string // pre-loaded from config, used as path input default
	runnerIdx     int
	modelOverride string
	reflect       bool
	reflectBranch string

	// UI state
	showHelp bool

	// text inputs
	input    textinput.Model
	inputFor step // which field the input is currently editing

	// PR picker
	prs       []pr
	prCursor  int
	prLoading bool
	prLoadErr string
	prManual  bool // user chose "type manually"

	// runner picker cursor
	runnerCursor int

	// reflect picker cursor (0=yes 1=no)
	reflectCursor int

	// confirm picker cursor
	confirmCursor int

	// async data
	defaultBranch string

	// error message to show inline
	errMsg string

	// final command to exec (set at stepDone)
	finalCmd []string
}

func initialModel() model {
	cfg := LoadConfig()

	ti := textinput.New()
	ti.Cursor.Style = lipgloss.NewStyle().Foreground(mauve)
	ti.PromptStyle = lipgloss.NewStyle().Foreground(mauve)
	ti.TextStyle = lipgloss.NewStyle().Foreground(text)
	ti.Prompt = "  ❯ "
	ti.CharLimit = 256
	ti.Focus()

	// Auto-detect repo from git/gh; fall back to saved config.
	repo := detectRepo()
	if repo == "" {
		repo = cfg.LastRepo
	}
	ti.SetValue(repo)

	reflectCursor := 1 // default: "No"
	if cfg.LastReflect {
		reflectCursor = 0
	}

	return model{
		step:          stepRepo,
		repo:          "",
		path:          "",
		pathDefault:   cfg.LastPath,
		runnerIdx:     0,
		runnerCursor:  cfg.LastRunner,
		modelOverride: cfg.LastModel,
		reflect:       false,
		reflectCursor: reflectCursor,
		reflectBranch: cfg.LastBranch,
		defaultBranch: "main",
		input:         ti,
		inputFor:      stepRepo,
		prLoading:     false,
	}
}

// ── Init ──────────────────────────────────────────────────────────────────────

func (m model) Init() tea.Cmd {
	return textinput.Blink
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
		return m, nil

	case prListErrMsg:
		m.prLoading = false
		m.prLoadErr = msg.err.Error()
		m.prManual = true
		m.input.SetValue(m.prNum)
		m.input.Focus()
		return m, textinput.Blink

	case defaultBranchMsg:
		m.defaultBranch = string(msg)
		if m.reflectBranch == "" {
			m.reflectBranch = m.defaultBranch
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	// Forward to textinput when it's active.
	if m.isInputActive() {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m model) isInputActive() bool {
	switch m.step {
	case stepRepo, stepPath, stepModel, stepReflect:
		return true
	case stepPR:
		return m.prManual || len(m.prs) == 0
	}
	return false
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Global: quit.
	if key == "ctrl+c" {
		return m, tea.Quit
	}

	// Global: toggle help overlay.
	if key == "?" {
		m.showHelp = !m.showHelp
		return m, nil
	}

	// Any key dismisses the help overlay.
	if m.showHelp {
		m.showHelp = false
		return m, nil
	}

	switch m.step {

	// ── Repo input ────────────────────────────────────────────────────────────
	case stepRepo:
		if key == "enter" {
			val := strings.TrimSpace(m.input.Value())
			if val == "" {
				m.errMsg = "repository is required"
				return m, nil
			}
			m.repo = val
			m.errMsg = ""
			m.step = stepPR
			m.prLoading = true
			m.prs = nil
			m.prManual = false
			return m, tea.Batch(fetchPRs(val), fetchDefaultBranch(val))
		}
		if key == "esc" {
			return m, tea.Quit
		}

	// ── PR picker ─────────────────────────────────────────────────────────────
	case stepPR:
		if m.prLoading {
			if key == "esc" {
				m.step = stepRepo
				m.input.SetValue(m.repo)
				m.input.Focus()
				return m, textinput.Blink
			}
			return m, nil
		}

		if m.prManual || len(m.prs) == 0 {
			// text input mode
			if key == "enter" {
				val := strings.TrimSpace(m.input.Value())
				if val == "" {
					m.errMsg = "PR number is required"
					return m, nil
				}
				m.prNum = val
				m.prTitle = "" // no title when entered manually
				m.errMsg = ""
				m.step = stepPath
				pathDefault := m.pathDefault
				if pathDefault == "" {
					pathDefault = detectCWD()
				}
				m.input.SetValue(pathDefault)
				m.input.Focus()
				return m, textinput.Blink
			}
			if key == "esc" {
				m.prManual = false
				m.step = stepRepo
				m.input.SetValue(m.repo)
				m.input.Focus()
				return m, textinput.Blink
			}
		} else {
			// list picker mode — rows: PRs + "type manually"
			total := len(m.prs) + 1
			switch key {
			case "up", "k":
				if m.prCursor > 0 {
					m.prCursor--
				}
			case "down", "j":
				if m.prCursor < total-1 {
					m.prCursor++
				}
			case "enter":
				if m.prCursor == len(m.prs) {
					// "type manually"
					m.prManual = true
					m.input.SetValue(m.prNum)
					m.input.Focus()
					return m, textinput.Blink
				}
				m.prNum = fmt.Sprintf("%d", m.prs[m.prCursor].Number)
				m.prTitle = m.prs[m.prCursor].Title
				m.step = stepPath
				pathDefault := m.pathDefault
				if pathDefault == "" {
					pathDefault = detectCWD()
				}
				m.input.SetValue(pathDefault)
				m.input.Focus()
				return m, textinput.Blink
			case "esc":
				m.step = stepRepo
				m.input.SetValue(m.repo)
				m.input.Focus()
				return m, textinput.Blink
			}
		}

	// ── Path input ────────────────────────────────────────────────────────────
	case stepPath:
		if key == "enter" {
			val := strings.TrimSpace(m.input.Value())
			if val == "" {
				val = detectCWD()
			}
			m.path = val
			m.errMsg = ""
			m.step = stepRunner
			return m, nil
		}
		if key == "esc" {
			m.step = stepPR
			m.prManual = false
			return m, nil
		}

	// ── Runner picker ─────────────────────────────────────────────────────────
	case stepRunner:
		switch key {
		case "up", "k":
			if m.runnerCursor > 0 {
				m.runnerCursor--
			}
		case "down", "j":
			if m.runnerCursor < len(runners)-1 {
				m.runnerCursor++
			}
		case "enter":
			m.runnerIdx = m.runnerCursor
			m.step = stepModel
			m.input.SetValue(m.modelOverride) // pre-fill with last-used model
			m.input.Placeholder = runners[m.runnerIdx].defaultModel
			m.input.Focus()
			return m, textinput.Blink
		case "esc":
			m.step = stepPath
			m.input.SetValue(m.path)
			m.input.Focus()
			return m, textinput.Blink
		}

	// ── Model input ───────────────────────────────────────────────────────────
	case stepModel:
		if key == "enter" {
			m.modelOverride = strings.TrimSpace(m.input.Value())
			m.step = stepReflect
			if m.reflectBranch == "" {
				m.reflectBranch = m.defaultBranch
			}
			m.input.SetValue("")
			m.input.Placeholder = m.reflectBranch
			m.input.Focus()
			// skip reflect for claude v1
			if m.runnerIdx == 2 {
				m.reflect = false
				m.step = stepConfirm
			}
			return m, textinput.Blink
		}
		if key == "esc" {
			m.step = stepRunner
			return m, nil
		}

	// ── Reflect picker ────────────────────────────────────────────────────────
	case stepReflect:
		if m.inputFor == stepReflect {
			// branch text input active
			if key == "enter" {
				val := strings.TrimSpace(m.input.Value())
				if val == "" {
					val = m.defaultBranch
				}
				m.reflectBranch = val
				m.reflect = true
				m.inputFor = stepRunner // reset
				m.step = stepConfirm
				return m, nil
			}
			if key == "esc" {
				m.inputFor = stepRunner
				m.step = stepReflect
				return m, nil
			}
		} else {
			switch key {
			case "up", "k":
				if m.reflectCursor > 0 {
					m.reflectCursor--
				}
			case "down", "j":
				if m.reflectCursor < 1 {
					m.reflectCursor++
				}
			case "enter":
				if m.reflectCursor == 0 {
					// yes — ask for branch
					m.inputFor = stepReflect
					m.input.SetValue(m.reflectBranch)
					m.input.Placeholder = m.defaultBranch
					m.input.Focus()
					return m, textinput.Blink
				}
				// no
				m.reflect = false
				m.step = stepConfirm
				return m, nil
			case "esc":
				m.step = stepModel
				m.input.SetValue(m.modelOverride)
				m.input.Focus()
				return m, textinput.Blink
			}
		}

	// ── Confirm ───────────────────────────────────────────────────────────────
	case stepConfirm:
		switch key {
		case "up", "k":
			if m.confirmCursor > 0 {
				m.confirmCursor--
			}
		case "down", "j":
			if m.confirmCursor < 2 {
				m.confirmCursor++
			}
		case "enter":
			switch m.confirmCursor {
			case 0: // launch
				cmd, err := m.buildCmd()
				if err != nil {
					m.errMsg = err.Error()
					return m, nil
				}
				m.finalCmd = cmd
				m.step = stepDone
				return m, tea.Quit
			case 1: // edit (go back)
				m.step = stepModel
				m.input.SetValue(m.modelOverride)
				m.input.Focus()
				return m, textinput.Blink
			case 2: // abort
				return m, tea.Quit
			}
		case "esc":
			m.step = stepModel
			m.input.SetValue(m.modelOverride)
			m.input.Focus()
			return m, textinput.Blink
		}
	}

	// Forward keystroke to textinput when active.
	if m.isInputActive() {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m model) buildCmd() ([]string, error) {
	scriptDir := os.Getenv("PR_REVIEW_SCRIPT_DIR")
	if scriptDir == "" {
		// Fall back to the directory containing the running binary.
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("could not determine script directory: %w", err)
		}
		scriptDir = filepath.Dir(exe)
	}

	r := runners[m.runnerIdx]
	script := filepath.Join(scriptDir, r.script)
	if _, err := os.Stat(script); err != nil {
		return nil, fmt.Errorf("runner script not found: %s", script)
	}

	cmd := []string{script, m.prNum,
		"--repo", m.repo,
		"--cwd", m.path,
	}
	if m.modelOverride != "" {
		cmd = append(cmd, "--model", m.modelOverride)
	}
	if m.reflect {
		cmd = append(cmd, "--reflect", "--reflect-main-branch", m.reflectBranch)
	}

	// Persist settings for next run.
	SaveConfig(Config{
		LastRepo:    m.repo,
		LastPath:    m.path,
		LastRunner:  m.runnerIdx,
		LastModel:   m.modelOverride,
		LastReflect: m.reflect,
		LastBranch:  m.reflectBranch,
	})

	return cmd, nil
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m model) View() string {
	w := m.width
	if w < 40 {
		w = 80
	}

	// Help overlay replaces the entire view.
	if m.showHelp {
		h := m.height
		if h <= 0 {
			h = 24
		}
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, m.renderHelp())
	}

	var b strings.Builder

	// Banner
	bannerInner := w - 4
	if bannerInner < 30 {
		bannerInner = 30
	}
	banner := styleBanner.Width(bannerInner).Render(
		"pr-review\n\n" +
			styleMuted.Render("Copilot PR Review Automation"),
	)
	b.WriteString(banner + "\n\n")

	// Summary of answered fields
	b.WriteString(m.renderSummary(w) + "\n")

	// Step header: progress bar + label
	label := stepLabels[m.step]
	if m.step == stepDone {
		label = "launching"
	}
	total := 7
	current := int(m.step) + 1
	if current > total {
		current = total
	}
	b.WriteString("  " + renderProgressBar(current, total, w) + "\n")
	b.WriteString("  " + styleMuted.Render(label) + "\n\n")

	// Step body
	b.WriteString(m.renderStep(w))

	// Error
	if m.errMsg != "" {
		b.WriteString("\n" + styleErr.Render("  ✗ "+m.errMsg))
	}

	// Help hint at the bottom
	b.WriteString("\n" + styleMuted.Render("  ? for keyboard shortcuts"))

	return b.String()
}

// renderProgressBar renders a filled/empty block progress bar.
func renderProgressBar(current, total, w int) string {
	barW := w - 12
	if barW > 40 {
		barW = 40
	}
	if barW < 8 {
		barW = 8
	}

	filled := 0
	if total > 0 {
		filled = barW * current / total
	}
	if filled > barW {
		filled = barW
	}

	bar := styleStep.Render(strings.Repeat("█", filled)) +
		styleMuted.Render(strings.Repeat("░", barW-filled))
	fraction := styleMuted.Render(fmt.Sprintf("  %d/%d", current, total))
	return bar + fraction
}

// renderHelp renders the keyboard shortcut overlay for the wizard.
func (m model) renderHelp() string {
	helpStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(mauve).
		Padding(1, 4)

	title := styleStep.Render("keyboard shortcuts")

	type krow struct{ key, desc string }
	rows := []krow{
		{"enter", "confirm / select"},
		{"esc", "go back"},
		{"↑ / k", "navigate up"},
		{"↓ / j", "navigate down"},
		{"?", "toggle this help"},
		{"^C", "quit"},
	}

	var lines []string
	for _, r := range rows {
		lines = append(lines,
			styleMuted.Render(fmt.Sprintf("%-10s", r.key))+"  "+styleVal.Render(r.desc))
	}

	return helpStyle.Render(title + "\n\n" + strings.Join(lines, "\n"))
}

func (m model) renderSummary(w int) string {
	type row struct{ k, v string }
	var rows []row

	if m.repo != "" {
		rows = append(rows, row{"repository", m.repo})
	}
	if m.prNum != "" {
		val := "#" + m.prNum
		if m.prTitle != "" {
			val += "  " + truncate(m.prTitle, 48)
		}
		rows = append(rows, row{"PR", val})
	}
	if m.path != "" {
		rows = append(rows, row{"path", m.path})
	}
	if m.step > stepRunner {
		label := runners[m.runnerIdx].label
		runnerName := label
		if sep := strings.Index(label, " —"); sep >= 0 {
			runnerName = label[:sep]
		}
		rows = append(rows, row{"runner", runnerName})
	}
	if m.step > stepModel && m.modelOverride != "" {
		rows = append(rows, row{"model", m.modelOverride})
	}
	if m.step >= stepConfirm {
		ref := "off"
		if m.reflect {
			ref = "on → " + m.reflectBranch
		}
		rows = append(rows, row{"reflection", ref})
	}

	if len(rows) == 0 {
		return ""
	}

	var lines []string
	for _, r := range rows {
		lines = append(lines, styleKey.Render(r.k)+"  "+styleVal.Render(r.v))
	}
	return styleSummaryBox.Width(w-4).Render(strings.Join(lines, "\n")) + "\n"
}

func (m model) renderStep(w int) string {
	var b strings.Builder

	switch m.step {

	case stepRepo:
		b.WriteString(styleMuted.Render("  GitHub repo in owner/repo format") + "\n\n")
		b.WriteString(m.input.View())

	case stepPR:
		if m.prLoading {
			b.WriteString(styleMuted.Render("  Fetching open PRs for " + m.repo + "…"))
			return b.String()
		}
		if m.prManual || len(m.prs) == 0 {
			if len(m.prs) == 0 && !m.prManual {
				b.WriteString(styleMuted.Render("  No open PRs found — enter number manually") + "\n\n")
			} else {
				b.WriteString(styleMuted.Render("  Enter PR number") + "\n\n")
			}
			b.WriteString(m.input.View())
		} else {
			b.WriteString(styleMuted.Render("  ↑/↓ or j/k to navigate · enter to select · esc to go back") + "\n\n")
			for i, p := range m.prs {
				label := fmt.Sprintf("#%-4d  %-28s  %s",
					p.Number,
					truncate(p.HeadRefName, 28),
					truncate(p.Title, 48),
				)
				if i == m.prCursor {
					b.WriteString(styleSelected.Render("  ❯ " + label))
				} else {
					b.WriteString(styleUnselected.Render("    " + label))
				}
				b.WriteString("\n")
			}
			manualLabel := "  ✏  type a number manually"
			if m.prCursor == len(m.prs) {
				b.WriteString(styleSelected.Render("  ❯" + manualLabel[2:]))
			} else {
				b.WriteString(styleUnselected.Render(manualLabel))
			}
		}

	case stepPath:
		b.WriteString(styleMuted.Render("  Absolute path to your local checkout of "+m.repo) + "\n\n")
		b.WriteString(m.input.View())

	case stepRunner:
		b.WriteString(styleMuted.Render("  ↑/↓ to navigate · enter to select · esc to go back") + "\n\n")
		for i, r := range runners {
			if i == m.runnerCursor {
				b.WriteString(styleSelected.Render("  ❯ " + r.label))
			} else {
				b.WriteString(styleUnselected.Render("    " + r.label))
			}
			b.WriteString("\n")
		}

	case stepModel:
		def := runners[m.runnerIdx].defaultModel
		b.WriteString(styleMuted.Render("  Leave blank for default: ") + styleVal.Render(def) + "\n\n")
		b.WriteString(m.input.View())

	case stepReflect:
		if m.inputFor == stepReflect {
			b.WriteString(styleMuted.Render("  Branch to push rules to:") + "\n\n")
			b.WriteString(m.input.View())
		} else {
			b.WriteString(styleMuted.Render(
				"  Extract coding rules from Copilot comments → push to "+m.defaultBranch,
			) + "\n\n")
			opts := []string{"  Yes, enable reflection", "  No, skip"}
			for i, opt := range opts {
				if i == m.reflectCursor {
					b.WriteString(styleSelected.Render("  ❯" + opt[2:]))
				} else {
					b.WriteString(styleUnselected.Render(opt))
				}
				b.WriteString("\n")
			}
		}

	case stepConfirm:
		type row struct{ k, v string }
		def := runners[m.runnerIdx].defaultModel
		modelStr := m.modelOverride
		if modelStr == "" {
			modelStr = def + " (default)"
		}
		refStr := "off"
		if m.reflect {
			refStr = "on → " + m.reflectBranch
		}
		prStr := "#" + m.prNum
		if m.prTitle != "" {
			prStr += "  " + truncate(m.prTitle, 40)
		}
		rows := []row{
			{"PR", prStr},
			{"repository", m.repo},
			{"path", m.path},
			{"runner", func() string {
				label := runners[m.runnerIdx].label
				if sep := strings.Index(label, " —"); sep >= 0 {
					return label[:sep]
				}
				return label
			}()},
			{"model", modelStr},
			{"reflection", refStr},
		}
		var lines []string
		for _, r := range rows {
			lines = append(lines, styleKey.Render(r.k)+"  "+styleVal.Render(r.v))
		}
		box := styleConfirmBox.Width(w - 4).Render(
			styleTeal.Render("settings") + "\n\n" +
				strings.Join(lines, "\n"),
		)
		b.WriteString(box + "\n\n")

		opts := []string{"🚀  Launch", "✏   Edit  (go back)", "✗   Abort"}
		for i, opt := range opts {
			if i == m.confirmCursor {
				b.WriteString(styleSelected.Render("  ❯ " + opt))
			} else {
				b.WriteString(styleUnselected.Render("    " + opt))
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}

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
	if fm.step != stepDone || len(fm.finalCmd) == 0 {
		// user aborted
		os.Exit(0)
	}

	// Hand off to the cycle monitor TUI
	r := runners[fm.runnerIdx]
	rName, _, found := strings.Cut(r.label, " —")
	if !found {
		rName = r.label
	}

	if err := RunMonitor(fm.prNum, fm.repo, rName, fm.modelOverride, fm.prTitle, fm.finalCmd); err != nil {
		fmt.Fprintln(os.Stderr, "monitor error:", err)
		os.Exit(1)
	}
}
