package main

import (
	"fmt"
	"os"

	"github.com/orsharon7/rinse/internal/cli"
	"github.com/orsharon7/rinse/internal/tui"
)

// version is set at build time via -ldflags "-X main.version=<tag>".
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("rinse %s\n", version)
		os.Exit(0)
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

	if err := tui.Run(version); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
