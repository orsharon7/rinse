package tui

import (
	"reflect"
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

func TestExtractPatterns(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  []string
	}{
		{
			name:  "drops operational status lines",
			lines: []string{"starting  (model: github-copilot/claude-sonnet-4.6 → main)", "killed (opencode failed)", "complete"},
			want:  nil,
		},
		{
			name:  "classifies error-handling rule",
			lines: []string{"Always check the error returned by os.WriteFile and propagate it"},
			want:  []string{"error_handling"},
		},
		{
			name:  "classifies security rule",
			lines: []string{"Avoid hardcoded secrets — load credentials from env"},
			want:  []string{"security"},
		},
		{
			name:  "deduplicates same category",
			lines: []string{"unhandled error", "ignore the error"},
			want:  []string{"error_handling"},
		},
		{
			name:  "drops unknown text but keeps recognised lines",
			lines: []string{"random unrelated chatter", "fix the nil pointer dereference"},
			want:  []string{"correctness"},
		},
		{
			name:  "respects max",
			lines: []string{"unhandled error", "fix nil pointer", "secret leak", "naming style"},
			want:  []string{"error_handling", "correctness"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			max := 5
			if tt.name == "respects max" {
				max = 2
			}
			got := extractPatterns(tt.lines, max)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("extractPatterns = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsOperationalReflectLine(t *testing.T) {
	operational := []string{
		"starting",
		"complete",
		"done",
		"killed (opencode failed)",
		"starting (model: github-copilot/claude-sonnet-4.6 → main)",
		"3 rule(s) pushed to main",
		"no changes detected",
		"nothing to commit",
		"committing reflection rules",
		"pushed to main",
		"branch: feat/x",
		"pulling from origin",
	}
	for _, l := range operational {
		if !isOperationalReflectLine(l) {
			t.Errorf("expected operational: %q", l)
		}
	}
	rules := []string{
		"Always check the error returned",
		"Avoid hardcoded secrets",
		"Add nil-pointer guard before dereferencing",
	}
	for _, l := range rules {
		if isOperationalReflectLine(l) {
			t.Errorf("expected non-operational rule: %q", l)
		}
	}
}

func TestInferPhase_StalledIsTransient(t *testing.T) {
	tests := []struct {
		name    string
		current phase
		line    string
		want    phase
	}{
		{"stall trigger", phaseWaiting, "   ⚠️  Stalled after 300s — dismissing and re-requesting", phaseStalled},
		{"hold stall on unrelated line", phaseStalled, "some unrelated log line", phaseStalled},
		{"recover on wait-tick", phaseStalled, "⏳ Copilot reviewing (retry)... (45s / 300s)", phaseWaiting},
		{"escalate on terminal stall", phaseStalled, "   ❌ Copilot still stalled after dismiss+retry", phaseError},
		{"approved escapes stall", phaseStalled, "APPROVED by Copilot", phaseDone},
		{"done remains terminal", phaseDone, "any log line", phaseDone},
		{"cancelled remains terminal", phaseCancelled, "any log line", phaseCancelled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferPhase(tt.line, tt.current)
			if got != tt.want {
				t.Errorf("inferPhase(%q, %v) = %v, want %v", tt.line, tt.current, got, tt.want)
			}
		})
	}
}

func TestApplyPhaseChange_StalledAtTracking(t *testing.T) {
	m := monitorModel{}
	m2 := m.applyPhaseChange(phaseStalled)
	if m2.stalledAt.IsZero() {
		t.Error("entering phaseStalled should set stalledAt")
	}
	if m2.frozenElapsed != nil {
		t.Error("entering phaseStalled must NOT freeze elapsed (transient state)")
	}
	m3 := m2.applyPhaseChange(phaseWaiting)
	if !m3.stalledAt.IsZero() {
		t.Error("leaving phaseStalled should clear stalledAt")
	}
	m4 := m.applyPhaseChange(phaseDone)
	if m4.frozenElapsed == nil {
		t.Error("entering phaseDone should freeze elapsed (terminal state)")
	}
}
