package main

import (
	"fmt"
	"os"

	"github.com/orsharon7/rinse/internal/session"
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
			runStats()
			return
		}
	}

	if err := tui.Run(version); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// runStats loads all recorded sessions and prints aggregate statistics.
func runStats() {
	sessions, err := session.LoadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "rinse stats: %v\n", err)
		os.Exit(1)
	}
	session.PrintStats(sessions)
}
