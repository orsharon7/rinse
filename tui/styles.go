package main

// styles.go — unified Catppuccin Mocha colour palette and lipgloss styles.
//
// All TUI files import from this file. No raw hex strings are allowed elsewhere.

import "github.com/charmbracelet/lipgloss"

// ── Catppuccin Mocha palette ──────────────────────────────────────────────────

var (
	colorMauve   = lipgloss.Color("#CBA6F7")
	colorLavender = lipgloss.Color("#B4BEFE")
	colorTeal    = lipgloss.Color("#94E2D5")
	colorSky     = lipgloss.Color("#89DCEB")
	colorGreen   = lipgloss.Color("#A6E3A1")
	colorPeach   = lipgloss.Color("#FAB387")
	colorYellow  = lipgloss.Color("#F9E2AF")
	colorRed     = lipgloss.Color("#F38BA8")

	colorText     = lipgloss.Color("#CDD6F4")
	colorSubtext  = lipgloss.Color("#BAC2DE")
	colorOverlay  = lipgloss.Color("#6C7086")
	colorSurface  = lipgloss.Color("#313244")
	colorCrust    = lipgloss.Color("#11111B")
)

// ── Semantic aliases (required by the issue spec) ─────────────────────────────
// These six names should be used throughout the TUI for semantic colouring.

var (
	// Primary is the primary accent colour (mauve).
	Primary = lipgloss.NewStyle().Foreground(colorMauve)
	// Secondary is a softer accent (lavender).
	Secondary = lipgloss.NewStyle().Foreground(colorLavender)
	// Accent is the highlight colour (teal).
	Accent = lipgloss.NewStyle().Foreground(colorTeal)
	// Muted is the de-emphasised text colour (overlay).
	Muted = lipgloss.NewStyle().Foreground(colorOverlay)
	// Error is the error colour (red).
	Error = lipgloss.NewStyle().Foreground(colorRed)
	// Success is the success colour (green).
	Success = lipgloss.NewStyle().Foreground(colorGreen)
)

// ── Shared component styles ────────────────────────────────────────────────────

var (
	// Banner / heading
	styleBanner = lipgloss.NewStyle().
		Bold(true).
		Foreground(colorMauve).
		Padding(0, 1)

	// Generic key/value pair display
	styleKey = lipgloss.NewStyle().Foreground(colorOverlay).Width(16)
	styleVal = lipgloss.NewStyle().Foreground(colorLavender).Bold(true)

	// Common semantic utilities
	styleMuted    = lipgloss.NewStyle().Foreground(colorOverlay)
	styleStep     = lipgloss.NewStyle().Foreground(colorMauve).Bold(true)
	styleErr      = lipgloss.NewStyle().Foreground(colorRed)
	styleTeal     = lipgloss.NewStyle().Foreground(colorTeal).Bold(true)

	// PR list selection states
	styleSelected   = lipgloss.NewStyle().Foreground(colorMauve).Bold(true)
	styleUnselected = lipgloss.NewStyle().Foreground(colorSubtext)

	// Settings / help box borders
	styleRibbon = lipgloss.NewStyle().
		Foreground(colorSubtext).
		BorderStyle(lipgloss.NormalBorder()).
		BorderTop(true).
		BorderForeground(colorOverlay).
		Padding(0, 1)

	styleSettingsBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorMauve).
		Padding(1, 3)
)

// ── Monitor-specific styles ───────────────────────────────────────────────────

var (
	// Header
	styleHeader = lipgloss.NewStyle().
		Bold(true).
		Foreground(colorText).
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(colorOverlay).
		Padding(0, 1)

	styleHeaderLabel = lipgloss.NewStyle().Foreground(colorOverlay)
	styleHeaderVal   = lipgloss.NewStyle().Foreground(colorLavender).Bold(true)

	// Status bar
	styleStatusBar = lipgloss.NewStyle().
		Foreground(colorSubtext).
		BorderStyle(lipgloss.NormalBorder()).
		BorderTop(true).
		BorderForeground(colorOverlay).
		Padding(0, 1)

	// Phase indicators
	stylePhaseWaiting = lipgloss.NewStyle().Foreground(colorYellow).Bold(true)
	stylePhaseFixing  = lipgloss.NewStyle().Foreground(colorMauve).Bold(true)
	stylePhaseReflect = lipgloss.NewStyle().Foreground(colorTeal).Bold(true)
	stylePhaseDone    = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	stylePhaseErr     = lipgloss.NewStyle().Foreground(colorRed).Bold(true)

	// Log line semantic colours
	styleLogInfo    = lipgloss.NewStyle().Foreground(colorText)
	styleLogDebug   = lipgloss.NewStyle().Foreground(colorSubtext)
	styleLogWarn    = lipgloss.NewStyle().Foreground(colorYellow)
	styleLogErr     = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
	styleLogIter    = lipgloss.NewStyle().Foreground(colorMauve).Bold(true)
	styleLogAgent   = lipgloss.NewStyle().Foreground(colorText)
	styleLogSuccess = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	styleLogGit     = lipgloss.NewStyle().Foreground(colorPeach)
	styleLogAPI     = lipgloss.NewStyle().Foreground(colorSky)

	// Stat badges
	styleBadge        = lipgloss.NewStyle().Foreground(colorCrust).Padding(0, 1)
	styleBadgeIter    = styleBadge.Background(colorMauve)
	styleBadgeComment = styleBadge.Background(colorYellow)
	styleBadgeRules   = styleBadge.Background(colorTeal)
	styleBadgeTime    = styleBadge.Background(colorLavender)

	// Reflect panel
	styleReflectPanel = lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderLeft(true).
		BorderForeground(colorTeal).
		Padding(0, 1)
	styleReflectTitle = lipgloss.NewStyle().Foreground(colorTeal).Bold(true)
	styleReflectLine  = lipgloss.NewStyle().Foreground(colorSubtext)
	styleReflectNew   = lipgloss.NewStyle().Foreground(colorText)
	styleReflectOK    = lipgloss.NewStyle().Foreground(colorGreen)
	styleReflectFail  = lipgloss.NewStyle().Foreground(colorRed)

	// Iteration timeline
	styleTimelineDot     = lipgloss.NewStyle().Foreground(colorMauve)
	styleTimelineDone    = lipgloss.NewStyle().Foreground(colorGreen)
	styleTimelineErr     = lipgloss.NewStyle().Foreground(colorRed)
	styleTimelineCurrent = lipgloss.NewStyle().Foreground(colorYellow).Bold(true)

	// Toast notification
	styleToast = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorGreen).
		Padding(0, 2).
		Foreground(colorText).
		Bold(true)
)
