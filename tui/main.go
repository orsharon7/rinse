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

	// Handle -h/--help flags.
	if len(os.Args) > 1 && (os.Args[1] == "--help" || os.Args[1] == "-h") {
		fmt.Println("usage: rinse [init|--version|-v|-h|--help]")
		os.Exit(0)
	}

	// Handle subcommands.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "init":
			RunInit()
			os.Exit(0)
		default:
			fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
			fmt.Fprintln(os.Stderr, "usage: rinse [init|--version|-v|-h|--help]")
			os.Exit(1)
		}
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
		os.Exit(0)
	}

	rName := shortRunnerName(fm.runnerIdx)
	runnerCmd := append(fm.finalCmd, "--no-interactive")

	if err := RunMonitor(fm.prNum, fm.repo, strings.TrimSpace(rName), fm.modelOverride, fm.prTitle, fm.path, fm.autoMerge, runnerCmd); err != nil {
		fmt.Fprintln(os.Stderr, "monitor error:", err)
		os.Exit(1)
	}
}
