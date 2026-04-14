package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// version is set at build time via -ldflags.
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

// ── Shared helpers ────────────────────────────────────────────────────────────

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

// wrapLine splits s into lines of at most w visible runes, breaking at spaces.
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
		cut := w
		for cut > w-12 && cut > 0 && runes[cut-1] != ' ' {
			cut--
		}
		if cut <= 0 {
			cut = w
		}
		lines = append(lines, strings.TrimRight(string(runes[:cut]), " "))
		runes = runes[cut:]
		for len(runes) > 0 && runes[0] == ' ' {
			runes = runes[1:]
		}
	}
	return lines
}

// renderKeyHint renders a "key action" pair in the standard hint style.
func renderKeyHint(key, desc string) string {
	return styleHintKey.Render(key) + " " + styleHintDesc.Render(desc)
}

// renderSeparator renders a horizontal line of width w using ─ characters.
func renderSeparator(w int) string {
	if w <= 0 {
		w = 1
	}
	return styleSeparator.Render(strings.Repeat("─", w))
}

// renderLogo renders the ◇ rinse branding header.
func renderLogo(repo, branch string) string {
	logo := styleLogoIcon.Render(IconDiamond) + " " + styleLogo.Render("rinse")
	ver := styleMuted.Render(" v" + version)
	right := ""
	if repo != "" {
		right = styleVal.Render(repo)
		if branch != "" {
			right += styleMuted.Render(" on ") + styleTeal.Render(branch)
		}
	}
	if right != "" {
		return "  " + logo + ver + styleMuted.Render("  "+IconSep+"  ") + right
	}
	return "  " + logo + ver
}

// renderMonitorLogo renders the slash-separated monitor header brand.
func renderMonitorLogo(prNum, repo, runnerName string) string {
	parts := []string{
		styleLogoIcon.Render(IconDiamond) + " " + styleLogo.Render("rinse"),
	}
	if prNum != "" {
		parts = append(parts, styleHeaderVal.Render("#"+prNum))
	}
	if repo != "" {
		parts = append(parts, styleHeaderVal.Render(repo))
	}
	if runnerName != "" {
		parts = append(parts, styleHeaderLabel.Render(runnerName))
	}
	sep := " " + styleLogoSlash.Render(IconSlash) + " "
	return strings.Join(parts, sep)
}

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
