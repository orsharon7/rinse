package tui

import (
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/orsharon7/rinse/internal/theme"
)

// ── KeyMap ────────────────────────────────────────────────────────────────────

// KeyMap holds all keybindings used across RINSE views. All key matching MUST
// go through this struct — no raw string comparisons in Update handlers.
type KeyMap struct {
	Up        key.Binding
	Down      key.Binding
	Top       key.Binding
	Bottom    key.Binding
	Confirm   key.Binding
	Back      key.Binding
	CloseHelp key.Binding
	Quit      key.Binding
	ForceQuit key.Binding
	Refresh   key.Binding
	Help      key.Binding
	Filter    key.Binding
	// Wizard-specific.
	Settings key.Binding
	ManualPR key.Binding
	// Settings view navigation.
	Left   key.Binding
	Right  key.Binding
	Tab    key.Binding
	Toggle key.Binding
}

// Keys is the global singleton KeyMap used by all views.
var Keys = KeyMap{
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "down"),
	),
	Top: key.NewBinding(
		key.WithKeys("g", "home"),
		key.WithHelp("g/Home", "jump to top"),
	),
	Bottom: key.NewBinding(
		key.WithKeys("G", "end"),
		key.WithHelp("G/End", "jump to bottom"),
	),
	Confirm: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "select"),
	),
	Back: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "back"),
	),
	CloseHelp: key.NewBinding(
		key.WithKeys("esc", "q"),
		key.WithHelp("esc/q", "close help"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q/ctrl+c", "quit"),
	),
	ForceQuit: key.NewBinding(
		key.WithKeys("ctrl+c"),
		key.WithHelp("ctrl+c", "quit"),
	),
	Refresh: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "refresh"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "toggle help"),
	),
	Filter: key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "filter (coming soon)"),
	),
	Settings: key.NewBinding(
		key.WithKeys("s"),
		key.WithHelp("s", "settings"),
	),
	ManualPR: key.NewBinding(
		key.WithKeys("#"),
		key.WithHelp("#", "enter PR number"),
	),
	Left: key.NewBinding(
		key.WithKeys("left", "h"),
		key.WithHelp("←/h", "previous"),
	),
	Right: key.NewBinding(
		key.WithKeys("right", "l"),
		key.WithHelp("→/l", "next"),
	),
	Tab: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "next field"),
	),
	Toggle: key.NewBinding(
		key.WithKeys(" "),
		key.WithHelp("space", "toggle"),
	),
}

// ShortHelp returns the short keybinding list for the compact help bar.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Refresh, k.Settings, k.Help, k.Quit}
}

// FullHelp returns all keybindings for the expanded help overlay.
// NOTE: when the overlay is open, 'q' closes the overlay (CloseHelp) rather
// than quitting. We show CloseHelp + ForceQuit so displayed bindings always
// match the active behaviour in that context.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Top, k.Bottom},
		{k.Confirm, k.Settings, k.ManualPR, k.Refresh},
		{k.Help, k.CloseHelp, k.ForceQuit},
	}
}

// ── Global help model ─────────────────────────────────────────────────────────

// newHelpModel returns a freshly configured bubbles/help model.
func newHelpModel() help.Model {
	h := help.New()
	h.Styles.ShortKey = theme.StyleHintKey
	h.Styles.ShortDesc = theme.StyleHintDesc
	h.Styles.ShortSeparator = theme.StyleHintDesc
	h.Styles.FullKey = theme.StyleHintKey
	h.Styles.FullDesc = theme.StyleHintDesc
	h.Styles.FullSeparator = theme.StyleHintDesc
	return h
}
