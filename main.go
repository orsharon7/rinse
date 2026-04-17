package main

import (
	"fmt"
	"os"

	"github.com/orsharon7/rinse/internal/cli"
	"github.com/orsharon7/rinse/internal/session"
	"github.com/orsharon7/rinse/internal/tui"
)

// version is set at build time via -ldflags "-X main.version=<tag>".
var version = "dev"

const helpText = `rinse — AI-powered PR review that fixes your code automatically.

RINSE drives an AI agent in a loop to resolve GitHub Copilot review comments
until your PR is approved. You pick the PR; RINSE handles the rest.

USAGE

  rinse              Launch the interactive PR picker (recommended)
  rinse stats        Show session history and time-saved metrics
  rinse --version    Print the installed version
  rinse --help       Show this help

QUICK START

  cd your-repo
  rinse

  RINSE auto-detects your repository and lists open PRs. Press Enter to
  launch the review cycle on the selected PR.

INTERACTIVE CONTROLS

  ↑ ↓ / j k    Navigate the PR list
  Enter         Launch review cycle on the selected PR
  g / G         Jump to top / bottom of list
  #             Enter a PR number manually
  s             Open settings (runner, model, reflect, auto-merge)
  r             Refresh PR list from GitHub
  ?             Toggle keyboard shortcuts overlay
  q / Ctrl+C    Quit

SETTINGS  (press s inside the PR picker)

  runner        opencode  GitHub Copilot, no API key required (default)
                claude    Claude Code, requires an Anthropic API key

  model         AI model to use. Leave blank for the runner's default.
                opencode default: github-copilot/claude-sonnet-4.6
                claude default:   claude-sonnet-4-6

  reflect       When on, a second AI agent extracts generalizable coding
                rules from review comments and pushes them to AGENTS.md
                and CLAUDE.md on your main branch. Each future cycle
                starts with those rules loaded — fewer comments over time.

  branch        The branch where reflection rules are pushed (default: main)

  auto-merge    When on, RINSE merges the PR automatically once approved.

  Settings are saved per-repo under ~/.rinse/.

COMMANDS

  rinse stats

    Reads session history from ~/.rinse/sessions/ and prints:

      RINSE Stats (last 30 days)
      PRs reviewed:     23
      Comments fixed:   187
      Avg iterations:   2.1
      Est. time saved:  ~9.4 hours

      Top patterns:
        1. Missing error handling  (41x)
        2. Unused imports          (28x)

ENVIRONMENT VARIABLES

  RINSE_SCRIPT_DIR      Override the directory where runner scripts are found.
  PR_REVIEW_SCRIPT_DIR  Fallback script directory (legacy alias).
  RINSE_WEBHOOK_URL     When set, POST a JSON payload to this URL after each
                        completed review cycle.

REQUIREMENTS

  gh v2.88+   GitHub CLI — used by all runners
  opencode    Required for the opencode runner
  claude      Required for the claude runner
  jq          Required by shell scripts
  git         Required by the reflection agent

SESSION DATA

  Each run is saved as a JSON file in ~/.rinse/sessions/. Files contain the
  repo, PR number, runner, model, comments fixed, iterations, approval status,
  and detected code patterns. No data leaves your machine.

MORE

  GitHub:    https://github.com/orsharon7/rinse
  Pro tier:  https://rinse.sh
`

func main() {
	if len(os.Args) > 1 {
		// Dispatch one-shot CLI subcommands (status, start) for agent/CI use.
		// cli.TryDispatch handles these and returns true; other args fall through
		// to the existing switch below.
		if cli.TryDispatch() {
			return
		}

		switch os.Args[1] {
		case "--help", "-h":
			fmt.Print(helpText)
			os.Exit(0)
		case "--version", "-v":
			fmt.Printf("rinse %s\n", version)
			os.Exit(0)
		case "init":
			if err := tui.RunInit(); err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
			os.Exit(0)
		case "stats":
			sessions, err := session.LoadAll()
			if err != nil {
				fmt.Fprintln(os.Stderr, "error reading sessions:", err)
				os.Exit(1)
			}
			session.PrintStats(sessions)
			os.Exit(0)
		}
	}

	if err := tui.Run(version); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
