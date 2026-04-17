// Package stats provides session history recording and summary reporting for rinse.
//
// Sessions are stored in two places:
//   - ~/.rinse/rinse.db  (SQLite, preferred — written by the Go runner)
//   - ~/.rinse/sessions/ (JSON files, legacy — written by the shell scripts)
//
// Load() reads the DB when available and falls back to JSON files. This lets
// existing shell-script sessions coexist with new Go-runner sessions until the
// migration is complete.
package stats

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	mrand "math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/orsharon7/rinse/internal/db"
	"github.com/orsharon7/rinse/internal/theme"
	"github.com/orsharon7/rinse/internal/upgrade"
)

// Outcome describes the terminal result of a RINSE cycle.
type Outcome string

const (
	OutcomeApproved Outcome = "approved"
	OutcomeMerged   Outcome = "merged"
	OutcomeMaxIter  Outcome = "max_iterations"
	OutcomeError    Outcome = "error"
	OutcomeAborted  Outcome = "aborted"
)

// newUUID generates a random UUID v4 string.
// Returns an error if the OS random source is unavailable.
func newUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("stats: newUUID: crypto/rand unavailable: %w", err)
	}
	// Set version 4 (bits 12-15 of byte 6 to 0100)
	b[6] = (b[6] & 0x0f) | 0x40
	// Set variant bits (bits 6-7 of byte 8 to 10)
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// Session records the outcome of a single rinse PR-review run.
type Session struct {
	// Identity
	SessionID string `json:"session_id"`

	// Metadata
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Repo      string    `json:"repo"`
	PR        string    `json:"pr"`
	PRTitle   string    `json:"pr_title,omitempty"`
	Runner    string    `json:"runner"`
	Model     string    `json:"model"`

	// Outcomes
	Outcome                    Outcome  `json:"outcome"`
	Iterations                 int      `json:"iterations"`
	CopilotCommentsByIteration []int    `json:"copilot_comments_by_iteration"`
	TotalComments              int      `json:"total_comments"`
	EstimatedTimeSavedSeconds  int      `json:"estimated_time_saved_seconds"`
	Approved                   bool     `json:"approved"`
	Patterns                   []string `json:"patterns,omitempty"`
}

// NewSession creates a new Session with a generated UUID and the current time
// as StartedAt. It panics if the OS random source is unavailable, since that
// indicates a severe system fault.
func NewSession(repo, pr, runner, model string) Session {
	id, err := newUUID()
	if err != nil {
		panic(err)
	}
	return Session{
		SessionID: id,
		StartedAt: time.Now().UTC(),
		Repo:      repo,
		PR:        pr,
		Runner:    runner,
		Model:     model,
	}
}

// DurationSeconds returns the session duration in seconds.
func (s Session) DurationSeconds() float64 {
	return s.EndedAt.Sub(s.StartedAt).Seconds()
}

// SessionsDir returns the directory where legacy session JSON files are stored.
func SessionsDir() (string, error) {
	if sessionsDirOverride != "" {
		return sessionsDirOverride, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".rinse", "sessions"), nil
}

// Save writes the session as a JSON file in SessionsDir (legacy path, used by
// shell scripts). New code should write to the SQLite DB instead.
func Save(s Session) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("stats: cannot load config: %w", err)
	}
	if cfg.StatsOptIn != nil && !*cfg.StatsOptIn {
		// User explicitly opted out — never prompt again.
		return nil
	}
	if cfg.StatsOptIn == nil {
		// No preference set yet — prompt if interactive; silently skip in CI.
		fi, statErr := os.Stdin.Stat()
		if statErr != nil || (fi.Mode()&os.ModeCharDevice) == 0 {
			return nil // CI/non-TTY: silently off
		}
		optedIn, promptErr := PromptOptIn()
		if promptErr != nil {
			return promptErr
		}
		if !optedIn {
			return nil
		}
	}

	s.SchemaVersion = SchemaVersion

	dir, err := SessionsDir()
	if err != nil {
		return fmt.Errorf("stats: cannot determine sessions dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("stats: cannot create sessions dir: %w", err)
	}

	repoSlug := strings.ReplaceAll(s.Repo, "/", "-")
	safePR := strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= 'a' && r <= 'z':
			return r
		default:
			return '-'
		}
	}, s.PR)
	if safePR == "" {
		safePR = "unknown"
	}
	fname := fmt.Sprintf("%s-%d-%s-PR%s.json",
		s.StartedAt.Format("20060102-150405"),
		s.StartedAt.UnixNano(),
		repoSlug,
		safePR,
	)
	path := filepath.Join(dir, fname)

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("stats: cannot marshal session: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, "session.tmp-*")
	if err != nil {
		return fmt.Errorf("stats: cannot create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("stats: cannot write session: %w", err)
	}
	if err := tmpFile.Chmod(0o644); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("stats: cannot chmod session: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("stats: cannot sync session: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("stats: cannot close session: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("stats: cannot rename session: %w", err)
	}
	success = true
	return nil
}

// Load reads sessions from the best available source:
//   - ~/.rinse/rinse.db when it exists (SQLite, preferred)
//   - ~/.rinse/sessions/*.json as fallback (legacy JSON files)
//
// Sessions from both sources are merged and deduped by (repo, PR, started_at
// truncated to the minute) so that a mixed environment shows unified stats.
func Load() ([]Session, error) {
	dbSessions, dbErr := loadFromDB()
	jsonSessions, jsonErr := loadFromJSON()

	// If both sources fail, surface the DB error (more informative).
	if dbErr != nil && jsonErr != nil {
		return nil, fmt.Errorf("stats: db: %w; json: %v", dbErr, jsonErr)
	}

	// Merge, dedup by session fingerprint (repo|pr|started-minute).
	seen := make(map[string]bool, len(dbSessions)+len(jsonSessions))
	merged := make([]Session, 0, len(dbSessions)+len(jsonSessions))

	add := func(s Session) {
		key := fmt.Sprintf("%s|%s|%s", s.Repo, s.PR,
			s.StartedAt.UTC().Truncate(time.Minute).Format(time.RFC3339))
		if !seen[key] {
			seen[key] = true
			merged = append(merged, s)
		}
	}

	// DB sessions take precedence — add first so JSON dupes are skipped.
	for _, s := range dbSessions {
		add(s)
	}
	for _, s := range jsonSessions {
		add(s)
	}

	sort.Slice(merged, func(i, j int) bool {
		return merged[i].StartedAt.Before(merged[j].StartedAt)
	})
	return merged, nil
}

// loadFromDB opens the default SQLite DB and converts rows to Session values.
// Returns (nil, nil) when the DB file does not exist yet — not an error.
func loadFromDB() ([]Session, error) {
	dbPath, err := db.Path()
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(dbPath); os.IsNotExist(statErr) {
		return nil, nil // DB not created yet — not an error
	} else if statErr != nil {
		return nil, statErr
	}

	d, err := db.Open(dbPath)
	if err != nil {
		return nil, err
	}
	defer d.Close()

	rows, err := d.LoadSessions()
	if err != nil {
		return nil, err
	}

	sessions := make([]Session, 0, len(rows))
	for _, r := range rows {
		s := Session{
			StartedAt:     r.StartedAt,
			Repo:          r.Repo,
			PR:            fmt.Sprintf("%d", r.PRNumber),
			Runner:        r.Runner,
			Model:         r.Model,
			TotalComments: r.TotalCommentsFixed,
			Iterations:    r.Iterations,
			Approved:      r.Outcome == "merged",
			Patterns:      r.Patterns,
		}
		if r.CompletedAt != nil {
			s.EndedAt = *r.CompletedAt
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// loadFromJSON reads legacy JSON session files from SessionsDir.
func loadFromJSON() ([]Session, error) {
	dir, err := SessionsDir()
	if err != nil {
		return sortByStarted(sessions), nil
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return sortByStarted(sessions), nil
	}
	if err != nil {
		return sortByStarted(sessions), err
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var s Session
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		if seen[s.SessionID] {
			continue // DB record takes precedence
		}
		sessions = append(sessions, s)
	}

	return sortByStarted(sessions), nil
}

// dbRowToSession converts a db.SessionRow to a stats.Session.
func dbRowToSession(r db.SessionRow) Session {
	s := Session{
		SessionID: r.ID,
		Repo:      r.Repo,
		PR:        fmt.Sprintf("%d", r.PRNumber),
		PRTitle:   r.PRTitle,
		StartedAt: r.StartedAt,
		Model:     r.Model,
		Outcome:   Outcome(r.Outcome),
		Iterations: r.Iterations,
		TotalComments: r.TotalCommentsFixed,
	}
	if r.CompletedAt != nil {
		s.EndedAt = *r.CompletedAt
	}
	if r.EstimatedTimeSavedSeconds != nil {
		s.EstimatedTimeSavedSeconds = *r.EstimatedTimeSavedSeconds
	}
	s.Approved = s.Outcome == OutcomeApproved || s.Outcome == OutcomeMerged
	return s
}

func sortByStarted(sessions []Session) []Session {
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].StartedAt.Before(sessions[j].StartedAt)
	})
	return sessions
}

// Summary holds aggregated metrics across a set of sessions.
type Summary struct {
	TotalSessions    int
	TotalComments    int
	TotalIterations  int
	ApprovedSessions int
	TotalDurationSec float64
	PatternCounts    map[string]int
	OutcomeCounts    map[Outcome]int
	// Last30Days is a filtered summary over the last 30 days.
	// It is always populated by Summarize and is the zero value when no sessions match.
	Last30Days *Summary
}

// AvgIterations returns the average iterations per session (0 if no sessions).
func (s *Summary) AvgIterations() float64 {
	if s.TotalSessions == 0 {
		return 0
	}
	return float64(s.TotalIterations) / float64(s.TotalSessions)
}

// EstTimeSavedHours returns a rough estimate of hours saved.
// Uses TotalTimeSavedSeconds (populated by Summarize from per-session
// EstimatedTimeSavedSeconds, which assumes 240 s/comment, matching Finish).
func (s *Summary) EstTimeSavedHours() float64 {
	return math.Round(float64(s.TotalTimeSavedSeconds)/3600*10) / 10
}

// TopPatterns returns up to n patterns ordered by frequency (descending).
func (s *Summary) TopPatterns(n int) []PatternCount {
	if len(s.PatternCounts) == 0 {
		return nil
	}
	counts := make([]PatternCount, 0, len(s.PatternCounts))
	for pat, cnt := range s.PatternCounts {
		counts = append(counts, PatternCount{Pattern: pat, Count: cnt})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].Count != counts[j].Count {
			return counts[i].Count > counts[j].Count
		}
		return counts[i].Pattern < counts[j].Pattern
	})
	if n > 0 && len(counts) > n {
		counts = counts[:n]
	}
	return counts
}

// PatternCount pairs a pattern name with its occurrence count.
type PatternCount struct {
	Pattern string
	Count   int
}

// Summarize aggregates a slice of sessions into a Summary.
// It also builds a nested Last30Days summary automatically.
func Summarize(sessions []Session) Summary {
	cutoff := time.Now().AddDate(0, 0, -30)

	var all, recent []Session
	for _, s := range sessions {
		all = append(all, s)
		if s.StartedAt.After(cutoff) {
			recent = append(recent, s)
		}
	}

	build := func(ss []Session) Summary {
		sum := Summary{
			PatternCounts: make(map[string]int),
			OutcomeCounts: make(map[Outcome]int),
		}
		for _, s := range ss {
			sum.TotalSessions++
			sum.TotalComments += s.TotalComments
			sum.TotalIterations += s.Iterations
			sum.TotalDurationSec += s.DurationSeconds()
			if s.Outcome == OutcomeApproved {
				sum.ApprovedSessions++
			}
			sum.OutcomeCounts[s.Outcome]++
			for _, p := range s.Patterns {
				sum.PatternCounts[p]++
			}
		}
		return sum
	}

	sum := build(all)
	recent30 := build(recent)
	sum.Last30Days = &recent30
	return sum
}

// filterStartedTodayUTC returns only sessions whose StartedAt timestamp falls on
// the current UTC day.
func filterStartedTodayUTC(sessions []Session) []Session {
	now := time.Now().UTC()
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	var out []Session
	for _, s := range sessions {
		if !s.StartedAt.UTC().Before(today) {
			out = append(out, s)
		}
	}
	return out
}

// LoadToday reads session files from SessionsDir filtered to today (UTC).
func LoadToday() ([]Session, error) {
	all, err := Load()
	if err != nil {
		return nil, err
	}
	return filterStartedTodayUTC(all), nil
}

// ReportSummary extends Summary with per-session extremes for the report command.
type ReportSummary struct {
	Summary
	FastestSec float64
	FastestPR  string
	LongestSec float64
	LongestPR  string
}

// SummarizeReport builds a ReportSummary from a session slice.
func SummarizeReport(sessions []Session) ReportSummary {
	r := ReportSummary{
		Summary: Summary{PatternCounts: make(map[string]int)},
	}
	r.FastestSec = -1
	for _, s := range sessions {
		r.TotalSessions++
		r.TotalComments += s.TotalComments
		r.TotalIterations += s.Iterations
		dur := s.DurationSeconds()
		r.TotalDurationSec += dur
		if s.Approved {
			r.ApprovedSessions++
		}
		for _, p := range s.Patterns {
			r.PatternCounts[p]++
		}
		if dur > 0 && (r.FastestSec < 0 || dur < r.FastestSec) {
			r.FastestSec = dur
			r.FastestPR = s.PR
		}
		if dur > r.LongestSec {
			r.LongestSec = dur
			r.LongestPR = s.PR
		}
	}
	return r
}

// PrintReport writes the `rinse report` dashboard to stdout.
// It prints a today-focused summary (or all-time if today has no data).
func PrintReport(sessions []Session) {
	todaySessions := filterStartedTodayUTC(sessions)
	today := time.Now().UTC()

	target := todaySessions
	dateLabel := "Today's Report · " + today.Format("January 2, 2006")
	if len(target) == 0 {
		target = sessions
		dateLabel = "All-Time Report"
	}

	if len(target) == 0 {
		fmt.Println()
		fmt.Println("  " + theme.StyleMuted.Render("No sessions recorded yet. Run ") +
			theme.StyleTeal.Render("rinse") +
			theme.StyleMuted.Render(" on a PR to start tracking."))
		fmt.Println()
		return
	}

	r := SummarizeReport(target)

	// Title
	fmt.Println()
	icon := theme.StyleStep.Render(theme.IconRadioOn + " ")
	label := theme.GradientString("RINSE", theme.Mauve, theme.Lavender, true)
	date := theme.StyleMuted.Render("  " + dateLabel)
	fmt.Println("  " + icon + label + date)
	fmt.Println()

	// Key column — 22 chars wide
	key := func(s string) string { return theme.StyleKey.Copy().Width(22).Render(s) }

	// Cycles / PRs reviewed / approved
	fmt.Println("  " + key("Cycles run") + theme.StyleMuted.Render(fmt.Sprintf("%d", r.TotalSessions)))
	fmt.Println("  " + key("PRs reviewed") + theme.StyleMuted.Render(fmt.Sprintf("%d", r.TotalSessions)))
	if r.TotalSessions > 0 {
		pct := int(math.Round(float64(r.ApprovedSessions) / float64(r.TotalSessions) * 100))
		fmt.Println("  " + key("PRs approved") + theme.StyleVal.Render(fmt.Sprintf("%d (%d%%)", r.ApprovedSessions, pct)))
	}
	fmt.Println()

	// Time / comments / avg
	timeSaved := r.EstTimeSavedHours()
	avgComments := 0.0
	if r.TotalSessions > 0 {
		avgComments = float64(r.TotalComments) / float64(r.TotalSessions)
	}
	fmt.Println("  " + key("Time saved") + theme.StyleVal.Render(fmt.Sprintf("~%.1f hours (est.)", timeSaved)))
	fmt.Println("  " + key("Comments fixed") + theme.StyleLogSuccess.Render(fmt.Sprintf("%d", r.TotalComments)))
	fmt.Println("  " + key("Avg per PR") + theme.StyleMuted.Render(fmt.Sprintf("%.0f comments, %.1f iters", avgComments, r.AvgIterations())))
	fmt.Println()

	// Fastest / longest (conditional)
	if r.FastestSec > 0 {
		fastStr := formatMinutes(int(math.Round(r.FastestSec / 60)))
		prStr := ""
		if r.FastestPR != "" {
			prStr = "  " + theme.StylePRNum.Render("PR #"+r.FastestPR)
		}
		fmt.Println("  " + key("Fastest cycle") + theme.StyleMuted.Render(fastStr) + prStr)
	}
	if r.LongestSec > 0 {
		longStr := formatMinutes(int(math.Round(r.LongestSec / 60)))
		prStr := ""
		if r.LongestPR != "" {
			prStr = "  " + theme.StylePRNum.Render("PR #"+r.LongestPR)
		}
		fmt.Println("  " + key("Longest cycle") + theme.StyleMuted.Render(longStr) + prStr)
	}

	// Top patterns
	top := r.TopPatterns(3)
	if len(top) > 0 {
		fmt.Println()
		fmt.Println("  " + theme.StyleStep.Render("Top patterns"))
		fmt.Println()
		for i, p := range top {
			num := theme.StyleMuted.Render(fmt.Sprintf("%d.", i+1))
			name := theme.StyleMuted.Render(fmt.Sprintf("  %-34s", p.Pattern))
			count := theme.StyleVal.Render(fmt.Sprintf("%dx", p.Count))
			fmt.Println("    " + num + name + count)
		}
	}

	// Footer
	fmt.Println()
	fmt.Println("  " + theme.StyleMuted.Render(strings.Repeat("─", 41)))
	dir, err := SessionsDir()
	if err != nil {
		fmt.Println("  " + theme.StyleMuted.Render("Sessions: (unknown)"))
	} else {
		fmt.Println("  " + theme.StyleMuted.Render("Sessions: "+dir))
	}
	fmt.Println()
}

// formatMinutes renders a duration in minutes as a human string.
func formatMinutes(mins int) string {
	if mins < 1 {
		return "<1 min"
	}
	return fmt.Sprintf("%d min", mins)
}

// Print writes a formatted stats report to stdout.
func Print(sessions []Session) {
	sum := Summarize(sessions)

	display := *sum.Last30Days
	label := "last 30 days"

	if sum.TotalSessions > 0 && sum.Last30Days.TotalSessions == 0 {
		display = sum
		label = "all time (no sessions in last 30 days)"
	}

	fmt.Printf("\n  RINSE Stats (%s)\n", label)
	fmt.Printf("  PRs reviewed:     %d\n", display.TotalSessions)
	fmt.Printf("  Comments fixed:   %d\n", display.TotalComments)
	fmt.Printf("  Avg iterations:   %.1f\n", display.AvgIterations())
	fmt.Printf("  Est. time saved:  ~%.1f hours\n", display.EstTimeSavedHours())
	if n := display.ApprovedSessions; n > 0 {
		fmt.Printf("  Approved:         %d\n", n)
	}

	top := display.TopPatterns(5)
	if len(top) > 0 {
		fmt.Println("\n  Top patterns:")
		for i, p := range top {
			fmt.Printf("    %d. %-30s (%dx)\n", i+1, p.Pattern, p.Count)
		}
	}
	fmt.Println()

	// Show Pro upgrade prompt at proof-of-value thresholds (3, 5, 10, 20 sessions).
	if upgrade.ShouldShowPrompt(sum.TotalSessions) {
		totalMin := int(sum.EstTimeSavedHours() * 60)
		fmt.Println(upgrade.RenderPrompt(totalMin, sum.TotalSessions))
		upgrade.RecordShown(sum.TotalSessions)
	}
}
