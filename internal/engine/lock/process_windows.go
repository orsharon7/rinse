//go:build windows

package lock

import (
	"syscall"
)

const (
	// PROCESS_QUERY_LIMITED_INFORMATION requires fewer privileges than
	// PROCESS_QUERY_INFORMATION and is available since Windows Vista.
	processQueryLimitedInformation = 0x1000
	// STILL_ACTIVE is the pseudo-exit-code returned by GetExitCodeProcess
	// for a process that has not yet exited.
	stillActive = 259
)

// isProcessAlive reports whether the process with the given PID is still running.
// On Windows there is no kill(pid, 0) equivalent, so we:
//  1. Open a handle with PROCESS_QUERY_LIMITED_INFORMATION (non-destructive).
//  2. Call GetExitCodeProcess — returns STILL_ACTIVE (259) if running.
//
// A missing or terminated process causes OpenProcess to fail with
// ERROR_INVALID_PARAMETER or the handle yields a non-STILL_ACTIVE exit code.
func isProcessAlive(pid int) bool {
	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle) //nolint:errcheck

	var exitCode uint32
	if err := syscall.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false
	}
	return exitCode == stillActive
}
