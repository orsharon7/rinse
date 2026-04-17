package tui

import (
	"testing"
	"time"
)

// ── resolveETA ────────────────────────────────────────────────────────────────

func ptr(t time.Time) *time.Time { return &t }

func TestResolveETA(t *testing.T) {
	now := time.Date(2026, 4, 17, 10, 0, 0, 0, time.UTC)
	todayETA := now.Add(5 * time.Minute)      // 10:05 same day → etaComputable
	pastETA := now.Add(-1 * time.Minute)      // 09:59 → etaOverdue
	tomorrowETA := now.Add(25 * time.Hour)    // next day → etaFutureDay

	tests := []struct {
		name          string
		phase         phase
		estimatedEnd  *time.Time
		wantState     etaState
		wantNonZero   bool // whether the returned time should be non-zero
	}{
		// etaHidden — phaseStarting always hides ETA regardless of estimatedEnd
		{"hidden/starting-no-eta", phaseStarting, nil, etaHidden, false},
		{"hidden/starting-with-eta", phaseStarting, ptr(todayETA), etaHidden, false},

		// etaCompleted / etaError
		{"completed/done", phaseDone, nil, etaCompleted, false},
		{"completed/done-with-eta", phaseDone, ptr(todayETA), etaCompleted, false},
		{"error/phaseError", phaseError, nil, etaError, false},
		{"error/phaseError-with-eta", phaseError, ptr(todayETA), etaError, false},

		// etaUnknown — active phase but no estimatedEnd
		{"unknown/waiting-nil", phaseWaiting, nil, etaUnknown, false},
		{"unknown/fixing-nil", phaseFixing, nil, etaUnknown, false},
		{"unknown/reflecting-nil", phaseReflecting, nil, etaUnknown, false},

		// etaComputable — ETA today and in the future
		{"computable/waiting", phaseWaiting, ptr(todayETA), etaComputable, true},
		{"computable/fixing", phaseFixing, ptr(todayETA), etaComputable, true},
		{"computable/reflecting", phaseReflecting, ptr(todayETA), etaComputable, true},

		// etaOverdue — past estimated end
		{"overdue/waiting", phaseWaiting, ptr(pastETA), etaOverdue, true},
		{"overdue/fixing", phaseFixing, ptr(pastETA), etaOverdue, true},

		// etaFutureDay — ETA is tomorrow or later
		{"futureday/waiting", phaseWaiting, ptr(tomorrowETA), etaFutureDay, true},
		{"futureday/fixing", phaseFixing, ptr(tomorrowETA), etaFutureDay, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotState, gotTime := resolveETA(tc.phase, tc.estimatedEnd, now)
			if gotState != tc.wantState {
				t.Errorf("resolveETA(%v, eta=%v): got state=%v, want %v",
					tc.phase, tc.estimatedEnd, gotState, tc.wantState)
			}
			if tc.wantNonZero && gotTime.IsZero() {
				t.Errorf("resolveETA(%v, eta=%v): expected non-zero time, got zero", tc.phase, tc.estimatedEnd)
			}
			if !tc.wantNonZero && !gotTime.IsZero() {
				t.Errorf("resolveETA(%v, eta=%v): expected zero time, got %v", tc.phase, tc.estimatedEnd, gotTime)
			}
		})
	}
}
