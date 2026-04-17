// Package stats provides session history recording and summary reporting for rinse.
//
// Sessions are stored as JSON files under ~/.rinse/sessions/ with filenames
// like 20060102-150405-owner-repo-PR42.json. The rinse stats command reads
// all session files, aggregates metrics, and prints a formatted summary.
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

// SessionsDir returns the directory where session JSON files are stored.
func SessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".rinse", "sessions"), nil
}

// Save writes the session as a JSON file in SessionsDir.
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

// Load reads all session files from SessionsDir and returns them ordered
// oldest-first. Files that cannot be parsed are silently skipped.
func Load() ([]Session, error) {
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
}
