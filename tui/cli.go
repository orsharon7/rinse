package main

// cli.go — one-shot CLI subcommands and --json output flag for RINSE.
//
// Usage (non-TUI, suitable for agents / CI without a TTY):
//
//   rinse status [<pr_number>] [--repo <owner/repo>] [--json]
//   rinse start  <pr_number>  [options]             [--json]
//
// Run `rinse help` or `rinse --help` for full flag documentation.
//
// When no subcommand is given, RINSE launches the interactive TUI as usual.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ── JSON output types ─────────────────────────────────────────────────────────

// StatusResult is the JSON output for `rinse status --json`.
type StatusResult struct {
	OK     bool   `json:"ok"`
	PR     string `json:"pr"`
	Repo   string `json:"repo"`
	Status string `json:"status"`           // approved / pending / new_review / no_reviews / merged / closed / error
	Error  string `json:"error,omitempty"`
}

// StartResult is the JSON output for `rinse start --json`.
type StartResult struct {
	OK       bool   `json:"ok"`
	PR       string `json:"pr"`
	Repo     string `json:"repo"`
	Runner   string `json:"runner"`
	Model    string `json:"model"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

// ── Dispatch ──────────────────────────────────────────────────────────────────

// tryDispatchCLI inspects os.Args[1] for a known subcommand.
// It returns true when it handled the request (and main() should not start the TUI).
func tryDispatchCLI() bool {
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
		printCLIHelp()
		return true
	}
	return false
}

// ── status subcommand ─────────────────────────────────────────────────────────

func runStatusCmd(args []string) {
	var (
		prNum  string
		repo   string
		asJSON bool
	)

	// Pre-scan for --json so early parse errors can be routed through fatalf.
	for _, a := range args {
		if a == "--json" {
			asJSON = true
			break
		}
	}

	// Positional first arg may be a PR number.
	rest := args
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		prNum = rest[0]
		rest = rest[1:]
	}

	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--repo":
			if i+1 >= len(rest) || strings.HasPrefix(rest[i+1], "-") {
				fatalf(asJSON, "--repo requires a value")
			}
			i++
			repo = rest[i]
		case "--json":
			asJSON = true
		case "--pr":
			if i+1 >= len(rest) || strings.HasPrefix(rest[i+1], "-") {
				fatalf(asJSON, "--pr requires a value")
			}
			i++
			prNum = rest[i]
		default:
			fatalf(asJSON, "unknown flag: %s", rest[i])
		}
	}

	// Auto-detect repo from CWD when not provided.
	if repo == "" {
		repo = detectRepo()
		if repo == "" {
			fatalf(asJSON, "no repository detected — run from inside a git checkout or pass --repo")
		}
	}

	// Auto-detect PR from current branch when not provided.
	if prNum == "" {
		prNum = detectCurrentPR(repo)
		if prNum == "" {
			fatalf(asJSON, "could not detect current PR — pass a PR number as the first argument")
		}
	}

	// Validate PR number is a positive integer.
	pr, err := strconv.Atoi(prNum)
	if err != nil || pr <= 0 {
		fatalf(asJSON, "PR number must be a positive integer, got: %s", prNum)
	}

	// Query PR state via gh.
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

// queryPRStatus returns a normalised status string for the given PR using gh.
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
	).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 500 {
			msg = msg[:500] + "..."
		}
		if msg != "" {
			return "error", fmt.Errorf("gh pr view: %w: %s", err, msg)
		}
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
		// Check for Copilot pending review.
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
	case "":
		if len(p.Reviews) == 0 {
			return "no_reviews", nil
		}
		return "new_review", nil
	}

	return "new_review", nil
}

// detectCurrentPR tries to find an open PR for the current git branch.
func detectCurrentPR(repo string) string {
	branch, err := exec.Command("git", "branch", "--show-current").Output()
	if err != nil || strings.TrimSpace(string(branch)) == "" {
		return ""
	}
	b := strings.TrimSpace(string(branch))

	out, err := exec.Command("gh", "pr", "list",
		"--repo", repo,
		"--head", b,
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

// ── start subcommand ──────────────────────────────────────────────────────────

func runStartCmd(args []string) {
	var (
		prNum       string
		repo        string
		cwd         string
		model       string
		runnerName  string
		reflect     bool
		reflectMain string
		autoMerge   bool
		asJSON      bool
	)

	// Pre-scan for --json so early errors can be routed through fatalf.
	for _, a := range args {
		if a == "--json" {
			asJSON = true
			break
		}
	}

	// Positional first arg is the required PR number.
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fatalf(asJSON, "usage: rinse start <pr_number> [options]")
	}
	prNum = args[0]
	prInt, err := strconv.Atoi(prNum)
	if err != nil || prInt <= 0 {
		fatalf(asJSON, "PR number must be a positive integer, got: %s", prNum)
	}
	rest := args[1:]

	for i := 0; i < len(rest); i++ {
		consumeFlagValue := func(flag string) string {
			if i+1 >= len(rest) || strings.HasPrefix(rest[i+1], "-") {
				fatalf(asJSON, "flag %s requires a value", flag)
			}
			i++
			return rest[i]
		}

		switch rest[i] {
		case "--repo":
			repo = consumeFlagValue("--repo")
		case "--cwd":
			cwd = consumeFlagValue("--cwd")
		case "--model":
			model = consumeFlagValue("--model")
		case "--runner":
			runnerName = consumeFlagValue("--runner")
		case "--reflect":
			reflect = true
		case "--reflect-main-branch":
			reflectMain = consumeFlagValue("--reflect-main-branch")
		case "--auto-merge":
			autoMerge = true
		case "--json":
			asJSON = true
		default:
			fatalf(asJSON, "unknown flag: %s", rest[i])
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

	// Resolve runner by name (default: first runner = opencode).
	runnerIdx := 0
	if runnerName != "" {
		found := false
		for i, r := range runners {
			if strings.EqualFold(r.name, runnerName) {
				runnerIdx = i
				found = true
				break
			}
		}
		if !found {
			fatalf(asJSON, "unknown runner %q — valid: opencode, claude", runnerName)
		}
	}
	r := runners[runnerIdx]

	if model == "" {
		model = r.defaultModel
	}
	if reflectMain == "" {
		reflectMain = "main"
	}

	// Find the runner script.
	script, err := resolveScript(r.script)
	if err != nil {
		fatalf(asJSON, "%v", err)
	}

	// Build argument list.
	cmdArgs := []string{script, prNum, "--repo", repo, "--cwd", cwd, "--model", model, "--no-interactive"}
	if reflect {
		cmdArgs = append(cmdArgs, "--reflect", "--reflect-main-branch", reflectMain)
	}
	if autoMerge {
		cmdArgs = append(cmdArgs, "--auto-merge")
	}

	if asJSON {
		// In --json mode run the script with inherited stdio so the agent can
		// observe streaming output, then emit a final JSON result line.
		exitCode := execInherited(cmdArgs)
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

	// Plain mode: exec replaces the process so the runner owns the terminal.
	execReplace(cmdArgs)
}

// resolveScript locates a runner script relative to the binary, mirroring the
// logic in wizard.go's buildCmd so both code paths stay in sync.
func resolveScript(scriptName string) (string, error) {
	scriptDir := os.Getenv("RINSE_SCRIPT_DIR")
	if scriptDir == "" {
		scriptDir = os.Getenv("PR_REVIEW_SCRIPT_DIR")
	}
	if scriptDir == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("could not determine script directory: %w", err)
		}
		binDir := filepath.Dir(exe)
		candidates := []string{
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
		return "", fmt.Errorf("runner script not found: %s", script)
	}
	return script, nil
}

// execInherited runs a command with inherited stdio and returns its exit code.
// Used in --json mode so streaming output is visible while we still capture the exit code.
func execInherited(args []string) int {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return 1
	}
	return 0
}

// execReplace replaces the current process with the given command using exec.
// On Darwin/Linux this uses syscall.Exec; we fall back to run+exit on error.
func execReplace(args []string) {
	path, err := exec.LookPath(args[0])
	if err != nil {
		path = args[0]
	}
	if err := execSyscall(path, args); err != nil {
		// Fallback: run with inherited stdio.
		os.Exit(execInherited(args))
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

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

func printCLIHelp() {
	fmt.Print(`rinse — Autonomous Copilot PR review lifecycle manager

USAGE
  rinse [subcommand] [flags]

  Without a subcommand, rinse launches the interactive TUI.

SUBCOMMANDS
  status [<pr>] [--repo <owner/repo>] [--json]
      Print the current Copilot review status of a PR.
      Detects PR from the current git branch when <pr> is omitted.

      Statuses: approved | pending | new_review | no_reviews | merged | closed | error

  start <pr> [options] [--json]
      Start the PR review fix loop non-interactively (no TUI, no TTY needed).
      Useful for agents and CI pipelines.

      --repo <owner/repo>         Override repository detection
      --cwd  <path>               Local checkout path (default: current directory)
      --model <model>             AI model string (default: runner default)
      --runner opencode|claude    Runner to use (default: opencode)
      --reflect                   Enable reflection agent to update AGENTS.md
      --reflect-main-branch <br>  Branch for reflection commits (default: main)
      --auto-merge                Auto-merge when Copilot approves
      --json                      Emit a JSON result line after the runner exits

  help | --help
      Show this help.

  --version | -v
      Print version.

EXAMPLES
  # Check status of PR #42 as JSON
  rinse status 42 --repo owner/repo --json

  # Start the review loop for PR #42 (agent-friendly, no TTY needed)
  rinse start 42 --repo owner/repo --cwd /path/to/repo

  # Same but capture the result as JSON (streaming output still goes to stdout)
  rinse start 42 --repo owner/repo --json

`)
}
