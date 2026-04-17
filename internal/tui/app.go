package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/orsharon7/rinse/internal/config"
)

// version is set by Run() from the value injected at build time via -ldflags.
var version = "dev"

// ── PR data ───────────────────────────────────────────────────────────────────

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

// ── Runner definitions ────────────────────────────────────────────────────────

type runner struct {
	name         string
	desc         string
	script       string
	defaultModel string
}

var runners = []runner{
	{"opencode", "GitHub Copilot · no API key", "pr-review-opencode.sh", "github-copilot/claude-sonnet-4.6"},
	{"claude", "Claude Code · Anthropic key", "pr-review-claude-v2.sh", "claude-sonnet-4-6"},
}

// ── View mode ─────────────────────────────────────────────────────────────────

type viewMode int

const (
	viewSplash viewMode = iota
	viewPRPicker
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

// shortRunnerName returns the runner's short name.
func shortRunnerName(idx int) string {
	if idx < 0 || idx >= len(runners) {
		return "?"
	}
	return runners[idx].name
}

// fmtPRNumber formats a PR number like "#14 " left-padded.
func fmtPRNumber(n int) string {
	return fmt.Sprintf("#%-4d", n)
}

// Run is the entry point for the RINSE TUI. ver is the version string injected
// at build time via -ldflags.
func Run(ver string) error {
	version = ver

	m := initialModel()

	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	final, err := p.Run()
	if err != nil {
		return err
	}

	fm := final.(model)
	if fm.view != viewDone || len(fm.finalCmd) == 0 {
		return nil
	}

	rName := shortRunnerName(fm.runnerIdx)
	runnerCmd := append(fm.finalCmd, "--no-interactive")

	return RunMonitor(fm.prNum, fm.repo, strings.TrimSpace(rName), fm.modelOverride, fm.prTitle, fm.path, fm.autoMerge, runnerCmd)
}

// initialModel builds a fresh wizard model with settings loaded from disk.
func initialModel() model {
	repo := detectRepo()

	cfg := config.LoadConfig(len(runners))
	var rc config.RepoConfig
	hasRepoConfig := false
	if repo != "" {
		if loaded, ok := config.LoadRepoConfig(repo); ok {
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

	return newModel(repo, path, rc, cfg, hasRepoConfig)
}

// saveConfig persists wizard settings to disk.
func saveConfig(cfg config.Config) {
	config.SaveConfig(cfg)
}
