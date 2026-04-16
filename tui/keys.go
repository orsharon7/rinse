package main

import (
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
)

// ── KeyMap ────────────────────────────────────────────────────────────────────

// KeyMap holds all keybindings used across RINSE views. All key matching MUST
// go through this struct — no raw string comparisons in Update handlers.
type KeyMap struct {
	Up      key.Binding
	Down    key.Binding
	Top     key.Binding
	Bottom  key.Binding
	Confirm key.Binding
	Back    key.Binding
	Quit    key.Binding
	Refresh key.Binding
	Help    key.Binding
	Filter  key.Binding
	// Wizard-specific
	Settings  key.Binding
	ManualPR  key.Binding
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
		key.WithKeys("esc", "q"),
		key.WithHelp("esc/q", "back/quit"),
	),
	Quit: key.NewBinding(
		key.WithKeys("ctrl+c", "q"),
		key.WithHelp("q/ctrl+c", "quit"),
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
}

// ShortHelp returns the short keybinding list for the compact help bar.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Refresh, k.Settings, k.Help, k.Back}
}

// FullHelp returns all keybindings for the expanded help overlay.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Top, k.Bottom},
		{k.Confirm, k.Settings, k.ManualPR, k.Refresh},
		{k.Filter, k.Help, k.Back},
	}
}

// ── Global help model ─────────────────────────────────────────────────────────

// newHelpModel returns a freshly configured bubbles/help model.
func newHelpModel() help.Model {
	h := help.New()
	h.Styles.ShortKey = styleHintKey
	h.Styles.ShortDesc = styleHintDesc
	h.Styles.ShortSeparator = styleHintDesc
	h.Styles.FullKey = styleHintKey
	h.Styles.FullDesc = styleHintDesc
	h.Styles.FullSeparator = styleHintDesc
	return h
}
