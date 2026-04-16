package main

import (
	"fmt"
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
	IconSep      = "•"
	IconDiag     = "╱"
)

// ── Block-character wordmark ──────────────────────────────────────────────────
// Built with ▄▀█ half-block characters — same approach as charmbracelet/crush.
//
// The logo is 3 rows tall:
//   ▄▀▀▀▄ ▀█▀ ▄▀▀▄ ▄▀▀▀ ▄▀▀▀▄
//   █▀▀▄  █  █  █ ▀▀▀▄ █▀▀▀
//   ▀   ▀ ▀▀▀ ▀  ▀ ▀▀▀  ▀▀▀▀

var logoLines = [3]string{
	"▄▀▀▀▄ ▀█▀ ▄▀▀▄ ▄▀▀▀ ▄▀▀▀▄",
	"█▀▀▄   █  █  █ ▀▀▀▄ █▀▀▀ ",
	"▀   ▀ ▀▀▀ ▀  ▀ ▀▀▀  ▀▀▀▀ ",
}

// ── Palette (Catppuccin Macchiato) ────────────────────────────────────────────

var (
	mauve    = lipgloss.Color("#C6A0F6")
	lavender = lipgloss.Color("#B7BDF8")
	teal     = lipgloss.Color("#8BD5CA")
	green    = lipgloss.Color("#A6DA95")
	red      = lipgloss.Color("#ED8796")
	yellow   = lipgloss.Color("#EED49F")
	peach    = lipgloss.Color("#F5A97F")
	sky      = lipgloss.Color("#91D7E3")
	text     = lipgloss.Color("#CAD3F5")
	subtext  = lipgloss.Color("#A5ADCB")
	overlay  = lipgloss.Color("#6E738D")
	surface  = lipgloss.Color("#363A4F")
	crust    = lipgloss.Color("#181926")
)

// ── Gradient rendering ────────────────────────────────────────────────────────
// Per-character foreground blend from colorA → colorB. Simplified version of
// what charmbracelet/crush does in styles/grad.go.

func gradientString(s string, colorA, colorB lipgloss.Color, bold bool) string {
	runes := []rune(s)
	n := len(runes)
	if n == 0 {
		return ""
	}

	// Parse hex colors for blending.
	rA, gA, bA := hexToRGB(string(colorA))
	rB, gB, bB := hexToRGB(string(colorB))

	var b strings.Builder
	for i, r := range runes {
		t := float64(i) / float64(max(1, n-1))
		ri := uint8(float64(rA)*(1-t) + float64(rB)*t)
		gi := uint8(float64(gA)*(1-t) + float64(gB)*t)
		bi := uint8(float64(bA)*(1-t) + float64(bB)*t)
		c := lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", ri, gi, bi))
		style := lipgloss.NewStyle().Foreground(c)
		if bold {
			style = style.Bold(true)
		}
		b.WriteString(style.Render(string(r)))
	}
	return b.String()
}

func hexToRGB(hex string) (uint8, uint8, uint8) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 200, 200, 200
	}
	var r, g, b uint8
	n, err := fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b)
	if err != nil || n != 3 {
		return 200, 200, 200
	}
	return r, g, b
}

// ── Logo rendering ────────────────────────────────────────────────────────────

// renderWordmark renders the big 3-row RINSE logo with a gradient from mauve
// to lavender, surrounded by diagonal field lines — exactly like Crush's logo.
func renderWordmark(width int) string {
	logoW := 0
	for _, line := range logoLines {
		logoW = max(logoW, lipgloss.Width(line))
	}
	fieldW := 6

	if width < logoW+fieldW*2+4 {
		// Narrow terminal — use compact one-line brand.
		return renderCompactBrand(width)
	}

	rightW := max(4, width-logoW-fieldW-3)

	var rows []string
	for _, line := range logoLines {
		leftField := styleDiag.Render(strings.Repeat(IconDiag, fieldW))
		rightField := styleDiag.Render(strings.Repeat(IconDiag, rightW))
		grad := gradientString(line, mauve, lavender, true)
		rows = append(rows, leftField+" "+grad+" "+rightField)
	}

	// Version + tagline below the logo
	meta := styleCharm.Render("rinse™") +
		strings.Repeat(" ", max(1, logoW-lipgloss.Width("rinse™")-lipgloss.Width(version))) +
		styleVersion.Render(version)

	return strings.Join(rows, "\n") + "\n" + strings.Repeat(" ", fieldW+1) + meta
}

// renderCompactBrand renders the one-line header used on all non-splash screens:
//
//	rinse™ RINSE ╱╱╱╱╱╱╱╱╱ ~/dir • ctrl+d ╱╱╱╱
func renderCompactBrand(width int) string {
	brand := styleCharm.Render("rinse™") + " " +
		gradientString("RINSE", mauve, lavender, true) + " "

	brandW := lipgloss.Width(brand)
	if width < brandW {
		// Terminal too narrow to fit the full brand — return the shortest fallback.
		return styleCharm.Render("rinse™")
	}
	remainingW := width - brandW
	return brand + styleDiag.Render(strings.Repeat(IconDiag, remainingW))
}

// renderCompactBrandWithDetails renders the compact header with contextual details:
//
//	rinse™ RINSE ╱╱╱╱╱╱ owner/repo • main ╱╱╱╱
func renderCompactBrandWithDetails(width int, details string) string {
	brand := styleCharm.Render("rinse™") + " " +
		gradientString("RINSE", mauve, lavender, true) + " "

	brandW := lipgloss.Width(brand)

	if details == "" {
		remainingW := max(0, width-brandW)
		return brand + styleDiag.Render(strings.Repeat(IconDiag, remainingW))
	}

	// Truncate details so the total rendered width never exceeds width.
	// Reserve brandW + 2 spaces around details; the rest is diagonal fill.
	maxDetailsW := width - brandW - 2
	if maxDetailsW < 0 {
		maxDetailsW = 0
	}
	truncatedDetails := truncate(details, maxDetailsW)
	detailsRendered := styleHeaderDetail.Render(truncatedDetails)
	detailsW := lipgloss.Width(detailsRendered)

	totalFixed := brandW + detailsW + 2 // 2 = spaces around details
	diagSpace := max(0, width-totalFixed)

	leftDiags := diagSpace * 40 / 100
	rightDiags := diagSpace - leftDiags

	return brand +
		styleDiag.Render(strings.Repeat(IconDiag, leftDiags)) +
		" " + detailsRendered + " " +
		styleDiag.Render(strings.Repeat(IconDiag, rightDiags))
}

// ── Brand styles ──────────────────────────────────────────────────────────────

var (
	styleCharm   = lipgloss.NewStyle().Foreground(teal)
	styleVersion = lipgloss.NewStyle().Foreground(overlay)
	styleDiag    = lipgloss.NewStyle().Foreground(surface)

	styleHeaderDetail = lipgloss.NewStyle().Foreground(subtext)
)

// ── Wizard Styles ─────────────────────────────────────────────────────────────

var (
	styleSplashStatus = lipgloss.NewStyle().Foreground(subtext)

	// Reusable text presets
	styleKey   = lipgloss.NewStyle().Foreground(overlay).Width(16)
	styleVal   = lipgloss.NewStyle().Foreground(lavender).Bold(true)
	styleMuted = lipgloss.NewStyle().Foreground(overlay)
	styleStep  = lipgloss.NewStyle().Foreground(mauve).Bold(true)
	styleErr   = lipgloss.NewStyle().Foreground(red)
	styleTeal  = lipgloss.NewStyle().Foreground(teal).Bold(true)

	// PR list: thick left bar for selected
	styleSelected    = lipgloss.NewStyle().Foreground(mauve).Bold(true)
	styleUnselected  = lipgloss.NewStyle().Foreground(subtext)
	styleSelectedBar = lipgloss.NewStyle().Foreground(mauve).Bold(true)

	stylePRNum      = lipgloss.NewStyle().Foreground(lavender).Bold(true)
	stylePRNumMuted = lipgloss.NewStyle().Foreground(overlay)

	// Settings ribbon
	styleRibbon = lipgloss.NewStyle().
			Foreground(subtext).
			BorderStyle(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(surface).
			Padding(0, 1)

	styleSettingsBox = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(mauve).
				Padding(1, 3)

	// Key hints
	styleHintKey  = lipgloss.NewStyle().Foreground(subtext)
	styleHintDesc = lipgloss.NewStyle().Foreground(overlay)
)

// ── Monitor Styles ────────────────────────────────────────────────────────────

var (
	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(text).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(surface).
			Padding(0, 1)

	styleHeaderLabel = lipgloss.NewStyle().Foreground(overlay)
	styleHeaderVal   = lipgloss.NewStyle().Foreground(lavender).Bold(true)

	styleStatusBar = lipgloss.NewStyle().
			Foreground(subtext).
			BorderStyle(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(surface).
			Padding(0, 1)

	stylePhaseWaiting = lipgloss.NewStyle().Foreground(yellow).Bold(true)
	stylePhaseFixing  = lipgloss.NewStyle().Foreground(mauve).Bold(true)
	stylePhaseReflect = lipgloss.NewStyle().Foreground(teal).Bold(true)
	stylePhaseDone    = lipgloss.NewStyle().Foreground(green).Bold(true)
	stylePhaseErr     = lipgloss.NewStyle().Foreground(red).Bold(true)

	styleLogInfo    = lipgloss.NewStyle().Foreground(text)
	styleLogDebug   = lipgloss.NewStyle().Foreground(subtext)
	styleLogWarn    = lipgloss.NewStyle().Foreground(yellow)
	styleLogErr     = lipgloss.NewStyle().Foreground(red).Bold(true)
	styleLogIter    = lipgloss.NewStyle().Foreground(mauve).Bold(true)
	styleLogAgent   = lipgloss.NewStyle().Foreground(text)
	styleLogSuccess = lipgloss.NewStyle().Foreground(green).Bold(true)
	styleLogGit     = lipgloss.NewStyle().Foreground(peach)
	styleLogAPI     = lipgloss.NewStyle().Foreground(sky)

	styleBadge        = lipgloss.NewStyle().Foreground(crust).Padding(0, 1)
	styleBadgeIter    = styleBadge.Background(mauve)
	styleBadgeComment = styleBadge.Background(yellow)
	styleBadgeRules   = styleBadge.Background(teal)
	styleBadgeTime    = styleBadge.Background(lavender)

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

	styleTimelineDot     = lipgloss.NewStyle().Foreground(mauve)
	styleTimelineDone    = lipgloss.NewStyle().Foreground(green)
	styleTimelineErr     = lipgloss.NewStyle().Foreground(red)
	styleTimelineCurrent = lipgloss.NewStyle().Foreground(yellow).Bold(true)

	styleToast = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(green).
			Padding(0, 2).
			Foreground(text).
			Bold(true)

	styleMenuBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(teal).
			Padding(1, 4)
)

// ── Persistent header / footer styles ─────────────────────────────────────────

var (
	// styleAppHeader — top bar (content row + bottom border); no explicit bg so
	// it reads against the terminal background, consistent with the rest of the
	// Catppuccin palette.
	styleAppHeader = lipgloss.NewStyle().
			Foreground(text).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(surface).
			Padding(0, 1)

	// styleAppFooter — bottom bar (top border + content row).
	styleAppFooter = lipgloss.NewStyle().
			Foreground(subtext).
			BorderStyle(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(surface).
			Padding(0, 1)

	styleFooterStatus    = lipgloss.NewStyle().Foreground(green)
	styleFooterStatusErr = lipgloss.NewStyle().Foreground(red)
	styleFooterMuted     = lipgloss.NewStyle().Foreground(overlay)
	styleFooterHint      = lipgloss.NewStyle().Foreground(overlay)
)
