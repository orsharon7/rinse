package theme

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

// ── Palette (RINSE brand accents + Catppuccin Macchiato base) ────────────────

var (
	Mauve    = lipgloss.Color("#8B5CF6") // brand Primary/Purple
	Lavender = lipgloss.Color("#B7BDF8")
	Teal     = lipgloss.Color("#8BD5CA")
	Green    = lipgloss.Color("#10B981") // brand Success/Green
	Red      = lipgloss.Color("#EF4444") // brand Error/Red
	Yellow   = lipgloss.Color("#F59E0B") // brand Warning/Yellow
	Peach    = lipgloss.Color("#F5A97F")
	Sky      = lipgloss.Color("#91D7E3")
	Text     = lipgloss.Color("#CAD3F5")
	Subtext  = lipgloss.Color("#A5ADCB")
	Overlay  = lipgloss.Color("#6E738D")
	Surface  = lipgloss.Color("#363A4F")
	Crust    = lipgloss.Color("#181926")
)

// ── Utility helpers ───────────────────────────────────────────────────────────

// Truncate truncates s to at most n visible runes, appending an ellipsis when
// truncation occurs.
func Truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 0 {
		return ""
	}
	if n == 1 {
		return "…"
	}
	return string(runes[:n-1]) + "…"
}

// Clamp returns v clamped to [lo, hi].
func Clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// WrapLine splits s into lines of at most w visible runes, breaking at spaces.
func WrapLine(s string, w int) []string {
	if w <= 0 {
		return []string{s}
	}
	runes := []rune(s)
	var lines []string
	for len(runes) > 0 {
		if len(runes) <= w {
			lines = append(lines, string(runes))
			break
		}
		cut := w
		for cut > w-12 && cut > 0 && runes[cut-1] != ' ' {
			cut--
		}
		if cut <= 0 {
			cut = w
		}
		lines = append(lines, strings.TrimRight(string(runes[:cut]), " "))
		runes = runes[cut:]
		for len(runes) > 0 && runes[0] == ' ' {
			runes = runes[1:]
		}
	}
	return lines
}

// FormatPatternLabel converts a snake_case pattern label (as stored/returned
// by the classifier) into a human-readable display string.
// e.g. "nil_check" → "nil check", "error_handling" → "error handling".
// Non-snake_case strings are returned unchanged.
func FormatPatternLabel(label string) string {
	return strings.ReplaceAll(label, "_", " ")
}

// RenderKeyHint renders a "key action" pair in the standard hint style.
func RenderKeyHint(keyStr, desc string) string {
	return StyleHintKey.Render(keyStr) + " " + StyleHintDesc.Render(desc)
}

// ── Brand styles ──────────────────────────────────────────────────────────────

var (
	StyleCharm   = lipgloss.NewStyle().Foreground(Teal)
	StyleVersion = lipgloss.NewStyle().Foreground(Overlay)
	StyleDiag    = lipgloss.NewStyle().Foreground(Surface)

	StyleHeaderDetail = lipgloss.NewStyle().Foreground(Subtext)
)

// ── Wizard Styles ─────────────────────────────────────────────────────────────

var (
	StyleSplashStatus = lipgloss.NewStyle().Foreground(Subtext)

	// Reusable text presets.
	StyleKey   = lipgloss.NewStyle().Foreground(Overlay).Width(16)
	StyleVal   = lipgloss.NewStyle().Foreground(Lavender).Bold(true)
	StyleMuted = lipgloss.NewStyle().Foreground(Overlay)
	StyleStep  = lipgloss.NewStyle().Foreground(Mauve).Bold(true)
	StyleErr   = lipgloss.NewStyle().Foreground(Red)
	StyleTeal  = lipgloss.NewStyle().Foreground(Teal).Bold(true)

	// PR list: thick left bar for selected.
	StyleSelected    = lipgloss.NewStyle().Foreground(Mauve).Bold(true)
	StyleUnselected  = lipgloss.NewStyle().Foreground(Subtext)
	StyleSelectedBar = lipgloss.NewStyle().Foreground(Mauve).Bold(true)

	StylePRNum      = lipgloss.NewStyle().Foreground(Lavender).Bold(true)
	StylePRNumMuted = lipgloss.NewStyle().Foreground(Overlay)

	// Settings ribbon.
	StyleRibbon = lipgloss.NewStyle().
			Foreground(Subtext).
			BorderStyle(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(Surface).
			Padding(0, 1)

	StyleSettingsBox = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(Mauve).
				Padding(1, 3)

	// Key hints.
	StyleHintKey  = lipgloss.NewStyle().Foreground(Subtext)
	StyleHintDesc = lipgloss.NewStyle().Foreground(Overlay)
)

// ── Monitor Styles ────────────────────────────────────────────────────────────

var (
	StyleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(Text).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(Surface).
			Padding(0, 1)

	StyleHeaderLabel = lipgloss.NewStyle().Foreground(Overlay)
	StyleHeaderVal   = lipgloss.NewStyle().Foreground(Lavender).Bold(true)

	StyleStatusBar = lipgloss.NewStyle().
			Foreground(Subtext).
			BorderStyle(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(Surface).
			Padding(0, 1)

	StylePhaseWaiting   = lipgloss.NewStyle().Foreground(Yellow).Bold(true)
	StylePhaseFixing    = lipgloss.NewStyle().Foreground(Mauve).Bold(true)
	StylePhaseReflect   = lipgloss.NewStyle().Foreground(Teal).Bold(true)
	StylePhaseDone      = lipgloss.NewStyle().Foreground(Green).Bold(true)
	StylePhaseErr       = lipgloss.NewStyle().Foreground(Red).Bold(true)
	StylePhaseStalled   = lipgloss.NewStyle().Foreground(Peach).Bold(true)   // amber/peach — stalled Copilot review
	StylePhaseCancelled = lipgloss.NewStyle().Foreground(Overlay).Bold(true) // silver/muted — cancelled cycle

	StyleLogInfo    = lipgloss.NewStyle().Foreground(Text)
	StyleLogDebug   = lipgloss.NewStyle().Foreground(Subtext)
	StyleLogWarn    = lipgloss.NewStyle().Foreground(Yellow)
	StyleLogErr     = lipgloss.NewStyle().Foreground(Red).Bold(true)
	StyleLogIter    = lipgloss.NewStyle().Foreground(Mauve).Bold(true)
	StyleLogAgent   = lipgloss.NewStyle().Foreground(Text)
	StyleLogSuccess = lipgloss.NewStyle().Foreground(Green).Bold(true)
	StyleLogGit     = lipgloss.NewStyle().Foreground(Peach)
	StyleLogAPI     = lipgloss.NewStyle().Foreground(Sky)

	StyleBadge        = lipgloss.NewStyle().Foreground(Crust).Padding(0, 1)
	StyleBadgeIter    = StyleBadge.Background(Mauve)
	StyleBadgeComment = StyleBadge.Background(Yellow)
	StyleBadgeRules   = StyleBadge.Background(Teal)
	StyleBadgeTime    = StyleBadge.Background(Lavender)
	StyleBadgeETA     = StyleBadge.Background(Sky)
	StyleBadgeOverdue = StyleBadge.Background(Red)

	// Timing-specific text styles (maps to UX design tokens).
	StyleOverdue       = lipgloss.NewStyle().Foreground(Red).Bold(true)   // --color-status-error
	StyleETAWarning    = lipgloss.NewStyle().Foreground(Yellow).Bold(true) // --color-status-warning
	StyleElapsedFrozen = lipgloss.NewStyle().Foreground(Subtext)          // --text-secondary (paused/frozen)
	StyleElapsedDimmed = lipgloss.NewStyle().Foreground(Overlay)          // --text-dimmed (cancelled)

	// Status badge styles — used by renderStatusBadge in the monitor.
	StyleBadgeQueued    = StyleBadge.Background(Sky)
	StyleBadgeRunning   = StyleBadge.Background(Mauve)
	StyleBadgeStalled   = StyleBadge.Background(Yellow)
	StyleBadgeCancelled = StyleBadge.Background(Overlay)
	StyleBadgeCompleted = StyleBadge.Background(Green)
	StyleBadgeFailed    = StyleBadge.Background(Red)

	StyleReflectPanel = lipgloss.NewStyle().
				BorderStyle(lipgloss.NormalBorder()).
				BorderLeft(true).
				BorderForeground(Teal).
				Padding(0, 1)
	StyleReflectTitle = lipgloss.NewStyle().Foreground(Teal).Bold(true)
	StyleReflectLine  = lipgloss.NewStyle().Foreground(Subtext)
	StyleReflectNew   = lipgloss.NewStyle().Foreground(Text)
	StyleReflectOK    = lipgloss.NewStyle().Foreground(Green)
	StyleReflectFail  = lipgloss.NewStyle().Foreground(Red)

	StyleTimelineDot     = lipgloss.NewStyle().Foreground(Mauve)
	StyleTimelineDone    = lipgloss.NewStyle().Foreground(Green)
	StyleTimelineErr     = lipgloss.NewStyle().Foreground(Red)
	StyleTimelineCurrent = lipgloss.NewStyle().Foreground(Yellow).Bold(true)

	StyleToast = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(Green).
			Padding(0, 2).
			Foreground(Text).
			Bold(true)

	StyleMenuBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(Teal).
			Padding(1, 4)
)

// ── Persistent header / footer styles ─────────────────────────────────────────

var (
	// StyleAppHeader — top bar (single content row with horizontal padding).
	StyleAppHeader = lipgloss.NewStyle().
			Foreground(Text).
			Padding(0, 1)

	// StyleAppFooter — bottom bar (single content row).
	StyleAppFooter = lipgloss.NewStyle().
			Foreground(Subtext).
			Padding(0, 1)

	StyleFooterStatus    = lipgloss.NewStyle().Foreground(Green)
	StyleFooterStatusErr = lipgloss.NewStyle().Foreground(Red)
	StyleFooterMuted     = lipgloss.NewStyle().Foreground(Overlay)
	StyleFooterHint      = lipgloss.NewStyle().Foreground(Overlay)
)

// ── Gradient rendering ────────────────────────────────────────────────────────

// GradientString renders s with a per-character foreground blend from colorA to colorB.
func GradientString(s string, colorA, colorB lipgloss.Color, bold bool) string {
	runes := []rune(s)
	n := len(runes)
	if n == 0 {
		return ""
	}

	rA, gA, bA := HexToRGB(string(colorA))
	rB, gB, bB := HexToRGB(string(colorB))

	var sb strings.Builder
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
		sb.WriteString(style.Render(string(r)))
	}
	return sb.String()
}

// HexToRGB parses a "#RRGGBB" hex string into component bytes.
func HexToRGB(hex string) (uint8, uint8, uint8) {
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

// ── Block-character wordmark ──────────────────────────────────────────────────
// Built with ▄▀█ half-block characters — same approach as charmbracelet/crush.
//
// The logo is 3 rows tall:
//
//	▄▀▀▀▄ ▀█▀ ▄▀▀▄ ▄▀▀▀ ▄▀▀▀▄
//	█▀▀▄  █  █  █ ▀▀▀▄ █▀▀▀
//	▀   ▀ ▀▀▀ ▀  ▀ ▀▀▀  ▀▀▀▀
var logoLines = [3]string{
	"▄▀▀▀▄ ▀█▀ ▄▀▀▄ ▄▀▀▀ ▄▀▀▀▄",
	"█▀▀▄   █  █  █ ▀▀▀▄ █▀▀▀ ",
	"▀   ▀ ▀▀▀ ▀  ▀ ▀▀▀  ▀▀▀▀ ",
}

// RenderWordmark renders the big 3-row RINSE logo with a gradient from Mauve
// to Lavender, surrounded by diagonal field lines. version is the version string
// displayed beneath the wordmark.
func RenderWordmark(width int, version string) string {
	logoW := 0
	for _, line := range logoLines {
		logoW = max(logoW, lipgloss.Width(line))
	}
	fieldW := 6

	if width < logoW+fieldW*2+4 {
		// Narrow terminal — use compact one-line brand.
		return RenderCompactBrand(width)
	}

	rightW := max(4, width-logoW-fieldW-3)

	var rows []string
	for _, line := range logoLines {
		leftField := StyleDiag.Render(strings.Repeat(IconDiag, fieldW))
		rightField := StyleDiag.Render(strings.Repeat(IconDiag, rightW))
		grad := GradientString(line, Mauve, Lavender, true)
		rows = append(rows, leftField+" "+grad+" "+rightField)
	}

	// Version + tagline below the logo.
	meta := StyleCharm.Render("rinse™") +
		strings.Repeat(" ", max(1, logoW-lipgloss.Width("rinse™")-lipgloss.Width(version))) +
		StyleVersion.Render(version)

	return strings.Join(rows, "\n") + "\n" + strings.Repeat(" ", fieldW+1) + meta
}

// RenderCompactBrand renders the one-line header used on narrow terminals:
//
//	rinse™ RINSE ╱╱╱╱╱╱╱╱╱
func RenderCompactBrand(width int) string {
	brand := StyleCharm.Render("rinse™") + " " +
		GradientString("RINSE", Mauve, Lavender, true) + " "

	brandW := lipgloss.Width(brand)
	if width < brandW {
		return StyleCharm.Render("rinse™")
	}
	remainingW := width - brandW
	return brand + StyleDiag.Render(strings.Repeat(IconDiag, remainingW))
}

// RenderCompactBrandWithDetails renders the compact header with contextual details:
//
//	rinse™ RINSE ╱╱╱╱╱╱ owner/repo • main ╱╱╱╱
func RenderCompactBrandWithDetails(width int, details string) string {
	brand := StyleCharm.Render("rinse™") + " " +
		GradientString("RINSE", Mauve, Lavender, true) + " "

	brandW := lipgloss.Width(brand)

	if details == "" {
		remainingW := max(0, width-brandW)
		return brand + StyleDiag.Render(strings.Repeat(IconDiag, remainingW))
	}

	maxDetailsW := width - brandW - 2
	if maxDetailsW < 0 {
		maxDetailsW = 0
	}
	truncatedDetails := Truncate(details, maxDetailsW)
	detailsRendered := StyleHeaderDetail.Render(truncatedDetails)
	detailsW := lipgloss.Width(detailsRendered)

	totalFixed := brandW + detailsW + 2 // 2 = spaces around details
	diagSpace := max(0, width-totalFixed)

	leftDiags := diagSpace * 40 / 100
	rightDiags := diagSpace - leftDiags

	return brand +
		StyleDiag.Render(strings.Repeat(IconDiag, leftDiags)) +
		" " + detailsRendered + " " +
		StyleDiag.Render(strings.Repeat(IconDiag, rightDiags))
}
