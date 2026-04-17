// Package stats provides session history recording and summary reporting for rinse.
//
// Sessions are stored as JSON files under ~/.rinse/sessions/ with filenames
// like 20060102-150405-000000000-owner-repo-PR42.json (date, time, nanoseconds,
// repo slug with slashes replaced by dashes, and PR number). The rinse stats command reads
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

// SchemaVersion is the current on-disk schema version. Increment when making
// backward-incompatible changes to Session.
const SchemaVersion = 1

// Outcome describes the result of a single rinse run.
type Outcome string

const (
	OutcomeApproved Outcome = "approved"
	OutcomeClean    Outcome = "clean"
	OutcomeMerged   Outcome = "merged"
	OutcomeClosed   Outcome = "closed"
	OutcomeMaxIter  Outcome = "max_iter"
	OutcomeError    Outcome = "error"
	OutcomeAborted  Outcome = "aborted"
	OutcomeDryRun   Outcome = "dry_run"
)

// Session records the outcome of a single rinse PR-review run.
type Session struct {
	SchemaVersion int     `json:"schema_version"`
	StartedAt     time.Time `json:"started_at"`
	EndedAt       time.Time `json:"ended_at"`
	Repo          string    `json:"repo"`
	PR            string    `json:"pr"`
	Runner        string    `json:"runner"`
	Model         string    `json:"model"`

	// Outcomes
	TotalComments int      `json:"total_comments"`
	Iterations    int      `json:"iterations"`
	Outcome       Outcome  `json:"outcome"`
	Patterns      []string `json:"patterns,omitempty"`

	// LegacyApproved is a read-only migration field for pre-Outcome session files.
	// When Outcome is empty and LegacyApproved is true, Outcome is normalised to
	// OutcomeApproved during Load so that existing on-disk files continue to count.
	LegacyApproved *bool `json:"approved,omitempty"`
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

// configDir returns the directory for rinse config files.
// Uses os.UserConfigDir() (same root as internal/config/config.go) to avoid
// a second config root at ~/.rinse.
func configDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "rinse"), nil
}

// config holds user preferences persisted in stats.json under configDir().
type config struct {
	StatsOptIn *bool `json:"stats_opt_in,omitempty"`
}

func loadConfig() (config, error) {
	dir, err := configDir()
	if err != nil {
		return config{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "stats.json"))
	if os.IsNotExist(err) {
		return config{}, nil
	}
	if err != nil {
		return config{}, err
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func saveConfig(cfg config) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	configPath := filepath.Join(dir, "stats.json")
	tmpFile, err := os.CreateTemp(dir, "stats.json.tmp-*")
	if err != nil {
		return err
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
		return err
	}
	if err := tmpFile.Chmod(0o644); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, configPath); err != nil {
		return err
	}

	success = true
	return nil
}

// IsOptedIn reports whether the user has opted in to stats collection.
// Returns (false, nil) when no preference has been set yet.
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

// SetOptIn persists the user's stats opt-in preference.
func SetOptIn(optIn bool) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	cfg.StatsOptIn = &optIn
	return saveConfig(cfg)
}

// PromptOptIn prints a privacy notice and asks the user to opt in.
// It is a no-op in non-interactive (CI/non-TTY) environments.
// Returns true if the user opted in.
func PromptOptIn() (bool, error) {
	// Only prompt in interactive terminals.
	fi, err := os.Stdin.Stat()
	if err != nil || (fi.Mode()&os.ModeCharDevice) == 0 {
		return false, nil
	}

	sessionsDir, err := SessionsDir()
	if err != nil {
		sessionsDir = "~/.rinse/sessions"
	}
	fmt.Printf(`
  RINSE Stats — Privacy Notice
  ─────────────────────────────────────────────────────
  Rinse can record per-run telemetry locally to power
  the Pro dashboard (PRs reviewed, comments fixed,
  time saved, top patterns).

  Data is stored ONLY on this machine at:
    %s

  Nothing is sent to any server.
  You can opt out at any time with: rinse opt-out
  ─────────────────────────────────────────────────────`+"\n", sessionsDir)
	fmt.Print("  Enable local stats? [y/N]: ")

	var resp string
	if _, err := fmt.Scanln(&resp); err != nil {
		// Read failed (EOF / closed terminal) — treat as no response without persisting any preference.
		return false, nil
	}
	optIn := strings.ToLower(strings.TrimSpace(resp)) == "y"
	if err := SetOptIn(optIn); err != nil {
		return false, err
	}
	return optIn, nil
}

// Save writes the session as a JSON file in SessionsDir.
// It silently skips saving if the user has not opted in, and prompts
// on first interactive run when no preference is set.
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
	fname := fmt.Sprintf("%s-%09d-%s-PR%s.json",
		s.StartedAt.Format("20060102-150405"),
		s.StartedAt.Nanosecond(),
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
		// Backward-compat: migrate legacy Approved bool to Outcome.
		if s.Outcome == "" {
			if s.LegacyApproved != nil && *s.LegacyApproved {
				s.Outcome = OutcomeApproved
			} else {
				s.Outcome = OutcomeClean
			}
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
	OutcomeCounts    map[Outcome]int
	// Last30Days is a filtered summary over the last 30 days.
	// It is always populated by Summarize and is the zero value when no sessions match.
	Last30Days Summary
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
	sum.Last30Days = build(recent)
	return sum
}

// Print writes a formatted stats report to stdout.
func Print(sessions []Session) {
	sum := Summarize(sessions)

	display := sum.Last30Days
	label := "last 30 days"

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
}
