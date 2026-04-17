// exec_windows.go — Windows implementation of execSyscall.
//go:build windows

package main

import "os"

// execSyscall on Windows falls back to running the command with inherited stdio
// and exiting with the same status code, since syscall.Exec is not available.
func execSyscall(path string, args []string) error {
	os.Exit(execInherited(args))
	return nil // unreachable
}
