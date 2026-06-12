package tui

import (
	"reflect"
	"strings"
	"testing"
)

func TestFilterModels(t *testing.T) {
	models := []string{
		"opencode/big-pickle",
		"github-copilot/claude-opus-4.7",
		"github-copilot/claude-sonnet-4.6",
		"github-copilot/gpt-5.4",
		"github-copilot/gemini-3.1-pro-preview",
		"kimi-apim/Kimi-K2.5",
	}
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "empty query keeps all",
			query: "",
			want:  models,
		},
		{
			name:  "default github-copilot filter",
			query: defaultModelFilter,
			want: []string{
				"github-copilot/claude-opus-4.7",
				"github-copilot/claude-sonnet-4.6",
				"github-copilot/gpt-5.4",
				"github-copilot/gemini-3.1-pro-preview",
			},
		},
		{
			name:  "multi-token AND (copilot + sonnet)",
			query: "copilot sonnet",
			want:  []string{"github-copilot/claude-sonnet-4.6"},
		},
		{
			name:  "case-insensitive",
			query: "GITHUB",
			want: []string{
				"github-copilot/claude-opus-4.7",
				"github-copilot/claude-sonnet-4.6",
				"github-copilot/gpt-5.4",
				"github-copilot/gemini-3.1-pro-preview",
			},
		},
		{
			name:  "no match",
			query: "zzz",
			want:  []string{},
		},
		{
			name:  "whitespace only is empty",
			query: "   ",
			want:  models,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterModels(models, tt.query)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("filterModels(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestPickerVisibleRows(t *testing.T) {
	tests := []struct {
		termHeight int
		want       int
	}{
		{0, 4},
		{10, 4},
		{16, 4},
		{17, 5},
		{20, 8},
		{26, 14},
		{40, 14},
	}
	for _, tt := range tests {
		got := pickerVisibleRows(tt.termHeight)
		if got != tt.want {
			t.Errorf("pickerVisibleRows(%d) = %d, want %d", tt.termHeight, got, tt.want)
		}
	}
}

func TestStyleModelRow(t *testing.T) {
	cases := []struct {
		name        string
		selected    bool
		wantContain string
	}{
		{"github-copilot/claude-sonnet-4.6", false, "claude-sonnet-4.6"},
		{"github-copilot/claude-sonnet-4.6", true, "claude-sonnet-4.6"},
		{"bare-model-no-slash", false, "bare-model-no-slash"},
	}
	for _, c := range cases {
		got := styleModelRow(c.name, c.selected)
		if !strings.Contains(got, c.wantContain) {
			t.Errorf("styleModelRow(%q, %v) did not contain %q; got: %q", c.name, c.selected, c.wantContain, got)
		}
	}
}
