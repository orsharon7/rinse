package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ── Icons ─────────────────────────────────────────────────────────────────────

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
	IconDiag     = "╱"
)

// ── Splash logo ───────────────────────────────────────────────────────────────

const splashLogo = `
    ╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱
    ╱╱                                   ╱╱
    ╱╱    ◇  r  i  n  s  e              ╱╱
    ╱╱                                   ╱╱
    ╱╱    lather · rinse · repeat        ╱╱
    ╱╱                                   ╱╱
    ╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱`

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

// ── Brand bar ─────────────────────────────────────────────────────────────────
// renderBrandBar produces the consistent header shown on EVERY screen:
//   ◇ rinse  ╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱  v0.1.0
// The diagonals fill remaining width — the signature Crush-inspired look.

func renderBrandBar(w int) string {
	brand := styleBrandIcon.Render(IconDiamond) + " " + styleBrandName.Render("rinse")
	ver := styleBrandVersion.Render("v" + version)

	brandW := lipgloss.Width(brand)
	verW := lipgloss.Width(ver)
	diagSpace := w - brandW - verW - 4 // 4 = gaps
	if diagSpace < 2 {
		diagSpace = 2
	}
	diags := styleBrandDiag.Render(strings.Repeat(IconDiag, diagSpace))

	return brand + "  " + diags + "  " + ver
}

// renderBrandBarWithContext produces the header with contextual info:
//   ◇ rinse  ╱╱╱  owner/repo on branch  ╱╱╱  v0.1.0
func renderBrandBarWithContext(w int, ctx string) string {
	brand := styleBrandIcon.Render(IconDiamond) + " " + styleBrandName.Render("rinse")
	ver := styleBrandVersion.Render("v" + version)
	ctxRendered := ""
	if ctx != "" {
		ctxRendered = "  " + styleBrandCtx.Render(ctx) + "  "
	}

	brandW := lipgloss.Width(brand)
	verW := lipgloss.Width(ver)
	ctxW := lipgloss.Width(ctxRendered)

	totalFixed := brandW + verW + ctxW + 4
	diagSpace := w - totalFixed
	if diagSpace < 2 {
		diagSpace = 2
	}

	// Split diags: left side + context + right side
	leftDiags := diagSpace * 40 / 100
	rightDiags := diagSpace - leftDiags
	if leftDiags < 1 {
		leftDiags = 1
	}
	if rightDiags < 1 {
		rightDiags = 1
	}

	left := styleBrandDiag.Render(strings.Repeat(IconDiag, leftDiags))
	right := styleBrandDiag.Render(strings.Repeat(IconDiag, rightDiags))

	if ctx == "" {
		return brand + "  " + styleBrandDiag.Render(strings.Repeat(IconDiag, diagSpace)) + "  " + ver
	}
	return brand + "  " + left + ctxRendered + right + "  " + ver
}

// ── Wizard Styles ─────────────────────────────────────────────────────────────

var (
	// Brand bar components
	styleBrandIcon = lipgloss.NewStyle().
			Foreground(mauve).
			Bold(true)

	styleBrandName = lipgloss.NewStyle().
			Foreground(mauve).
			Bold(true)

	styleBrandDiag = lipgloss.NewStyle().
			Foreground(surface)

	styleBrandVersion = lipgloss.NewStyle().
				Foreground(overlay)

	styleBrandCtx = lipgloss.NewStyle().
			Foreground(subtext)

	// Logo styles (kept for backward compat in app.go helpers)
	styleLogo = styleBrandName

	styleLogoIcon = styleBrandIcon

	styleLogoSlash = lipgloss.NewStyle().
			Foreground(overlay)

	// Splash screen styles
	styleSplashBox = lipgloss.NewStyle().
			Foreground(mauve).
			Bold(true)

	styleSplashVersion = lipgloss.NewStyle().
				Foreground(overlay)

	styleSplashStatus = lipgloss.NewStyle().
				Foreground(subtext)

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

	// PR list: thick left bar for selected, like Crush's message focus border
	styleSelected   = lipgloss.NewStyle().Foreground(mauve).Bold(true)
	styleUnselected = lipgloss.NewStyle().Foreground(subtext)

	styleSelectedBar = lipgloss.NewStyle().
				Foreground(mauve).Bold(true)

	stylePRNum = lipgloss.NewStyle().
			Foreground(lavender).Bold(true)

	stylePRNumMuted = lipgloss.NewStyle().
				Foreground(overlay)

	// Settings ribbon
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
