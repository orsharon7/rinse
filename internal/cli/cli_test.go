package cli

import (
	"testing"
)

// TestHasFlag verifies the hasFlag helper used by TryDispatch's Pro gate.
func TestHasFlag(t *testing.T) {
	tests := []struct {
		args []string
		flag string
		want bool
	}{
		{[]string{"--interactive"}, "--interactive", true},
		{[]string{"--doc-drift"}, "--doc-drift", true},
		{[]string{"--json", "--no-log"}, "--interactive", false},
		{[]string{}, "--interactive", false},
		{[]string{"--interactive", "--doc-drift"}, "--doc-drift", true},
	}
	for _, tt := range tests {
		got := hasFlag(tt.args, tt.flag)
		if got != tt.want {
			t.Errorf("hasFlag(%v, %q) = %v, want %v", tt.args, tt.flag, got, tt.want)
		}
	}
}

// TestHasPRFlag verifies the --pr skip-guard used by CheckStagedChanges.
func TestHasPRFlag(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{[]string{"--pr", "42"}, true},
		{[]string{"--pr"}, true},
		{[]string{"--repo", "owner/repo"}, false},
		{[]string{}, false},
		{[]string{"--json", "--pr", "7"}, true},
		{[]string{"--json", "--repo", "o/r"}, false},
		{[]string{"--predict"}, false},
	}
	for _, tt := range tests {
		got := hasPRFlag(tt.args)
		if got != tt.want {
			t.Errorf("hasPRFlag(%v) = %v, want %v", tt.args, got, tt.want)
		}
	}
}
