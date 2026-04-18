package tui

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/orsharon7/rinse/internal/session"
	"github.com/orsharon7/rinse/internal/theme"
)

// ── Sort modes ────────────────────────────────────────────────────────────────

type historySortMode int

const (
	sortNewest historySortMode = iota
	sortOldest
	sortMostComments
	sortMostTimeSaved
)

func (s historySortMode) String() string {
	switch s {
	case sortNewest:
		return "newest first"
	case sortOldest:
		return "oldest first"
	case sortMostComments:
		return "most comments"
	case sortMostTimeSaved:
		return "most time saved"
	}
	return ""
}

// ── Filter outcome ────────────────────────────────────────────────────────────

type outcomeFilter int

const (
	filterAll outcomeFilter = iota
	filterApproved
	filterUnresolved
	filterError
)

func (o outcomeFilter) String() string {
	switch o {
	case filterAll:
		return "All"
	case filterApproved:
		return "Approved"
	case filterUnresolved:
		return "Unresolved"
	case filterError:
		return "Error"
	}
	return ""
}

// ── Screen state ──────────────────────────────────────────────────────────────

type historyScreen int

const (
	screenList   historyScreen = iota
	screenDetail               // viewing a single session
	screenFilter               // filter panel overlay
)

// ── History model ─────────────────────────────────────────────────────────────

type historyModel struct {
	// data
	allSessions      []session.Session
	filteredSessions []session.Session

	// list screen state
	cursor   int
	sortMode historySortMode

	// filter state
	filterText    string
	filterOutcome outcomeFilter
	filterTyping  bool // whether we're in text-input mode in the filter panel

	// detail screen state
	detailSession session.Session
	detailCmd     string // last rinse run --pr <N> command shown

	// layout
	width  int
	height int

	// current screen
	screen historyScreen
}

func newHistoryModel(sessions []session.Session) historyModel {
	m := historyModel{
		allSessions: sessions,
		sortMode:    sortNewest,
	}
	return m.applyFilterOp()
}

// ── Filter & sort ─────────────────────────────────────────────────────────────

func (m historyModel) applyFilterOp() historyModel {
	var out []session.Session
	for _, s := range m.allSessions {
		// outcome filter
		switch m.filterOutcome {
		case filterApproved:
			if !s.Approved {
				continue
			}
		case filterUnresolved:
			if s.Approved || s.Iterations == 0 {
				continue
			}
		case filterError:
			if s.Approved || s.Iterations > 0 {
				continue
			}
		}
		// text filter on repo
		if m.filterText != "" {
			if !strings.Contains(strings.ToLower(s.Repo), strings.ToLower(m.filterText)) {
				continue
			}
		}
		out = append(out, s)
	}

	// sort
	switch m.sortMode {
	case sortNewest:
		sort.Slice(out, func(i, j int) bool {
			return out[i].StartedAt.After(out[j].StartedAt)
		})
	case sortOldest:
		sort.Slice(out, func(i, j int) bool {
			return out[i].StartedAt.Before(out[j].StartedAt)
		})
	case sortMostComments:
		sort.Slice(out, func(i, j int) bool {
			return out[i].TotalComments > out[j].TotalComments
		})
	case sortMostTimeSaved:
		sort.Slice(out, func(i, j int) bool {
			return out[i].TimeSaved() > out[j].TimeSaved()
		})
	}
	m.filteredSessions = out
	// clamp cursor
	if m.cursor >= len(m.filteredSessions) {
		m.cursor = max(0, len(m.filteredSessions)-1)
	}
	return m
}

// ── Init ──────────────────────────────────────────────────────────────────────

func (m historyModel) Init() tea.Cmd {
	return nil
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m historyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		switch m.screen {
		case screenList:
			return m.updateList(msg)
		case screenDetail:
			return m.updateDetail(msg)
		case screenFilter:
			return m.updateFilter(msg)
		}
	}
	return m, nil
}

func (m historyModel) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(m.filteredSessions)

	switch {
	case key.Matches(msg, Keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, Keys.Up):
		if m.cursor > 0 {
			m.cursor--
		}

	case key.Matches(msg, Keys.Down):
		if m.cursor < n-1 {
			m.cursor++
		}

	case key.Matches(msg, Keys.Confirm):
		if n > 0 {
			m.detailSession = m.filteredSessions[m.cursor]
			m.detailCmd = ""
			m.screen = screenDetail
		}

	case msg.String() == "f":
		m.screen = screenFilter
		m.filterTyping = false

	case msg.String() == "r":
		if n > 0 {
			s := m.filteredSessions[m.cursor]
			m.detailSession = s
			m.detailCmd = fmt.Sprintf("rinse run --pr %s", s.PR)
			m.screen = screenDetail
		}

	case msg.String() == "s":
		m.sortMode = (m.sortMode + 1) % 4
		m = m.applyFilterOp()
	}

	return m, nil
}

func (m historyModel) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, Keys.Back) || msg.String() == "q":
		m.screen = screenList

	case msg.String() == "o":
		s := m.detailSession
		cmd := exec.Command("gh", "pr", "view", s.PR, "--repo", s.Repo, "--web")
		_ = cmd.Start()

	case msg.String() == "r":
		m.detailCmd = fmt.Sprintf("rinse run --pr %s", m.detailSession.PR)
	}
	return m, nil
}

func (m historyModel) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.filterTyping {
		switch msg.String() {
		case "enter", "esc":
			m.filterTyping = false
			m = m.applyFilterOp()
		case "backspace":
			if len(m.filterText) > 0 {
				m.filterText = m.filterText[:len(m.filterText)-1]
			}
		default:
			if len(msg.Runes) > 0 {
				m.filterText += string(msg.Runes)
			}
		}
		return m, nil
	}

	switch msg.String() {
	case "q", "esc", "f":
		m.screen = screenList
		m = m.applyFilterOp()

	case "r":
		m.filterText = ""
		m.filterTyping = true

	case "tab", "o":
		m.filterOutcome = (m.filterOutcome + 1) % 4
		m = m.applyFilterOp()
	}
	return m, nil
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m historyModel) View() string {
	switch m.screen {
	case screenDetail:
		return m.viewDetail()
	case screenFilter:
		return m.viewList() + "\n" + m.viewFilterPanel()
	default:
		return m.viewList()
	}
}

// ── Status icon helpers ────────────────────────────────────────────────────────

func sessionIcon(s session.Session) string {
	if s.Approved {
		return lipgloss.NewStyle().Foreground(theme.Green).Render("✓")
	}
	if s.Iterations == 0 || (!s.Approved && s.TotalComments == 0) {
		return lipgloss.NewStyle().Foreground(theme.Red).Render("✗")
	}
	return lipgloss.NewStyle().Foreground(theme.Yellow).Render("⚠")
}

func sessionOutcomeSummary(s session.Session) string {
	if s.Approved {
		return "approved"
	}
	if s.TotalComments > 0 {
		return fmt.Sprintf("%d comment(s), not approved", s.TotalComments)
	}
	return "error / incomplete"
}

// ── List view ─────────────────────────────────────────────────────────────────

func (m historyModel) viewList() string {
	w := m.width
	if w <= 0 {
		w = 80
	}
	h := m.height
	if h <= 0 {
		h = 24
	}

	// Header
	header := theme.StyleHeader.Width(w - 2).Render(
		theme.RenderCompactBrandWithDetails(w-2, "history") + "\n" +
			"  " + theme.StyleMuted.Render("sort: "+m.sortMode.String()) +
			"   " + theme.StyleMuted.Render("filter: "+m.filterOutcome.String()) +
			func() string {
				if m.filterText != "" {
					return "  " + theme.StyleMuted.Render(`repo: "`+m.filterText+`"`)
				}
				return ""
			}(),
	)

	// Body: list rows
	bodyLines := h - 5 // header(2) + border(1) + status(1) + border(1)
	if bodyLines < 1 {
		bodyLines = 1
	}

	var rows []string

	if len(m.filteredSessions) == 0 {
		empty := lipgloss.NewStyle().
			Foreground(theme.Overlay).
			Render("  No sessions found. Run `rinse` to record your first cycle.")
		rows = append(rows, empty)
	} else {
		// Paginate around cursor
		start := 0
		maxRows := bodyLines / 2 // each session = 2 lines
		if maxRows < 1 {
			maxRows = 1
		}
		if m.cursor >= maxRows {
			start = m.cursor - maxRows + 1
		}
		end := start + maxRows
		if end > len(m.filteredSessions) {
			end = len(m.filteredSessions)
		}

		for i := start; i < end; i++ {
			s := m.filteredSessions[i]
			icon := sessionIcon(s)
			repo := s.Repo
			pr := "#" + s.PR
			date := s.StartedAt.Local().Format("2006-01-02")
			dur := s.ElapsedWall()
			var durStr string
			if dur == 0 {
				durStr = "—"
			} else {
				durStr = formatElapsed(dur)
			}

			line1 := fmt.Sprintf("  %s  %-30s %-8s  %s · %s",
				icon,
				theme.Truncate(repo, 28),
				pr,
				date,
				durStr,
			)
			outcome := "    " + theme.StyleMuted.Render(sessionOutcomeSummary(s))

			if i == m.cursor {
				line1 = theme.StyleSelected.Render(line1)
				outcome = theme.StyleSelected.Render(outcome)
			} else {
				line1 = theme.StyleUnselected.Render(line1)
				outcome = theme.StyleMuted.Render(outcome)
			}

			rows = append(rows, line1, outcome)
		}
	}

	body := strings.Join(rows, "\n")

	// Status bar
	dot := theme.StyleMuted.Render(" " + theme.IconSep + " ")
	keys := "  " + strings.Join([]string{
		theme.RenderKeyHint("↑↓/jk", "move"),
		theme.RenderKeyHint("enter", "detail"),
		theme.RenderKeyHint("r", "re-run"),
		theme.RenderKeyHint("f", "filter"),
		theme.RenderKeyHint("s", "sort"),
		theme.RenderKeyHint("q", "quit"),
	}, dot)
	statusBar := theme.StyleStatusBar.Width(w - 2).Render(keys)

	return header + "\n" + body + "\n" + statusBar
}

// ── Detail view ───────────────────────────────────────────────────────────────

func (m historyModel) viewDetail() string {
	s := m.detailSession
	w := m.width
	if w <= 0 {
		w = 80
	}

	icon := sessionIcon(s)
	headerDetail := fmt.Sprintf("← back  %s  #%s  %s", s.Repo, s.PR, icon)
	header := theme.StyleHeader.Width(w - 2).Render(
		theme.RenderCompactBrandWithDetails(w-2, headerDetail),
	)

	// Fields
	saved := s.TimeSaved()
	var savedStr string
	if saved == 0 {
		savedStr = "—"
	} else {
		savedStr = fmt.Sprintf("~%d min", int(saved.Minutes()))
	}

	roundsStr := fmt.Sprintf("%d", s.Iterations)
	if len(s.CommentsByRound) > 1 {
		parts := make([]string, len(s.CommentsByRound))
		for i, c := range s.CommentsByRound {
			parts[i] = fmt.Sprintf("%d", c)
		}
		roundsStr += fmt.Sprintf("  (rounds: %s)", strings.Join(parts, " → "))
	}

	row := func(label, val string) string {
		return "  " + theme.StyleKey.Render(label) + "  " + theme.StyleVal.Render(val)
	}

	var lines []string
	lines = append(lines,
		row("Comments fixed:", fmt.Sprintf("%d", s.TotalComments)),
		row("Iterations:", roundsStr),
		row("Time saved:", savedStr),
		row("Rules learned:", fmt.Sprintf("+%d", s.RulesExtracted)),
	)

	// Elapsed wall-clock
	if elapsed := s.ElapsedWall(); elapsed > 0 {
		lines = append(lines, row("Elapsed:", formatElapsed(elapsed)))
	}

	if len(s.Patterns) > 0 {
		lines = append(lines, "")
		lines = append(lines, "  "+theme.StyleKey.Render("Top patterns:"))
		for _, p := range s.Patterns {
			lines = append(lines, "    "+theme.StyleMuted.Render("• ")+theme.StyleUnselected.Render(p))
		}
	}

	if m.detailCmd != "" {
		lines = append(lines, "")
		lines = append(lines, "  "+theme.StyleTeal.Render("$ "+m.detailCmd))
	}

	body := strings.Join(lines, "\n")

	// Status bar
	dot := theme.StyleMuted.Render(" " + theme.IconSep + " ")
	hints := []string{
		theme.RenderKeyHint("esc/q", "back"),
		theme.RenderKeyHint("o", "open PR in browser"),
		theme.RenderKeyHint("r", "show run command"),
	}
	statusBar := theme.StyleStatusBar.Width(w - 2).Render("  " + strings.Join(hints, dot))

	return header + "\n\n" + body + "\n\n" + statusBar
}

// ── Filter panel overlay ──────────────────────────────────────────────────────

func (m historyModel) viewFilterPanel() string {
	w := m.width
	if w <= 0 {
		w = 80
	}

	cursor := ""
	if m.filterTyping {
		cursor = "█"
	}
	textVal := m.filterText + cursor
	if textVal == "" {
		textVal = theme.StyleMuted.Render("(any)")
	}

	content := theme.GradientString("FILTER", theme.Mauve, theme.Lavender, true) + "\n\n" +
		"  " + theme.StyleKey.Render("Repo:") + "  " + theme.StyleVal.Render(textVal) + "\n" +
		"  " + theme.StyleKey.Render("Outcome:") + "  " + theme.StyleVal.Render(m.filterOutcome.String()) +
		theme.StyleMuted.Render(fmt.Sprintf("  (%d/4)", int(m.filterOutcome)+1)) + "\n\n" +
		"  " + theme.StyleMuted.Render("r=edit repo  tab=cycle outcome  esc/f=close")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Mauve).
		Padding(1, 4).
		Width(w - 8).
		Render(content)

	return box
}

// ── RunHistory ────────────────────────────────────────────────────────────────

// RunHistory loads session history and launches the TUI browser.
// Falls back to session.PrintStats when NO_COLOR or TERM=dumb.
func RunHistory() error {
	sessions, err := session.LoadAll()
	if err != nil {
		return fmt.Errorf("history: load sessions: %w", err)
	}

	// Fallback for dumb terminals or NO_COLOR.
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		session.PrintStats(sessions)
		return nil
	}

	m := newHistoryModel(sessions)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}


