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
		}
	}

	// Dispatch one-shot CLI subcommands (status, start, help) before the TUI.
	// Returns true when a subcommand was handled; main() should exit.
	if cli.TryDispatch() {
		return
	}

	if err := tui.Run(version); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
