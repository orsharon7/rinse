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
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/orsharon7/rinse/internal/db"
	"github.com/orsharon7/rinse/internal/upgrade"
)

// Session records the outcome of a single rinse PR-review run.
type Session struct {
	// Metadata
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Repo      string    `json:"repo"`
	PR        string    `json:"pr"`
	Runner    string    `json:"runner"`
	Model     string    `json:"model"`

	// Outcomes
	TotalComments int      `json:"total_comments"`
	Iterations    int      `json:"iterations"`
	Approved      bool     `json:"approved"`
	Patterns      []string `json:"patterns,omitempty"`
}

// DurationSeconds returns the session duration in seconds.
func (s Session) DurationSeconds() float64 {
	return s.EndedAt.Sub(s.StartedAt).Seconds()
}

// SessionsDir returns the directory where legacy session JSON files are stored.
func SessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".rinse", "sessions"), nil
}

// Save writes the session as a JSON file in SessionsDir (legacy path, used by
// shell scripts). New code should write to the SQLite DB instead.
func Save(s Session) error {
	dir, err := SessionsDir()
	if err != nil {
		return fmt.Errorf("stats: cannot determine sessions dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("stats: cannot create sessions dir: %w", err)
	}

	repoSlug := strings.ReplaceAll(s.Repo, "/", "-")
	fname := fmt.Sprintf("%s-%s-PR%s.json",
		s.StartedAt.Format("20060102-150405"),
		repoSlug,
		s.PR,
	)
	path := filepath.Join(dir, fname)

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("stats: cannot marshal session: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
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
		return nil, err
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
		sessions = append(sessions, s)
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].StartedAt.Before(sessions[j].StartedAt)
	})
	return sessions, nil
}

// Summary holds aggregated metrics across a set of sessions.
type Summary struct {
	TotalSessions    int
	TotalComments    int
	TotalIterations  int
	ApprovedSessions int
	TotalDurationSec float64
	PatternCounts    map[string]int
	// Last30Days is a filtered summary over the last 30 days.
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
// Assumes each comment would take a developer ~3 minutes to address manually.
func (s *Summary) EstTimeSavedHours() float64 {
	return math.Round(float64(s.TotalComments)*3/60*10) / 10
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
		sum := Summary{PatternCounts: make(map[string]int)}
		for _, s := range ss {
			sum.TotalSessions++
			sum.TotalComments += s.TotalComments
			sum.TotalIterations += s.Iterations
			sum.TotalDurationSec += s.DurationSeconds()
			if s.Approved {
				sum.ApprovedSessions++
			}
			for _, p := range s.Patterns {
				sum.PatternCounts[p]++
			}
		}
		return sum
	}

	sum := build(all)
	if len(recent) < len(all) {
		r := build(recent)
		sum.Last30Days = &r
	}
	return sum
}

// Print writes a formatted stats report to stdout.
func Print(sessions []Session) {
	sum := Summarize(sessions)

	display := sum
	label := "all time"
	if sum.Last30Days != nil {
		display = *sum.Last30Days
		label = "last 30 days"
	}

	fmt.Printf("\n  RINSE Stats (%s)\n", label)
	fmt.Printf("  PRs reviewed:     %d\n", display.TotalSessions)
	fmt.Printf("  Comments fixed:   %d\n", display.TotalComments)
	fmt.Printf("  Avg iterations:   %.1f\n", display.AvgIterations())
	fmt.Printf("  Est. time saved:  ~%.1f hours\n", display.EstTimeSavedHours())

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
		totalMin := sum.TotalComments * 3
		fmt.Println(upgrade.RenderPrompt(totalMin, sum.TotalSessions))
		upgrade.RecordShown(sum.TotalSessions)
	}
}
