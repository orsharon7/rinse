//go:build !windows

package lock

import (
	"errors"
	"syscall"
)

// isProcessAlive reports whether the process with the given PID is running.
// On Unix we use signal 0 (kill -0): it requires no permission beyond being in
// the same namespace.
//
// Return values from kill(pid, 0):
//   - nil    → process exists and we have permission (alive)
//   - ESRCH  → no such process (dead / reaped)
//   - EPERM  → process exists but we lack signal permission (alive — different owner)
func isProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
