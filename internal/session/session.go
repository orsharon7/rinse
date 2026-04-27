// Package session records post-cycle insight data and renders the summary
// that RINSE prints after every approved or completed PR review loop.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/orsharon7/rinse/internal/theme"
	"github.com/orsharon7/rinse/internal/upgrade"
)

// minutesPerComment is the estimated developer time saved per Copilot comment
// fixed by RINSE (used for the "time saved" heuristic in the summary).
const minutesPerComment = 4

// Session captures the outcome of a single RINSE PR review cycle.
type Session struct {
	// Identity
	PR         string `json:"pr"`
	Repo       string `json:"repo"`
	RunnerName string `json:"runner_name"`

	// Timing
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`

	// Outcomes
	Approved       bool  `json:"approved"`
	Iterations     int   `json:"iterations"`
	TotalComments  int   `json:"total_comments"`
	RulesExtracted int   `json:"rules_extracted,omitempty"`
	CommentsByRound []int `json:"comments_by_round,omitempty"`

	// Patterns extracted from reflect lines (best-effort).
	Patterns []string `json:"patterns,omitempty"`
}

// TimeSaved returns the estimated developer time saved by this session.
func (s Session) TimeSaved() time.Duration {
	return time.Duration(s.TotalComments*minutesPerComment) * time.Minute
}

// ElapsedWall returns the actual wall-clock duration of the session.
func (s Session) ElapsedWall() time.Duration {
	if s.EndedAt.IsZero() {
		return 0
	}
	return s.EndedAt.Sub(s.StartedAt).Round(time.Second)
}

// sessionsDir is the directory resolver for session persistence.
// It is a variable so tests can override it with a temp directory.
// RINSE_SESSIONS_DIR, if set, overrides the default ~/.rinse/sessions path.
var sessionsDir = func() (string, error) {
	if envDir := os.Getenv("RINSE_SESSIONS_DIR"); envDir != "" {
		return envDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("session: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".rinse", "sessions"), nil
}

// sessionPath builds the file path for a given session.
// We include the timestamp so multiple sessions for the same PR don't collide.
func sessionPath(s Session) (string, error) {
	dir, err := sessionsDir()
	if err != nil {
		return "", err
	}
	safe := strings.ReplaceAll(s.Repo, "/", "_")
	ts := s.StartedAt.UTC().Format("20060102-150405")
	nano := s.StartedAt.UTC().UnixNano() % 1e9
	name := fmt.Sprintf("%s-pr%s-%s-%09d.json", safe, s.PR, ts, nano)
	return filepath.Join(dir, name), nil
}

// Save persists the session to ~/.rinse/sessions/.
// It writes to a temp file and renames atomically to avoid partial writes.
func (s Session) Save() error {
	dir, err := sessionsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("session: create sessions dir: %w", err)
	}

	path, err := sessionPath(s)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("session: marshal: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("session: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("session: rename: %w", err)
	}
	return nil
}

// LoadAll reads all sessions from ~/.rinse/sessions/, oldest first.
// Files that cannot be parsed are silently skipped (corrupt/partial writes).
func LoadAll() ([]Session, error) {
	dir, err := sessionsDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("session: read sessions dir: %w", err)
	}

	var sessions []Session
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // skip unreadable files
		}
		var s Session
		if err := json.Unmarshal(data, &s); err != nil {
			continue // skip corrupt files
		}
		sessions = append(sessions, s)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].StartedAt.Before(sessions[j].StartedAt)
	})
	return sessions, nil
}

// LoadAllTimeSaved reads all sessions and returns the total estimated time saved
// (in minutes) and the total number of sessions (PRs).
func LoadAllTimeSaved() (totalMin int, totalPRs int, err error) {
	sessions, err := LoadAll()
	if err != nil {
		return 0, 0, err
	}
	for _, s := range sessions {
		totalMin += int(s.TimeSaved().Minutes())
		totalPRs++
	}
	return totalMin, totalPRs, nil
}

// formatSummaryElapsed formats a duration for the post-cycle summary title line.
// e.g. 4m32s → "4m 32s", 45s → "45s".
func formatSummaryElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	if d <= 0 {
		return ""
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// summaryTermWidth returns the terminal width for summary rendering.
// It reads $COLUMNS and falls back to 80.
func summaryTermWidth() int {
	if v := os.Getenv("COLUMNS"); v != "" {
		var w int
		if _, err := fmt.Sscanf(v, "%d", &w); err == nil && w > 0 {
			return w
		}
	}
	return 80
}

// PrintSummary writes the post-cycle insight summary to stdout.
// When jsonMode is true it emits JSON instead of the human-readable banner.
func PrintSummary(s Session, jsonMode bool) {
	if jsonMode {
		data, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "session: marshal summary: %v\n", err)
			return
		}
		fmt.Println(string(data))
		return
	}

	// Dumb-terminal / NO_COLOR: fall back to plain text output.
	noCols := os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb"

	termW := summaryTermWidth()
	// Box width: min(termW-4, 56), at least 30.
	boxW := termW - 4
	if boxW > 56 {
		boxW = 56
	}
	if boxW < 30 {
		boxW = 30
	}

	// ── Title line ────────────────────────────────────────────────────────────
	var titleIcon, statusStr string
	if s.Approved {
		titleIcon = theme.StyleLogSuccess.Render(theme.IconCheck)
		statusStr = theme.StyleLogSuccess.Render("approved")
	} else {
		titleIcon = theme.StyleMuted.Render(theme.IconCircle)
		statusStr = theme.StyleMuted.Render("complete")
	}
	label := theme.GradientString("RINSE", theme.Mauve, theme.Lavender, true)
	prBadge := theme.StylePRNum.Render("PR #" + s.PR)

	elapsedStr := formatSummaryElapsed(s.ElapsedWall())
	titleParts := []string{titleIcon, label, prBadge, statusStr}
	if elapsedStr != "" {
		titleParts = append(titleParts, theme.StyleMuted.Render(elapsedStr))
	}
	fmt.Println()
	fmt.Println("  " + strings.Join(titleParts, "  "))
	fmt.Println()

	// ── Hero box — value prop front and centre ────────────────────────────────
	// Only render the box when terminal is capable.
	if !noCols && termW >= 40 {
		var heroIcon, heroMain string
		saved := s.TimeSaved()

		if s.Approved {
			heroIcon = theme.StyleLogSuccess.Render("✦")
			if s.TotalComments > 0 && saved > 0 {
				heroMain = theme.StyleVal.Render(
					fmt.Sprintf("%d comment(s) fixed", s.TotalComments)) +
					theme.StyleMuted.Render(fmt.Sprintf("  ·  ~%d min saved", int(saved.Minutes())))
			} else if s.TotalComments > 0 {
				heroMain = theme.StyleVal.Render(fmt.Sprintf("%d comment(s) fixed", s.TotalComments))
			} else {
				heroMain = theme.StyleMuted.Render("clean review — no comments to fix")
			}
		} else {
			heroIcon = theme.StylePhaseStalled.Render("⚠")
			if s.TotalComments > 0 {
				heroMain = theme.StyleVal.Render(fmt.Sprintf("%d comment(s) fixed", s.TotalComments)) +
					theme.StyleMuted.Render(", PR not yet approved")
			} else {
				heroMain = theme.StyleMuted.Render("PR not yet approved")
			}
		}

		heroLine1 := heroIcon + "  " + heroMain

		// Rounds breakdown — only if >1 round.
		var heroLine2 string
		if len(s.CommentsByRound) > 1 {
			parts := make([]string, len(s.CommentsByRound))
			for i, c := range s.CommentsByRound {
				parts[i] = fmt.Sprintf("%d", c)
			}
			heroLine2 = theme.StyleMuted.Render(
				fmt.Sprintf("%d rounds: %s", len(s.CommentsByRound), strings.Join(parts, " → ")))
		}

		var boxContent string
		if heroLine2 != "" {
			boxContent = heroLine1 + "\n" + "   " + heroLine2
		} else {
			boxContent = heroLine1
		}

		borderColor := theme.Green
		if !s.Approved {
			borderColor = theme.Yellow
		}

		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor).
			Padding(0, 2).
			Width(boxW).
			Render(boxContent)

		for _, line := range strings.Split(box, "\n") {
			fmt.Println("  " + line)
		}
		fmt.Println()
	}

	// ── Metrics rows ──────────────────────────────────────────────────────────
	key := func(k string) string { return theme.StyleKey.Copy().Width(18).Render(k) }

	// Elapsed wall-clock
	if elapsedStr != "" {
		fmt.Println("  " + key("Elapsed") + theme.StyleMuted.Render(elapsedStr))
	}

	// Iterations — note if max was hit (no approved)
	iterStr := fmt.Sprintf("%d", s.Iterations)
	if !s.Approved && s.Iterations > 0 {
		iterStr += theme.StyleMuted.Render("  (max reached)")
	}
	fmt.Println("  " + key("Iterations") + theme.StyleMuted.Render(iterStr))

	// Rules learned — only if > 0
	if s.RulesExtracted > 0 {
		fmt.Println("  " + key("Rules learned") + theme.StyleVal.Render(fmt.Sprintf("+%d", s.RulesExtracted)))
	}

	// Top patterns (if any) — each on its own line
	if len(s.Patterns) > 0 {
		top := s.Patterns
		if len(top) > 3 {
			top = top[:3]
		}
		fmt.Println("  " + key("Top patterns") + theme.StyleMuted.Render(theme.Truncate(top[0], 50)))
		for _, p := range top[1:] {
			fmt.Println("  " + strings.Repeat(" ", 20) + theme.StyleMuted.Render(theme.Truncate(p, 50)))
		}
	}

	fmt.Println()

	// ── "What's next" hint ────────────────────────────────────────────────────
	var ctaCmd, ctaSuffix string
	if s.Approved {
		ctaCmd = "rinse history"
		ctaSuffix = "to browse past cycles"
	} else {
		ctaCmd = fmt.Sprintf("rinse run --pr %s", s.PR)
		ctaSuffix = "to resume"
	}
	cta := theme.StyleMuted.Render("  $ ") +
		theme.StyleTeal.Render(ctaCmd) +
		theme.StyleMuted.Render("   "+ctaSuffix)
	fmt.Println(cta)
	fmt.Println()

	// Show upgrade prompt after proof-of-value cycles (approved outcome only, no NO_COLOR/dumb terminal).
	if s.Approved && !noCols {
		if totalMin, totalPRs, err := LoadAllTimeSaved(); err == nil {
			if upgrade.ShouldShowPrompt(totalPRs) {
				fmt.Println(upgrade.RenderPrompt(totalMin, totalPRs))
				fmt.Println()
				upgrade.RecordShown(totalPRs)
			}
		}
	}
}

// PrintStats renders an aggregate statistics table for a slice of sessions.
func PrintStats(sessions []Session) {
	if len(sessions) == 0 {
		fmt.Println()
		fmt.Println("  " + theme.StyleMuted.Render("No RINSE sessions recorded yet."))
		fmt.Println("  " + theme.StyleMuted.Render("Run ") + theme.StyleTeal.Render("rinse") + theme.StyleMuted.Render(" to start your first cycle."))
		fmt.Println()
		return
	}

	var totalComments, totalIterations int
	var totalSaved time.Duration
	approvals := 0
	for _, s := range sessions {
		totalComments += s.TotalComments
		totalIterations += s.Iterations
		totalSaved += s.TimeSaved()
		if s.Approved {
			approvals++
		}
	}

	// Title
	fmt.Println()
	title := theme.StyleStep.Render(theme.IconRadioOn+" ") +
		theme.GradientString("RINSE", theme.Mauve, theme.Lavender, true) +
		"  " + theme.StyleMuted.Render("session history")
	fmt.Println("  " + title)
	fmt.Println()

	// Aggregate metrics
	key := func(k string) string { return theme.StyleKey.Copy().Width(22).Render(k) }
	fmt.Println("  " + key("Total sessions") + theme.StyleMuted.Render(fmt.Sprintf("%d", len(sessions))))
	fmt.Println("  " + key("Approved") + theme.StyleVal.Render(fmt.Sprintf("%d / %d", approvals, len(sessions))))
	fmt.Println("  " + key("Comments fixed") + theme.StyleLogSuccess.Render(fmt.Sprintf("%d", totalComments)))
	fmt.Println("  " + key("Time saved") + theme.StyleVal.Render(fmt.Sprintf("~%d min", int(totalSaved.Minutes()))))
	fmt.Println("  " + key("Iterations") + theme.StyleMuted.Render(fmt.Sprintf("%d", totalIterations)))
	fmt.Println()

	// Section header
	fmt.Println("  " + theme.StyleStep.Render("Recent sessions"))
	fmt.Println()

	// Session rows (newest first, max 10)
	recent := sessions
	if len(recent) > 10 {
		recent = recent[len(recent)-10:]
	}
	for i, j := 0, len(recent)-1; i < j; i, j = i+1, j-1 {
		recent[i], recent[j] = recent[j], recent[i]
	}
	for _, s := range recent {
		var icon string
		if s.Approved {
			icon = theme.StyleLogSuccess.Render(theme.IconCheck)
		} else {
			icon = theme.StyleMuted.Render(theme.IconCircle)
		}
		date := theme.StyleMuted.Render(s.StartedAt.Local().Format("2006-01-02"))
		pr := theme.StylePRNum.Render(fmt.Sprintf("PR #%-5s", s.PR))
		cmts := theme.StyleMuted.Render(fmt.Sprintf("%2d comments", s.TotalComments))
		mins := theme.StyleVal.Render(fmt.Sprintf("~%d min", int(s.TimeSaved().Minutes())))
		fmt.Printf("  %s  %s  %s  %s  %s\n", icon, date, pr, cmts, mins)
	}
	fmt.Println()
}
