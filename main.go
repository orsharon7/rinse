package main

import (
	"fmt"
	"os"

	"github.com/orsharon7/rinse/internal/stats"
	"github.com/orsharon7/rinse/internal/tui"
)

// version is set at build time via -ldflags "-X main.version=<tag>".
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v":
			fmt.Printf("rinse %s\n", version)
			os.Exit(0)
		case "stats":
			sessions, err := stats.Load()
			if err != nil {
				fmt.Fprintln(os.Stderr, "error reading sessions:", err)
				os.Exit(1)
			}
			if len(sessions) == 0 {
				fmt.Println("\n  No sessions recorded yet. Run rinse on a PR to start tracking stats.\n")
				optedIn, err := stats.IsOptedIn()
				if err != nil {
					fmt.Fprintln(os.Stderr, "warning: could not determine stats opt-in status:", err)
				} else if !optedIn {
					fmt.Println("  If stats collection is not enabled, run: rinse opt-in\n")
				}
				os.Exit(0)
			}
			stats.Print(sessions)
			os.Exit(0)
		case "opt-in":
			if err := stats.SetOptIn(true); err != nil {
				fmt.Fprintln(os.Stderr, "error saving preference:", err)
				os.Exit(1)
			}
			fmt.Println("  Stats collection enabled. Sessions will be saved to ~/.rinse/sessions/")
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

	if err := tui.Run(version); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
