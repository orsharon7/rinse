package main

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	// Handle --version flag.
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("rinse %s\n", version)
		os.Exit(0)
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
	exitCode, err := RunMonitor(fm.prNum, fm.repo, strings.TrimSpace(rName), fm.modelOverride, fm.prTitle, fm.path, fm.autoMerge, runnerCmd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "monitor error:", err)
		os.Exit(1)
	}
	os.Exit(exitCode)
}
