package cli

import (
	"reflect"
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

func TestExtractPositionalPR(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantPR   string
		wantRest []string
	}{
		{
			name:     "PR before flags",
			args:     []string{"42", "--repo", "owner/repo"},
			wantPR:   "42",
			wantRest: []string{"--repo", "owner/repo"},
		},
		{
			name:     "PR after --repo (regression case)",
			args:     []string{"--repo", "owner/repo", "42"},
			wantPR:   "42",
			wantRest: []string{"--repo", "owner/repo"},
		},
		{
			name:     "PR between flags",
			args:     []string{"--json", "42", "--repo", "owner/repo"},
			wantPR:   "42",
			wantRest: []string{"--json", "--repo", "owner/repo"},
		},
		{
			name:     "PR last after multiple value-flags",
			args:     []string{"--repo", "owner/repo", "--cwd", "/tmp", "--model", "x", "42"},
			wantPR:   "42",
			wantRest: []string{"--repo", "owner/repo", "--cwd", "/tmp", "--model", "x"},
		},
		{
			name:     "no PR — only flags",
			args:     []string{"--json", "--repo", "owner/repo"},
			wantPR:   "",
			wantRest: []string{"--json", "--repo", "owner/repo"},
		},
		{
			name:     "PR followed by stray non-flag is preserved as rest",
			args:     []string{"42", "stray", "--repo", "owner/repo"},
			wantPR:   "42",
			wantRest: []string{"stray", "--repo", "owner/repo"},
		},
		{
			name:     "value of --repo is not consumed as PR",
			args:     []string{"--repo", "123", "42"},
			wantPR:   "42",
			wantRest: []string{"--repo", "123"},
		},
		{
			name:     "empty args",
			args:     []string{},
			wantPR:   "",
			wantRest: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPR, gotRest := extractPositionalPR(tt.args)
			if gotPR != tt.wantPR {
				t.Errorf("PR = %q, want %q", gotPR, tt.wantPR)
			}
			if !reflect.DeepEqual(gotRest, tt.wantRest) {
				t.Errorf("rest = %v, want %v", gotRest, tt.wantRest)
			}
		})
	}
}

