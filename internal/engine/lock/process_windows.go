//go:build windows

package lock

import (
	"os"
)

// isProcessAlive on Windows: os.FindProcess always succeeds; we attempt to
// open the process handle via signal(0) equivalent. On Windows, Signal(os.Interrupt)
// would be needed for a real kill, but FindProcess alone doesn't tell us if the
// process is live. We use a best-effort approach: try to open the process.
func isProcessAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Windows, FindProcess always succeeds even for dead PIDs.
	// Use Signal(os.Interrupt) with recover as a liveness probe — not ideal,
	// but acceptable for a best-effort stale-lock check.
	err = p.Signal(os.Interrupt)
	return err == nil
}
