//go:build windows

package cli

import (
	"os"
	"os/exec"
)

// execReplace is a Windows-safe fallback that uses exec.Command instead of
// syscall.Exec (which is unavailable on Windows).
func execReplace(args []string) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}
	os.Exit(0)
}
