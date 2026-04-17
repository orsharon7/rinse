//go:build !windows

package cli

import (
	"os"
	"os/exec"
	"syscall"
)

// execReplace replaces the current process with args via syscall.Exec (Unix).
// Falls back to execInherited+exit on error.
func execReplace(args []string) {
	path, err := exec.LookPath(args[0])
	if err != nil {
		path = args[0]
	}
	// syscall.Exec replaces the process image; env is inherited.
	if err := syscall.Exec(path, args, os.Environ()); err != nil {
		// Fallback: run with inherited stdio.
		os.Exit(execInherited(args))
	}
}
