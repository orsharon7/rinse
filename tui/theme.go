package main

import (
	"github.com/charmbracelet/lipgloss"
)

// ── Icons ─────────────────────────────────────────────────────────────────────
// Consistent icon vocabulary inspired by charmbracelet/crush.

const (
	IconDiamond  = "◇"
	IconCheck    = "✓"
	IconCross    = "×"
	IconDot      = "●"
	IconCircle   = "○"
	IconRadioOn  = "◉"
	IconRadioOff = "○"
	IconArrow    = "→"
	IconSlash    = "╱"
	IconThickBar = "▌"
	IconPending  = "●"
	IconRunning  = "◌"
	IconSep      = "·"
)

// ── Palette (Catppuccin Macchiato) ────────────────────────────────────────────

var (
	// Brand / primary
	mauve    = lipgloss.Color("#C6A0F6")
	lavender = lipgloss.Color("#B7BDF8")

	// Semantic
	teal   = lipgloss.Color("#8BD5CA")
	green  = lipgloss.Color("#A6DA95")
	red    = lipgloss.Color("#ED8796")
	yellow = lipgloss.Color("#EED49F")
	peach  = lipgloss.Color("#F5A97F")
	sky    = lipgloss.Color("#91D7E3")

	// Neutrals
	text    = lipgloss.Color("#CAD3F5")
	subtext = lipgloss.Color("#A5ADCB")
	overlay = lipgloss.Color("#6E738D")
	surface = lipgloss.Color("#363A4F")
	crust   = lipgloss.Color("#181926")
)

// ── Wizard Styles ─────────────────────────────────────────────────────────────

var (
	// Logo & branding — ◇ pr-review
	styleLogo = lipgloss.NewStyle().
			Foreground(mauve).
			Bold(true)

	styleLogoIcon = lipgloss.NewStyle().
			Foreground(mauve)

	styleLogoSlash = lipgloss.NewStyle().
			Foreground(overlay)

	// Reusable text presets
	styleBanner = lipgloss.NewStyle().
			Bold(true).
			Foreground(mauve).
			Padding(0, 1)

	styleKey   = lipgloss.NewStyle().Foreground(overlay).Width(16)
	styleVal   = lipgloss.NewStyle().Foreground(lavender).Bold(true)
	styleMuted = lipgloss.NewStyle().Foreground(overlay)
	styleStep  = lipgloss.NewStyle().Foreground(mauve).Bold(true)
	styleErr   = lipgloss.NewStyle().Foreground(red)
	styleTeal  = lipgloss.NewStyle().Foreground(teal).Bold(true)

	// PR list items — Crush-style thick left border for selected
	styleSelected   = lipgloss.NewStyle().Foreground(mauve).Bold(true)
	styleUnselected = lipgloss.NewStyle().Foreground(subtext)

	styleSelectedBar = lipgloss.NewStyle().
				Foreground(mauve).Bold(true)

	// PR number styling
	stylePRNum = lipgloss.NewStyle().
			Foreground(lavender).Bold(true)

	stylePRNumMuted = lipgloss.NewStyle().
				Foreground(overlay)

	// Settings bar / ribbon
	styleRibbon = lipgloss.NewStyle().
			Foreground(subtext).
			BorderStyle(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(surface).
			Padding(0, 1)

	// Settings overlay box
	styleSettingsBox = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(mauve).
				Padding(1, 3)

	// Key hint presets
	styleHintKey = lipgloss.NewStyle().
			Foreground(subtext)

	styleHintDesc = lipgloss.NewStyle().
			Foreground(overlay)

	// Separator line
	styleSeparator = lipgloss.NewStyle().
			Foreground(surface)
)

// ── Monitor Styles ────────────────────────────────────────────────────────────

var (
	// Header: bottom border separates from log area.
	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(text).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(surface).
			Padding(0, 1)

	styleHeaderLabel = lipgloss.NewStyle().Foreground(overlay)
	styleHeaderVal   = lipgloss.NewStyle().Foreground(lavender).Bold(true)

	// Status bar: top border, no background.
	styleStatusBar = lipgloss.NewStyle().
			Foreground(subtext).
			BorderStyle(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(surface).
			Padding(0, 1)

	// Phase indicators
	stylePhaseWaiting = lipgloss.NewStyle().Foreground(yellow).Bold(true)
	stylePhaseFixing  = lipgloss.NewStyle().Foreground(mauve).Bold(true)
	stylePhaseReflect = lipgloss.NewStyle().Foreground(teal).Bold(true)
	stylePhaseDone    = lipgloss.NewStyle().Foreground(green).Bold(true)
	stylePhaseErr     = lipgloss.NewStyle().Foreground(red).Bold(true)

	// Log line semantic styles
	styleLogInfo    = lipgloss.NewStyle().Foreground(text)
	styleLogDebug   = lipgloss.NewStyle().Foreground(subtext)
	styleLogWarn    = lipgloss.NewStyle().Foreground(yellow)
	styleLogErr     = lipgloss.NewStyle().Foreground(red).Bold(true)
	styleLogIter    = lipgloss.NewStyle().Foreground(mauve).Bold(true)
	styleLogAgent   = lipgloss.NewStyle().Foreground(text)
	styleLogSuccess = lipgloss.NewStyle().Foreground(green).Bold(true)
	styleLogGit     = lipgloss.NewStyle().Foreground(peach)
	styleLogAPI     = lipgloss.NewStyle().Foreground(sky)

	// Stat badges — compact pill labels
	styleBadge        = lipgloss.NewStyle().Foreground(crust).Padding(0, 1)
	styleBadgeIter    = styleBadge.Background(mauve)
	styleBadgeComment = styleBadge.Background(yellow)
	styleBadgeRules   = styleBadge.Background(teal)
	styleBadgeTime    = styleBadge.Background(lavender)

	// Reflect side-panel
	styleReflectPanel = lipgloss.NewStyle().
				BorderStyle(lipgloss.NormalBorder()).
				BorderLeft(true).
				BorderForeground(teal).
				Padding(0, 1)
	styleReflectTitle = lipgloss.NewStyle().Foreground(teal).Bold(true)
	styleReflectLine  = lipgloss.NewStyle().Foreground(subtext)
	styleReflectNew   = lipgloss.NewStyle().Foreground(text)
	styleReflectOK    = lipgloss.NewStyle().Foreground(green)
	styleReflectFail  = lipgloss.NewStyle().Foreground(red)

	// Iteration timeline dots
	styleTimelineDot     = lipgloss.NewStyle().Foreground(mauve)
	styleTimelineDone    = lipgloss.NewStyle().Foreground(green)
	styleTimelineErr     = lipgloss.NewStyle().Foreground(red)
	styleTimelineCurrent = lipgloss.NewStyle().Foreground(yellow).Bold(true)

	// Toast notification overlay
	styleToast = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(green).
			Padding(0, 2).
			Foreground(text).
			Bold(true)

	// Post-cycle menu
	styleMenuBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(teal).
			Padding(1, 4)
)
