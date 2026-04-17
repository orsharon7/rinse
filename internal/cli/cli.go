// Package cli implements one-shot CLI subcommands for RINSE.
//
// Subcommands allow RINSE to be invoked by agents and CI pipelines without a TTY.
// The interactive TUI (internal/tui) is untouched; this package is purely additive.
//
// Entry point: call TryDispatch() from main() before tui.Run().
// It returns true when it has handled the request and main() should exit.
//
// Usage:
//
//	rinse status [<pr>] [--repo <owner/repo>] [--json]
//	rinse start  <pr>  [options]              [--json]
//	rinse help
//
// RINSE targets Linux and macOS only (matching the cross-build Makefile targets).

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ── Runner registry ───────────────────────────────────────────────────────────
//
// Mirrors the runners slice in internal/tui/app.go.
// When new runners are added there, update this list too.

type runnerDef struct {
	name         string
	script       string
	defaultModel string
}

var knownRunners = []runnerDef{
	{"opencode", "pr-review-opencode.sh", "github-copilot/claude-sonnet-4.6"},
	{"claude", "pr-review-claude-v2.sh", "claude-sonnet-4-6"},
}

// ── JSON output types ─────────────────────────────────────────────────────────

// StatusResult is the JSON envelope for `rinse status --json`.
type StatusResult struct {
	OK     bool   `json:"ok"`
	PR     string `json:"pr"`
	Repo   string `json:"repo"`
	Status string `json:"status"`         // approved / pending / new_review / no_reviews / merged / closed / error
	Error  string `json:"error,omitempty"`
}

// StartResult is the JSON envelope for `rinse start --json`.
type StartResult struct {
	OK       bool   `json:"ok"`
	PR       string `json:"pr"`
	Repo     string `json:"repo"`
	Runner   string `json:"runner"`
	Model    string `json:"model"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

// ── TryDispatch ───────────────────────────────────────────────────────────────

// TryDispatch inspects os.Args[1] for a known CLI subcommand.
// It returns true when it has fully handled the request.
// main() should return immediately when TryDispatch returns true.
func TryDispatch() bool {
	if len(os.Args) < 2 {
		return false
	}
	switch os.Args[1] {
	case "status":
		runStatusCmd(os.Args[2:])
		return true
	case "start":
		runStartCmd(os.Args[2:])
		return true
	case "help", "--help", "-h":
		PrintHelp()
		return true
	}
	return false
}

// ── status ────────────────────────────────────────────────────────────────────

func runStatusCmd(args []string) {
	var (
		prNum  string
		repo   string
		asJSON bool
	)

	// Pre-scan for --json so that any validation error below is JSON-formatted
	// regardless of flag order.
	for _, a := range args {
		if a == "--json" {
			asJSON = true
			break
		}
	}

	rest := args
	// Optional positional PR number.
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		prNum = rest[0]
		rest = rest[1:]
	}

	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--repo":
			i++
			if i >= len(rest) || strings.HasPrefix(rest[i], "-") {
				fatalf(asJSON, "--repo requires a value (e.g. --repo owner/repo)")
			}
			repo = rest[i]
		case "--pr":
			i++
			if i >= len(rest) || strings.HasPrefix(rest[i], "-") {
				fatalf(asJSON, "--pr requires a value (e.g. --pr 42)")
			}
			prNum = rest[i]
		case "--json":
			asJSON = true
		default:
			fatalf(asJSON, "unknown flag: %s", rest[i])
		}
	}

	if repo == "" {
		repo = detectRepo()
		if repo == "" {
			fatalf(asJSON, "no repository detected — run from inside a git checkout or pass --repo")
		}
	}
	if prNum == "" {
		prNum = detectCurrentPR(repo)
		if prNum == "" {
			fatalf(asJSON, "could not detect current PR — pass a PR number as the first argument")
		}
	}
	if n, err := strconv.Atoi(prNum); err != nil || n <= 0 {
		fatalf(asJSON, "PR number must be a positive integer, got: %s", prNum)
	}

	status, err := queryPRStatus(repo, prNum)
	if err != nil {
		if asJSON {
			emitJSON(StatusResult{OK: false, PR: prNum, Repo: repo, Status: "error", Error: err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		os.Exit(1)
	}

	if asJSON {
		emitJSON(StatusResult{OK: true, PR: prNum, Repo: repo, Status: status})
	} else {
		fmt.Printf("pr:     #%s\n", prNum)
		fmt.Printf("repo:   %s\n", repo)
		fmt.Printf("status: %s\n", status)
	}
}

// queryPRStatus returns a normalised review status string for a PR using gh.
// Possible values: approved / pending / new_review / merged / closed / no_reviews / error
func queryPRStatus(repo, prNum string) (string, error) {
	type ghPR struct {
		State          string `json:"state"`
		Merged         bool   `json:"merged"`
		ReviewDecision string `json:"reviewDecision"`
		Reviews        []struct {
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			State string `json:"state"`
		} `json:"reviews"`
	}

	out, err := exec.Command("gh", "pr", "view", prNum,
		"--repo", repo,
		"--json", "state,merged,reviewDecision,reviews",
	).Output()
	if err != nil {
		return "error", fmt.Errorf("gh pr view: %w", err)
	}

	var p ghPR
	if err := json.Unmarshal(out, &p); err != nil {
		return "error", fmt.Errorf("parse gh output: %w", err)
	}

	if p.Merged {
		return "merged", nil
	}
	if strings.EqualFold(p.State, "closed") {
		return "closed", nil
	}

	switch strings.ToUpper(p.ReviewDecision) {
	case "APPROVED":
		return "approved", nil
	case "REVIEW_REQUIRED":
		for _, r := range p.Reviews {
			if strings.Contains(strings.ToLower(r.Author.Login), "copilot") &&
				strings.EqualFold(r.State, "PENDING") {
				return "pending", nil
			}
		}
		if len(p.Reviews) == 0 {
			return "no_reviews", nil
		}
		return "new_review", nil
	}

	if len(p.Reviews) == 0 {
		return "no_reviews", nil
	}
	return "new_review", nil
}

// detectCurrentPR finds an open PR for the current git branch.
func detectCurrentPR(repo string) string {
	branch, err := exec.Command("git", "branch", "--show-current").Output()
	if err != nil || strings.TrimSpace(string(branch)) == "" {
		return ""
	}
	out, err := exec.Command("gh", "pr", "list",
		"--repo", repo,
		"--head", strings.TrimSpace(string(branch)),
		"--json", "number",
		"--limit", "1",
	).Output()
	if err != nil {
		return ""
	}
	var prs []struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal(out, &prs); err != nil || len(prs) == 0 {
		return ""
	}
	return strconv.Itoa(prs[0].Number)
}

// ── start ─────────────────────────────────────────────────────────────────────

func runStartCmd(args []string) {
	var (
		prNum       string
		repo        string
		cwd         string
		model       string
		runnerName  string
		doReflect   bool
		reflectMain string
		autoMerge   bool
		asJSON      bool
	)

	// Pre-scan for --json so that any validation error below is JSON-formatted
	// regardless of flag order.
	for _, a := range args {
		if a == "--json" {
			asJSON = true
			break
		}
	}

	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fatalf(asJSON, "usage: rinse start <pr_number> [options]\nRun 'rinse help' for full usage.")
	}
	prNum = args[0]
	if n, err := strconv.Atoi(prNum); err != nil || n <= 0 {
		fatalf(asJSON, "PR number must be a positive integer, got: %s", prNum)
	}

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--repo":
			i++
			if i >= len(args) || strings.HasPrefix(args[i], "-") {
				fatalf(asJSON, "--repo requires a value (e.g. --repo owner/repo)")
			}
			repo = args[i]
		case "--cwd":
			i++
			if i >= len(args) || strings.HasPrefix(args[i], "-") {
				fatalf(asJSON, "--cwd requires a value (e.g. --cwd /path/to/repo)")
			}
			cwd = args[i]
		case "--model":
			i++
			if i >= len(args) || strings.HasPrefix(args[i], "-") {
				fatalf(asJSON, "--model requires a value (e.g. --model claude-sonnet-4-6)")
			}
			model = args[i]
		case "--runner":
			i++
			if i >= len(args) || strings.HasPrefix(args[i], "-") {
				fatalf(asJSON, "--runner requires a value (e.g. --runner opencode)")
			}
			runnerName = args[i]
		case "--reflect":
			doReflect = true
		case "--reflect-main-branch":
			i++
			if i >= len(args) || strings.HasPrefix(args[i], "-") {
				fatalf(asJSON, "--reflect-main-branch requires a value (e.g. --reflect-main-branch main)")
			}
			reflectMain = args[i]
		case "--auto-merge":
			autoMerge = true
		case "--json":
			asJSON = true
		default:
			fatalf(asJSON, "unknown flag: %s", args[i])
		}
	}

	// Defaults.
	if repo == "" {
		repo = detectRepo()
		if repo == "" {
			fatalf(asJSON, "no repository detected — run from inside a git checkout or pass --repo")
		}
	}
	if cwd == "" {
		cwd = detectCWD()
	}

	// Resolve runner (default: opencode).
	runnerIdx := 0
	if runnerName != "" {
		found := false
		for i, r := range knownRunners {
			if strings.EqualFold(r.name, runnerName) {
				runnerIdx = i
				found = true
				break
			}
		}
		if !found {
			valid := make([]string, len(knownRunners))
			for i, r := range knownRunners {
				valid[i] = r.name
			}
			fatalf(asJSON, "unknown runner %q — valid: %s", runnerName, strings.Join(valid, ", "))
		}
	}
	r := knownRunners[runnerIdx]

	if model == "" {
		model = r.defaultModel
	}
	if reflectMain == "" {
		reflectMain = "main"
	}

	// Locate runner script.
	script, err := resolveScript(r.script)
	if err != nil {
		fatalf(asJSON, "%v", err)
	}

	// Build argument list mirroring wizard.go buildCmd().
	cmdArgs := []string{
		script, prNum,
		"--repo", repo,
		"--cwd", cwd,
		"--model", model,
		"--no-interactive",
	}
	if doReflect {
		cmdArgs = append(cmdArgs, "--reflect", "--reflect-main-branch", reflectMain)
	}
	if autoMerge {
		cmdArgs = append(cmdArgs, "--auto-merge")
	}

	if asJSON {
		// Run with streaming output redirected to stderr so stdout remains
		// exclusively for the final JSON envelope (machine-readable).
		exitCode := execInherited(cmdArgs, os.Stderr)
		ok := exitCode == 0
		errMsg := ""
		if !ok {
			errMsg = fmt.Sprintf("runner exited with code %d", exitCode)
		}
		emitJSON(StartResult{
			OK:       ok,
			PR:       prNum,
			Repo:     repo,
			Runner:   r.name,
			Model:    model,
			ExitCode: exitCode,
			Error:    errMsg,
		})
		os.Exit(exitCode)
	}

	// Plain mode: replace the process so the runner owns the terminal.
	execReplace(cmdArgs)
}

// ── Script resolution ─────────────────────────────────────────────────────────

// resolveScript locates a runner script relative to the binary.
// Resolution order mirrors wizard.go buildCmd() so both paths stay in sync:
//
//  1. $RINSE_SCRIPT_DIR
//  2. $PR_REVIEW_SCRIPT_DIR  (legacy alias)
//  3. <binDir>/scripts/, <binDir>/../scripts/, <binDir>/  (install layouts)
func resolveScript(scriptName string) (string, error) {
	scriptDir := os.Getenv("RINSE_SCRIPT_DIR")
	if scriptDir == "" {
		scriptDir = os.Getenv("PR_REVIEW_SCRIPT_DIR") // legacy
	}
	if scriptDir == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("could not determine script directory: %w", err)
		}
		binDir := filepath.Dir(exe)
		candidates := []string{
			filepath.Join(binDir, "scripts"),
			filepath.Join(binDir, "..", "scripts"),
			filepath.Join(binDir, "pr-review"),
			filepath.Join(binDir, "..", "pr-review"),
			binDir,
		}
		for _, c := range candidates {
			if _, err := os.Stat(filepath.Join(c, scriptName)); err == nil {
				scriptDir = c
				break
			}
		}
		if scriptDir == "" {
			scriptDir = binDir
		}
	}

	script := filepath.Join(scriptDir, scriptName)
	if _, err := os.Stat(script); err != nil {
		return "", fmt.Errorf("runner script not found: %s\nSet RINSE_SCRIPT_DIR to override the search path.", script)
	}
	return script, nil
}

// ── Process execution ─────────────────────────────────────────────────────────

// execInherited runs args with the given stdout writer and inherited stdin/stderr,
// returning the exit code. In --json mode, pass os.Stderr as stdout to keep
// stdout reserved exclusively for the final JSON envelope.
func execInherited(args []string, stdout io.Writer) int {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return 1
	}
	return 0
}

// execReplace replaces the current process with args (Unix only; see exec_unix.go).
// Falls back to execInherited+exit on error.

// ── Helpers ───────────────────────────────────────────────────────────────────

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

func emitJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// fatalf prints an error and exits. In JSON mode it emits a JSON error object.
func fatalf(asJSON bool, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	if asJSON {
		type errOut struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		emitJSON(errOut{OK: false, Error: msg})
	} else {
		fmt.Fprintf(os.Stderr, "error: %s\n", msg)
	}
	os.Exit(1)
}

// ── Help ──────────────────────────────────────────────────────────────────────

// PrintHelp prints the CLI usage text to stdout.
func PrintHelp() {
	fmt.Print(`rinse — Autonomous Copilot PR review lifecycle manager

USAGE
  rinse [subcommand] [flags]

  Without a subcommand, rinse launches the interactive TUI.

SUBCOMMANDS

  status [<pr>] [--repo <owner/repo>] [--json]
      Print the current Copilot review status of a PR.
      When <pr> is omitted, auto-detects from the current git branch.

      Output statuses:
        approved   — Copilot approved the PR
        pending    — Copilot review is in progress
        new_review — new review with comments ready to fix
        no_reviews — no Copilot reviews yet
        merged     — PR already merged
        closed     — PR closed without merge
        error      — could not determine status

  start <pr> [options] [--json]
      Start the PR review fix loop non-interactively (no TUI, no TTY required).
      Suitable for agent pipelines and CI.

      --repo <owner/repo>           Override repository detection
      --cwd  <path>                 Local checkout path (default: current directory)
      --model <model>               AI model string (overrides runner default)
      --runner opencode|claude      Runner to use (default: opencode)
      --reflect                     Enable reflection agent to update AGENTS.md
      --reflect-main-branch <br>    Target branch for reflection commits (default: main)
      --auto-merge                  Auto-merge when Copilot approves
      --json                        Emit a JSON result after the runner exits.
                                    Streaming output goes to stderr throughout.

  help | --help | -h
      Show this help.

  --version | -v
      Print version string.

EXAMPLES

  # Check status of the PR for the current branch (machine-readable)
  rinse status --json

  # Check status of PR #42
  rinse status 42 --repo owner/repo

  # Start the review loop for PR #42 (no TTY needed)
  rinse start 42 --repo owner/repo --cwd /path/to/repo

  # Agent pipeline: stream output, capture JSON result
  rinse start 42 --repo owner/repo --reflect --json

`)
}
