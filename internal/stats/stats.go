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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/orsharon7/rinse/internal/config"
	"github.com/orsharon7/rinse/internal/db"
	"github.com/orsharon7/rinse/internal/quality"
	"github.com/orsharon7/rinse/internal/theme"
	"github.com/orsharon7/rinse/internal/upgrade"
)

// SchemaVersion is the current JSON session file schema version.
// Bump this when the Session struct layout changes incompatibly.
const SchemaVersion = 1

// sessionsDirOverride allows tests to redirect session file writes to a
// temporary directory. Set via the test hook in sessions_test_hook_test.go.
var sessionsDirOverride string

// Outcome describes the terminal result of a RINSE cycle.
type Outcome string

const (
	OutcomeApproved Outcome = "approved"
	OutcomeMerged   Outcome = "merged"
	OutcomeClosed   Outcome = "closed"
	OutcomeMaxIter  Outcome = "max_iterations"
	OutcomeError    Outcome = "error"
	OutcomeAborted  Outcome = "aborted"
	OutcomeClean    Outcome = "clean"
	OutcomeDryRun   Outcome = "dry_run"
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
	SessionID     string `json:"session_id"`
	SchemaVersion int    `json:"schema_version,omitempty"`

	// Metadata
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Repo      string    `json:"repo"`
	PR        string    `json:"pr"`
	PRTitle   string    `json:"pr_title"`
	Runner    string    `json:"runner"`
	Model     string    `json:"model"`

	// Outcomes
	Outcome                    Outcome  `json:"outcome"`
	Iterations                 int      `json:"iterations"`
	CopilotCommentsByIteration []int    `json:"copilot_comments_by_iteration"`
	TotalComments              int      `json:"total_comments"`
	EstimatedTimeSavedSeconds  int      `json:"estimated_time_saved_seconds"`
	Approved                   bool     `json:"approved"`
	RulesExtracted             int      `json:"rules_extracted,omitempty"`
	Patterns                   []string `json:"patterns,omitempty"`

	// Quality metrics (populated when available)
	Quality *quality.QualityDelta `json:"quality,omitempty"`
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
	// Allow test / CI environments to redirect session writes via env var.
	if envDir := os.Getenv("RINSE_SESSIONS_DIR"); envDir != "" {
		return envDir, nil
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
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("stats: cannot create sessions dir: %w", err)
	}

	if s.SessionID == "" {
		id, _ := newUUID()
		s.SessionID = id
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
			StartedAt:      r.StartedAt,
			Repo:           r.Repo,
			PR:             fmt.Sprintf("%d", r.PRNumber),
			Runner:         r.Runner,
			Model:          r.Model,
			TotalComments:  r.TotalCommentsFixed,
			Iterations:     r.Iterations,
			Approved:       r.Outcome == "merged",
			RulesExtracted: r.RulesExtracted,
			Patterns:       r.Patterns,
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
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var sessions []Session
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
		// Migrate legacy sessions that used a boolean "approved" field instead
		// of a string "outcome" field.
		if s.Outcome == "" {
			if s.Approved {
				s.Outcome = OutcomeApproved
			} else {
				s.Outcome = OutcomeClean
			}
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
	TotalSessions         int
	TotalComments         int
	TotalIterations       int
	ApprovedSessions      int
	TotalDurationSec      float64
	TotalTimeSavedSeconds int
	TotalRulesExtracted   int
	PatternCounts         map[string]int
	OutcomeCounts         map[Outcome]int
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
			sum.TotalRulesExtracted += s.RulesExtracted
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
	if n := display.TotalRulesExtracted; n > 0 {
		fmt.Printf("  Rules learned:    %d\n", n)
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

// PrintQualityReport prints a post-cycle quality report for a single session.
// It is shown after every completed RINSE cycle.
func PrintQualityReport(s Session) {
	if s.Quality == nil {
		return
	}
	q := s.Quality
	width := 54

	border := strings.Repeat("═", width-2)
	mid := strings.Repeat("═", width-2)
	blank := "║" + strings.Repeat(" ", width-2) + "║"

	title := fmt.Sprintf("RINSE Quality Report — PR #%s", s.PR)
	titleLine := fmt.Sprintf("║  %-*s║", width-4, title)

	scoreLine := func(label string, score float64, delta string) string {
		bar := scoreBar(score, 10)
		pct := fmt.Sprintf("%3.0f%%", score*100)
		return fmt.Sprintf("║  %-14s %s  %s %s║", label, bar, pct, pad(delta, 7))
	}

	fmt.Printf("╔%s╗\n", border)
	fmt.Println(titleLine)
	fmt.Printf("╠%s╣\n", mid)
	fmt.Println(blank)
	fmt.Println("║  Quality Score" + strings.Repeat(" ", width-17) + "║")
	fmt.Println(scoreLine("Before:", q.ScoreBefore, ""))
	d := q.ScoreDelta()
	dStr := ""
	if d > 0 {
		dStr = fmt.Sprintf("(+%.0f%%)", d*100)
	}
	fmt.Println(scoreLine("After: ", q.ScoreAfter, dStr))
	fmt.Println(blank)

	// Resolution rate
	rr := q.ResolutionRate
	rrBar := scoreBar(rr, 10)
	fmt.Printf("║  Resolution rate   %s  %3.0f%%%-*s║\n", rrBar, rr*100, width-40, "")

	// Convergence
	lambdaStr := fmt.Sprintf("λ=%.2f", q.FixRateLambda)
	speed := "slow"
	if q.FixRateLambda >= 1.5 {
		speed = "fast ✅"
	} else if q.FixRateLambda >= 0.8 {
		speed = "ok"
	}
	fmt.Printf("║  Convergence: %d iter  %s  (%s)%-*s║\n",
		s.Iterations, lambdaStr, speed, width-45-len(speed), "")

	// Time saved
	saved := s.EstimatedTimeSavedSeconds / 60
	fmt.Printf("║  Est. time saved: %d min%-*s║\n", saved, width-25, "")

	fmt.Println(blank)
	fmt.Printf("╚%s╝\n", border)
}

// PrintTrends shows quality improvement trends across sessions.
func PrintTrends(sessions []Session) {
	if len(sessions) == 0 {
		fmt.Println("  No sessions recorded yet.")
		return
	}

	// Build weekly buckets (last 4 weeks)
	now := time.Now()
	type weekBucket struct {
		label        string
		lambdaSum    float64
		lambdaCount  int
		scoreSum     float64
		scoreCount   int
		iterSum      int
		iterCount    int
	}

	weeks := make([]weekBucket, 4)
	for i := range weeks {
		weeksAgo := 3 - i
		start := now.AddDate(0, 0, -7*(weeksAgo+1))
		weeks[i].label = start.Format("Jan 02")
	}

	for _, s := range sessions {
		age := now.Sub(s.StartedAt)
		weekIdx := int(age.Hours()/24/7)
		if weekIdx < 0 || weekIdx >= 4 {
			continue
		}
		idx := 3 - weekIdx
		if idx < 0 {
			continue
		}
		if s.Quality != nil {
			weeks[idx].lambdaSum += s.Quality.FixRateLambda
			weeks[idx].lambdaCount++
			weeks[idx].scoreSum += s.Quality.ScoreAfter
			weeks[idx].scoreCount++
		}
		weeks[idx].iterSum += s.Iterations
		weeks[idx].iterCount++
	}

	fmt.Printf("\n  RINSE Quality Trends — (last 4 weeks)\n\n")
	fmt.Println("  Quality Score over time:")
	for _, w := range weeks {
		score := 0.0
		if w.scoreCount > 0 {
			score = w.scoreSum / float64(w.scoreCount)
		}
		bar := scoreBar(score, 10)
		fmt.Printf("  %s  %s  %3.0f%%\n", w.label, bar, score*100)
	}

	fmt.Println("\n  Fix rate (λ) trend:")
	for _, w := range weeks {
		lambda := 0.0
		if w.lambdaCount > 0 {
			lambda = w.lambdaSum / float64(w.lambdaCount)
		}
		avgIter := 0.0
		if w.iterCount > 0 {
			avgIter = float64(w.iterSum) / float64(w.iterCount)
		}
		speed := ""
		if lambda >= 1.5 {
			speed = "(fast)"
		} else if lambda >= 0.8 {
			speed = "(ok)"
		} else if lambda > 0 {
			speed = "(slow)"
		}
		fmt.Printf("  %s  λ = %.2f  avg %.1f iter  %s\n", w.label, lambda, avgIter, speed)
	}

	// Compute overall trend
	var firstLambda, lastLambda float64
	for _, w := range weeks {
		if w.lambdaCount > 0 && firstLambda == 0 {
			firstLambda = w.lambdaSum / float64(w.lambdaCount)
		}
		if w.lambdaCount > 0 {
			lastLambda = w.lambdaSum / float64(w.lambdaCount)
		}
	}
	if firstLambda > 0 && lastLambda > firstLambda {
		ratio := lastLambda / firstLambda
		fmt.Printf("\n  Verdict: RINSE is getting %.1fx faster at fixing Copilot comments.\n", ratio)
		fmt.Println("           AGENTS.md reflection is working. ✅")
	}
	fmt.Println()
}

// scoreBar renders a filled/empty bar of width n representing score in [0,1].
func scoreBar(score float64, n int) string {
	filled := int(math.Round(score * float64(n)))
	if filled > n {
		filled = n
	}
	if filled < 0 {
		filled = 0
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", n-filled)
}

// pad right-pads s to length l.
func pad(s string, l int) string {
	if len(s) >= l {
		return s
	}
	return s + strings.Repeat(" ", l-len(s))
}

// ── Stats opt-in / config ─────────────────────────────────────────────────────

// statsConfig is the subset of the rinse config file that controls telemetry.
type statsConfig struct {
	StatsOptIn *bool `json:"stats_opt_in,omitempty"`
	Pro        bool  `json:"pro,omitempty"`
}

// statsConfigPath returns the path to the rinse global config file.
func statsConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".rinse", "config.json"), nil
}

// loadConfig reads the stats opt-in preference from ~/.rinse/config.json.
// Returns an empty config (nil StatsOptIn) if the file does not exist.
func loadConfig() (statsConfig, error) {
	// RINSE_STATS_OPTIN env var allows tests (and CI) to force opt-in/out
	// without touching the on-disk config file.
	if v := os.Getenv("RINSE_STATS_OPTIN"); v != "" {
		optIn := v == "1" || v == "true"
		return statsConfig{StatsOptIn: &optIn}, nil
	}
	path, err := statsConfigPath()
	if err != nil {
		return statsConfig{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return statsConfig{}, nil
	}
	if err != nil {
		return statsConfig{}, err
	}
	var cfg statsConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return statsConfig{}, nil // treat corrupt config as unset
	}
	return cfg, nil
}

// SetOptIn persists the stats opt-in preference to ~/.rinse/config.json.
func SetOptIn(optIn bool) error {
	path, err := statsConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Read existing config to preserve other fields.
	cfg, _ := loadConfig()
	cfg.StatsOptIn = &optIn
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// IsOptedIn reports whether the user has explicitly opted in to stats
// collection. Returns (false, nil) when no preference has been set yet.
func IsOptedIn() (bool, error) {
	cfg, err := loadConfig()
	if err != nil {
		return false, err
	}
	if cfg.StatsOptIn == nil {
		return false, nil
	}
	return *cfg.StatsOptIn, nil
}

// PromptOptIn asks the user interactively whether to enable stats collection.
// Returns true when the user agrees. Writes the preference to disk.
func PromptOptIn() (bool, error) {
	fmt.Print("  RINSE can collect anonymous usage stats to improve the product.\n")
	fmt.Print("  Enable stats? [y/N]: ")
	var answer string
	if _, err := fmt.Scanln(&answer); err != nil {
		return false, nil
	}
	optIn := strings.ToLower(strings.TrimSpace(answer)) == "y"
	if err := SetOptIn(optIn); err != nil {
		return optIn, err
	}
	return optIn, nil
}

// ── Predict hit-rate stats ────────────────────────────────────────────────────

// predictEvent is the on-disk structure for a predict_generated event file
// written by internal/predict.LogEvent().
type predictEvent struct {
	EventType   string              `json:"event_type"`
	Source      string              `json:"source"`
	GeneratedAt string              `json:"generated_at"`
	Predictions []predictEventEntry `json:"predictions"`
}

type predictEventEntry struct {
	PatternID   string  `json:"pattern_id"`
	Description string  `json:"description"`
	File        string  `json:"file"`
	Line        int     `json:"line"`
	Confidence  float64 `json:"confidence"`
}

// sessionPredictResult holds hit-rate data for a single predict→session pair.
type sessionPredictResult struct {
	Date      time.Time
	Generated int // number of predictions made
	Matched   int // number of predictions that appeared in session patterns
}

// loadPredictEvents reads all predict_generated event files from the sessions dir.
func loadPredictEvents() ([]predictEvent, error) {
	dir, err := SessionsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var events []predictEvent
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "predict-") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var ev predictEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			continue
		}
		if ev.EventType == "predict_generated" {
			events = append(events, ev)
		}
	}
	return events, nil
}

// computePredictHitRate computes per-session hit rates by matching predict events
// against session pattern lists (best-effort correlation by time proximity).
// Returns results sorted oldest-first.
func computePredictHitRate(events []predictEvent, sessions []Session) []sessionPredictResult {
	if len(events) == 0 {
		return nil
	}

	var results []sessionPredictResult
	for _, ev := range events {
		t, err := time.Parse(time.RFC3339, ev.GeneratedAt)
		if err != nil {
			t = time.Now()
		}

		// Build set of predicted pattern IDs.
		predicted := make(map[string]bool, len(ev.Predictions))
		for _, p := range ev.Predictions {
			predicted[p.PatternID] = true
		}

		// Find the closest session that started within 10 minutes after the predict event.
		matched := 0
		var bestSession *Session
		for i := range sessions {
			s := &sessions[i]
			diff := s.StartedAt.Sub(t)
			if diff >= 0 && diff <= 10*time.Minute {
				if bestSession == nil || diff < bestSession.StartedAt.Sub(t) {
					bestSession = s
				}
			}
		}

		if bestSession != nil {
			// Count patterns from the session that were predicted.
			for _, pat := range bestSession.Patterns {
				pid := patternID(pat)
				if predicted[pid] {
					matched++
				}
			}
		}

		results = append(results, sessionPredictResult{
			Date:      t,
			Generated: len(ev.Predictions),
			Matched:   matched,
		})
	}

	// Sort oldest-first.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Date.Before(results[j].Date)
	})
	return results
}

// patternID converts a human-readable pattern name to a stable snake_case ID.
// Mirrors the same function in internal/predict to avoid an import cycle.
func patternID(pattern string) string {
	id := strings.ToLower(pattern)
	id = strings.ReplaceAll(id, " ", "_")
	id = strings.ReplaceAll(id, "/", "_")
	id = strings.ReplaceAll(id, "-", "_")
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

const predictGateThreshold = 0.85 // 85% hit rate unlocks auto-fix

// PrintPredictStats renders the `rinse stats --predict` dashboard.
// Pro status is read from ~/.rinse/config.json.
func PrintPredictStats() {
	isPro := config.IsPro()

	sessions, _ := Load()
	events, _ := loadPredictEvents()

	fmt.Println()
	icon := theme.StyleStep.Render(theme.IconRadioOn + " ")
	label := theme.GradientString("RINSE", theme.Mauve, theme.Lavender, true)
	sub := theme.StyleMuted.Render("  Prediction Hit Rate")
	fmt.Println("  " + icon + label + sub)
	fmt.Println()

	if len(events) == 0 {
		fmt.Println("  " + theme.StyleMuted.Render("No prediction events recorded yet."))
		fmt.Println("  " + theme.StyleMuted.Render("Run ") + theme.StyleTeal.Render("rinse predict") +
			theme.StyleMuted.Render(" on a PR to start tracking."))
		fmt.Println()
		return
	}

	results := computePredictHitRate(events, sessions)

	// Compute all-time hit rate.
	var totalGenerated, totalMatched int
	for _, r := range results {
		totalGenerated += r.Generated
		totalMatched += r.Matched
	}

	allTimeRate := 0.0
	if totalGenerated > 0 {
		allTimeRate = float64(totalMatched) / float64(totalGenerated)
	}

	// Rolling last-10 hit rate.
	last10 := results
	if len(last10) > 10 {
		last10 = last10[len(last10)-10:]
	}
	var l10gen, l10match int
	for _, r := range last10 {
		l10gen += r.Generated
		l10match += r.Matched
	}
	rolling10Rate := 0.0
	if l10gen > 0 {
		rolling10Rate = float64(l10match) / float64(l10gen)
	}

	key := func(s string) string { return theme.StyleKey.Copy().Width(24).Render(s) }
	barWidth := 10

	// Last 10 PRs row.
	l10Pct := int(math.Round(rolling10Rate * 100))
	l10Bar := scoreBar(rolling10Rate, barWidth)
	fmt.Println("  " + key("Last 10 PRs") + theme.StyleVal.Render(fmt.Sprintf("%d%%", l10Pct)) +
		"  " + theme.StyleMuted.Render(l10Bar))

	// All-time row.
	atPct := int(math.Round(allTimeRate * 100))
	atBar := scoreBar(allTimeRate, barWidth)
	fmt.Println("  " + key("All time") + theme.StyleVal.Render(fmt.Sprintf("%d%%", atPct)) +
		"  " + theme.StyleMuted.Render(atBar))

	// Gate line.
	fmt.Println()
	gateDelta := predictGateThreshold - rolling10Rate
	if gateDelta <= 0 {
		fmt.Println("  " + theme.StyleLogSuccess.Render("✓ Auto-fix gate reached (≥85%)"))
	} else {
		need := int(math.Ceil(gateDelta * 100))
		fmt.Println("  " + key("Gate to auto-fix") +
			theme.StyleMuted.Render(fmt.Sprintf("85%%  (need %d%% more)", need)))
	}

	// Recent sessions table.
	fmt.Println()
	fmt.Println("  " + theme.StyleStep.Render("Recent sessions"))
	fmt.Println()

	display := results
	if len(display) > 5 {
		display = display[len(display)-5:]
	}
	// Reverse so newest is first.
	for i, j := 0, len(display)-1; i < j; i, j = i+1, j-1 {
		display[i], display[j] = display[j], display[i]
	}

	if !isPro && len(display) > 3 {
		display = display[:3]
	}

	for _, r := range display {
		date := r.Date.Local().Format("Jan 02")
		var ratePct int
		if r.Generated > 0 {
			ratePct = int(math.Round(float64(r.Matched) / float64(r.Generated) * 100))
		}
		hitStr := fmt.Sprintf("%d/%d predictions correct  (%d%%)", r.Matched, r.Generated, ratePct)
		fmt.Println("  " + theme.StyleMuted.Render(date) + "  " + theme.StyleVal.Render(hitStr))
	}

	// Pro gate teaser.
	if !isPro {
		fmt.Println()
		fmt.Println("  " + theme.StyleStep.Render("★") + " " +
			theme.StyleMuted.Render("Upgrade to RINSE Pro to unlock the full prediction history."))
		fmt.Println("  " + theme.StyleMuted.Render("  rinse.sh/#pro"))
	}

	fmt.Println()
	fmt.Println("  " + theme.StyleMuted.Render(strings.Repeat("─", 41)))
	dir, _ := SessionsDir()
	fmt.Println("  " + theme.StyleMuted.Render("Events: "+dir))
	fmt.Println()
}
