//go:build !windows

package lock

import (
	"os"
)

// isProcessAlive reports whether the process with the given PID is running.
// On Unix we use signal 0: it requires no permission beyond being in the same
// namespace, succeeds for any live process, and fails with ESRCH when the
// process does not exist.
func isProcessAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(os.Signal(nil))
	return err == nil
}
