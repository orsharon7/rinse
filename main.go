package main

import (
	"fmt"
	"os"

	"github.com/orsharon7/rinse/internal/cli"
	"github.com/orsharon7/rinse/internal/session"
	"github.com/orsharon7/rinse/internal/stats"
	"github.com/orsharon7/rinse/internal/tui"
)

// version is set at build time via -ldflags "-X main.version=<tag>".
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		// cli.TryDispatch handles: status, start, --help/-h/help.
		// It returns true when the subcommand has been handled; main() exits.
		// All other args fall through to the switch below.
		if cli.TryDispatch() {
			return
		}

		switch os.Args[1] {
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
			// rinse stats --predict
			if len(os.Args) > 2 && os.Args[2] == "--predict" {
				stats.PrintPredictStats()
				os.Exit(0)
			}
			sessions, err := session.LoadAll()
			if err != nil {
				fmt.Fprintln(os.Stderr, "error reading sessions:", err)
				os.Exit(1)
			}
			session.PrintStats(sessions)
			os.Exit(0)
		case "report":
			sessions, err := stats.Load()
			if err != nil {
				fmt.Fprintln(os.Stderr, "error reading sessions:", err)
				os.Exit(1)
			}
			stats.PrintReport(sessions)
			os.Exit(0)
		case "opt-in":
			if err := stats.SetOptIn(true); err != nil {
				fmt.Fprintln(os.Stderr, "error saving preference:", err)
				os.Exit(1)
			}
			sessionsDir, err := stats.SessionsDir()
			if err != nil {
				fmt.Println("  Stats collection enabled.")
			} else {
				fmt.Printf("  Stats collection enabled. Sessions will be saved to %s\n", sessionsDir)
			}
			os.Exit(0)
		case "opt-out":
			if err := stats.SetOptIn(false); err != nil {
				fmt.Fprintln(os.Stderr, "error saving preference:", err)
				os.Exit(1)
			}
			fmt.Println("  Stats collection disabled. No new sessions will be saved.")
			os.Exit(0)
		}
	}

	// Dispatch help before dependency checks so docs are always accessible.
	if len(os.Args) > 1 && (os.Args[1] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h") {
		cli.PrintHelp()
		return
	}

	// Guard: check required tools (git, gh) before anything else so first-time
	// users get a clear, styled error with install instructions rather than
	// cryptic failures inside the TUI.
	cli.CheckDependencies()

	// Guard: ensure the user is authenticated with GitHub CLI.
	cli.CheckGHAuth()

	// Dispatch one-shot CLI subcommands (status, start) before the TUI.
	// Returns true when a subcommand was handled; main() should exit.
	if cli.TryDispatch() {
		return
	}

	// Guard: when no --pr flag is given, check that the user has staged changes.
	// If nothing is staged, show an actionable hint and exit 0 — this is not an
	// error; there simply is nothing for RINSE to do yet.
	cli.CheckStagedChanges()

	if err := tui.Run(version); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
