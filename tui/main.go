package main

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	// Handle --version flag (no dependency check needed).
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("rinse %s\n", version)
		os.Exit(0)
	}

	// Handle help before dependency checks so users can read docs without tools.
	if len(os.Args) > 1 && (os.Args[1] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h") {
		printCLIHelp()
		return
	}

	// First-time / missing dependency guard: check for required tools early so
	// users get a clear guided message instead of cryptic errors deep in the app.
	if missing := checkDependencies(); len(missing) > 0 {
		PrintDependencyError(missing)
		return // unreachable — PrintDependencyError calls os.Exit(1)
	}

	// Check GitHub CLI authentication (requires gh to be installed, checked above).
	if err := checkGHAuth(); err != nil {
		PrintGHAuthError()
		return // unreachable
	}

	// Dispatch CLI subcommands (status, start) before launching the TUI.
	if tryDispatchCLI() {
		return
	}

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
	if fm.view != viewDone || len(fm.finalCmd) == 0 {
		// User quit before launching a review cycle.
		// Exit 2 = cycle not started (meaningful exit code for scripting).
		os.Exit(2)
	}

	rName := shortRunnerName(fm.runnerIdx)
	runnerCmd := append(fm.finalCmd, "--no-interactive")

	// RunMonitor returns the runner's exit code (0=ok, 1=error).
	exitCode, err := RunMonitor(fm.prNum, fm.repo, strings.TrimSpace(rName), fm.modelOverride, fm.prTitle, fm.path, fm.autoMerge, fm.notify, runnerCmd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "monitor error:", err)
		os.Exit(1)
	}
	os.Exit(exitCode)
}
