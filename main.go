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

// commandsRequiringDeps lists subcommands that exec gh/git/runners and so
// must pass cli.CheckDependencies before running. Commands not listed here
// (--version, init, opt-in, opt-out, help, stats, report) work without external tools.
var commandsRequiringDeps = map[string]bool{
	"start":       true,
	"run":         true,
	"status":      true,
	"predict":     true,
	"wait-review": true,
}

func main() {
	if len(os.Args) > 1 {
		sub := os.Args[1]

		if commandsRequiringDeps[sub] {
			cli.CheckDependencies()
			cli.CheckGHAuth()
		}

		if cli.TryDispatch() {
			return
		}

		switch sub {
		case "--version", "-v":
			fmt.Printf("rinse %s\n", version)
			return
		case "init":
			if err := tui.RunInit(); err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
			return
		case "stats":
			if len(os.Args) > 2 && os.Args[2] == "--predict" {
				stats.PrintPredictStats()
				return
			}
			sessions, err := session.LoadAll()
			if err != nil {
				fmt.Fprintln(os.Stderr, "error reading sessions:", err)
				os.Exit(1)
			}
			session.PrintStats(sessions)
			return
		case "report":
			sessions, err := stats.Load()
			if err != nil {
				fmt.Fprintln(os.Stderr, "error reading sessions:", err)
				os.Exit(1)
			}
			stats.PrintReport(sessions)
			return
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
			return
		case "opt-out":
			if err := stats.SetOptIn(false); err != nil {
				fmt.Fprintln(os.Stderr, "error saving preference:", err)
				os.Exit(1)
			}
			fmt.Println("  Stats collection disabled. No new sessions will be saved.")
			return
		}
	}

	cli.CheckDependencies()
	cli.CheckGHAuth()

	if err := tui.Run(version); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
