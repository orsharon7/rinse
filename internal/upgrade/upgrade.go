// Package upgrade handles the Pro upgrade prompt shown after proof-of-value cycles.
package upgrade

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/orsharon7/rinse/internal/theme"
)

// showThresholds are the cycle counts at which the upgrade prompt is shown.
var showThresholds = []int{3, 5, 10, 20}

// upgradeConfig is the on-disk structure for upgrade prompt tracking.
type upgradeConfig struct {
	UpgradePromptShownAt []int `json:"upgrade_prompt_shown_at,omitempty"`
}

// configPath returns the path to ~/.rinse/config.json used for upgrade tracking.
// This uses the home directory (not os.UserConfigDir) to match the sessions dir convention.
func configPath() string {
	home := os.Getenv("HOME")
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	return filepath.Join(home, ".rinse", "config.json")
}

// loadUpgradeConfig reads the upgrade tracking section from config.
func loadUpgradeConfig() upgradeConfig {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return upgradeConfig{}
	}
	var cfg upgradeConfig
	_ = json.Unmarshal(data, &cfg)
	return cfg
}

// saveUpgradeConfig merges the upgrade tracking section into the existing config file.
func saveUpgradeConfig(cfg upgradeConfig) {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}

	// Load existing raw config to preserve unrelated keys.
	existing := map[string]interface{}{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &existing)
	}

	existing["upgrade_prompt_shown_at"] = cfg.UpgradePromptShownAt

	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// ShouldShowPrompt returns true if the upgrade prompt should be shown given
// the current total session count. It checks cycle thresholds and whether the
// prompt has already been shown at this threshold.
func ShouldShowPrompt(totalSessions int) bool {
	// Check if this cycle count hits any threshold.
	hitThreshold := -1
	for _, t := range showThresholds {
		if totalSessions == t {
			hitThreshold = t
			break
		}
	}
	if hitThreshold < 0 {
		return false
	}

	cfg := loadUpgradeConfig()
	for _, shown := range cfg.UpgradePromptShownAt {
		if shown == hitThreshold {
			return false // already shown at this threshold
		}
	}
	return true
}

// RecordShown records that the prompt was shown at the given cycle count.
func RecordShown(cycleCount int) {
	cfg := loadUpgradeConfig()
	cfg.UpgradePromptShownAt = append(cfg.UpgradePromptShownAt, cycleCount)
	saveUpgradeConfig(cfg)
}

// FormatTimeSaved formats total minutes into "Xh Ym" or "Xm" string.
func FormatTimeSaved(totalMin int) string {
	if totalMin <= 0 {
		return "0m"
	}
	d := time.Duration(totalMin) * time.Minute
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// ShowPrompt prints the Pro gate upgrade prompt to stdout and exits 0.
// Call this when a free-tier user attempts to use a Pro-only flag.
func ShowPrompt() {
	star := theme.StylePhaseDone.Render("✦")
	line1 := "  " + star + " " +
		theme.StyleMuted.Render("This flag requires") + " " +
		theme.StyleVal.Render("RINSE Pro") + theme.StyleMuted.Render(".")
	line2 := "    " + theme.StyleMuted.Render("Unlock --interactive and --doc-drift with a Pro licence.")
	urlStyle := lipgloss.NewStyle().Foreground(theme.Overlay).Underline(true)
	line3 := "    " + urlStyle.Render("rinse.sh/#pro")
	fmt.Println(strings.Join([]string{line1, line2, line3}, "\n"))
	os.Exit(0)
}

// RenderPrompt returns the styled upgrade prompt string.
func RenderPrompt(totalMin, totalPRs int) string {
	timeSaved := FormatTimeSaved(totalMin)

	// Line 1: ✦ Enjoying RINSE? You have saved {time} across {prs} PRs.
	star := theme.StylePhaseDone.Render("✦")
	line1 := "  " + star + " " +
		theme.StyleMuted.Render("Enjoying RINSE? You have saved") + " " +
		theme.StyleVal.Render(timeSaved) + " " +
		theme.StyleMuted.Render("across") + " " +
		theme.StyleVal.Render(fmt.Sprintf("%d PRs", totalPRs)) +
		theme.StyleMuted.Render(".")

	// Line 2: feature list
	line2 := "    " + theme.StyleMuted.Render("Unlock Pro for unlimited patterns, team dashboards, and priority support.")

	// Line 3: URL and dismiss hint
	urlStyle := lipgloss.NewStyle().Foreground(theme.Overlay).Underline(true)
	sep := theme.StyleMuted.Render("  ·  ")
	line3 := "    " + urlStyle.Render("rinse.sh/#pro") + sep + theme.StyleMuted.Render("d to dismiss")

	return strings.Join([]string{line1, line2, line3}, "\n")
}
