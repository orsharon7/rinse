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

// sessionsDir returns the directory where sessions are persisted.
func sessionsDir() (string, error) {
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
	name := fmt.Sprintf("%s-pr%s-%s.json", safe, s.PR, ts)
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

	status := "complete"
	if s.Approved {
		status = "approved ✓"
	}

	saved := s.TimeSaved()
	var savedStr string
	if saved == 0 {
		savedStr = "—"
	} else {
		savedStr = fmt.Sprintf("~%d min", int(saved.Minutes()))
	}

	commentsStr := fmt.Sprintf("%d", s.TotalComments)
	if len(s.CommentsByRound) > 1 {
		parts := make([]string, len(s.CommentsByRound))
		for i, c := range s.CommentsByRound {
			parts[i] = fmt.Sprintf("%d", c)
		}
		commentsStr += fmt.Sprintf(" across %d Copilot review rounds", len(s.CommentsByRound))
	} else if len(s.CommentsByRound) == 1 {
		commentsStr += " in 1 Copilot review round"
	}

	fmt.Println()
	fmt.Printf("RINSE %s — PR #%s\n", status, s.PR)
	fmt.Println()
	fmt.Printf("  %-16s %s\n", "Time saved:", savedStr)
	fmt.Printf("  %-16s %s\n", "Comments fixed:", commentsStr)
	fmt.Printf("  %-16s %d\n", "Iterations:", s.Iterations)

	if len(s.Patterns) > 0 {
		top := s.Patterns
		if len(top) > 3 {
			top = top[:3]
		}
		fmt.Printf("  %-16s %s\n", "Top patterns:", strings.Join(top, ", "))
	}

	fmt.Println()
	fmt.Println("  Run `rinse stats` to see your history.")
	fmt.Println()
}

// PrintStats renders an aggregate statistics table for a slice of sessions.
func PrintStats(sessions []Session) {
	if len(sessions) == 0 {
		fmt.Println("No RINSE sessions recorded yet.")
		fmt.Println("Run `rinse` to start your first cycle.")
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

	fmt.Println()
	fmt.Println("RINSE — session history")
	fmt.Println()
	fmt.Printf("  %-22s %d\n", "Total sessions:", len(sessions))
	fmt.Printf("  %-22s %d / %d\n", "Approved:", approvals, len(sessions))
	fmt.Printf("  %-22s %d\n", "Total comments fixed:", totalComments)
	fmt.Printf("  %-22s ~%d min\n", "Total time saved:", int(totalSaved.Minutes()))
	fmt.Printf("  %-22s %d\n", "Total iterations:", totalIterations)
	fmt.Println()
	fmt.Println("  Recent sessions:")
	fmt.Println()

	recent := sessions
	if len(recent) > 10 {
		recent = recent[len(recent)-10:]
	}
	// Reverse to show newest first.
	for i, j := 0, len(recent)-1; i < j; i, j = i+1, j-1 {
		recent[i], recent[j] = recent[j], recent[i]
	}
	for _, s := range recent {
		approved := " "
		if s.Approved {
			approved = "✓"
		}
		date := s.StartedAt.Local().Format("2006-01-02")
		saved := int(s.TimeSaved().Minutes())
		fmt.Printf("  %s  %s  PR #%-6s  %2d comments  ~%d min saved\n",
			approved, date, s.PR, s.TotalComments, saved)
	}
	fmt.Println()
}
