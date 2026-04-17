// exec_unix.go — syscall.Exec wrapper for Unix platforms.
//go:build !windows

package main

import (
	"os"
	"syscall"
)

// execSyscall replaces the current process image with the given command.
// env is inherited from the current process.
func execSyscall(path string, args []string) error {
	return syscall.Exec(path, args, os.Environ())
}
