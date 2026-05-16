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

