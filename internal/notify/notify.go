// Package notify sends desktop notifications after RINSE review cycles complete.
// This is a stub implementation — full desktop notification support is a v0.4 feature.
package notify

import "time"

// CycleResult describes the outcome of a RINSE review cycle.
type CycleResult int

const (
	// ResultApproved indicates the PR was approved.
	ResultApproved CycleResult = iota
	// ResultError indicates the cycle ended with an error.
	ResultError
)

// CycleParams holds the data for a cycle notification.
type CycleParams struct {
	PR      string
	Repo    string
	Result  CycleResult
	Elapsed time.Duration
}

// CycleNotification sends a desktop notification (stub: no-op for now).
// Full implementation (using beeep or OS-native APIs) is deferred to v0.4.
func CycleNotification(_ bool, _ CycleParams) {
	// no-op stub
}
