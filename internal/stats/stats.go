// Package stats provides session history recording and summary reporting for rinse.
//
// Sessions are stored as JSON files under ~/.rinse/sessions/ with filenames
// like 20060102-150405-owner-repo-PR42-<session_id>.json. The session ID suffix
// prevents collisions when multiple runs start within the same second. The rinse
// stats command reads all session files, aggregates metrics, and prints a
// formatted summary.
package stats

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	mrand "math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/orsharon7/rinse/internal/quality"
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

// newUUID generates a UUID v4 string.
// If cryptographic randomness is unavailable, it falls back to
// non-cryptographic bytes while still returning a syntactically valid UUID
// so stats recording remains best-effort.
func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		now := time.Now().UTC().UnixNano()
		pid := int64(os.Getpid())
		r := mrand.New(mrand.NewSource(now ^ (pid << 32))) //nolint:gosec
		for i := range b {
			b[i] = byte(r.Intn(256))
		}
	}
	// Set version 4 (bits 12-15 of byte 6 to 0100)
	b[6] = (b[6] & 0x0f) | 0x40
	// Set variant bits (bits 6-7 of byte 8 to 10)
	b[8] = (b[8] & 0x3f) | 0x80
	// Convert each group to a fixed-size integer so that width/zero-padding
	// verbs apply to numeric values, not to the slice representation.
	// (Using %x on a []byte produces the correct hex string, but %08x width
	// padding applies to the whole slice value and is not portable.)
	g0 := binary.BigEndian.Uint32(b[0:4])
	g1 := binary.BigEndian.Uint16(b[4:6])
	g2 := binary.BigEndian.Uint16(b[6:8])
	g3 := binary.BigEndian.Uint16(b[8:10])
	// Last group is 48 bits; encode as uint64 read from the 6 bytes.
	g4 := uint64(b[10])<<40 | uint64(b[11])<<32 | uint64(b[12])<<24 |
		uint64(b[13])<<16 | uint64(b[14])<<8 | uint64(b[15])
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", g0, g1, g2, g3, g4)
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
	Patterns                   []string `json:"patterns,omitempty"`

	// Quality metrics (populated when available)
	Quality *quality.QualityDelta `json:"quality,omitempty"`
}

// NewSession creates a new Session with a generated UUID and the current time
// as StartedAt.
func NewSession(repo, pr, runner, model string) Session {
	return Session{
		SessionID:                  newUUID(),
		StartedAt:                  time.Now().UTC(),
		Repo:                       repo,
		PR:                         pr,
		Runner:                     runner,
		Model:                      model,
		CopilotCommentsByIteration: []int{},
	}
}

// DurationSeconds returns the session duration in seconds.
func (s Session) DurationSeconds() float64 {
	return s.EndedAt.Sub(s.StartedAt).Seconds()
}

// ComputeQuality computes and stores quality metrics from the per-iteration
// comment counts. Call this after CopilotCommentsByIteration is fully
// populated and before saving the session.
func (s *Session) ComputeQuality() {
	if len(s.CopilotCommentsByIteration) == 0 {
		return
	}
	delta := quality.Compute(s.CopilotCommentsByIteration,
		quality.CategoryCounts{}, // category data not available from shell logs
		quality.CategoryCounts{})
	s.Quality = &delta
}

// Finish stamps EndedAt, derives TotalComments from CopilotCommentsByIteration
// if not already set, and computes EstimatedTimeSavedSeconds.
// estimatedSecondsPerComment defaults to 240 (4 min) when <= 0.
func (s *Session) Finish(outcome Outcome, estimatedSecondsPerComment int) {
	s.EndedAt = time.Now().UTC()
	s.Outcome = outcome
	s.Approved = outcome == OutcomeApproved || outcome == OutcomeMerged

	// Derive TotalComments from per-iteration slice if not set explicitly.
	if s.TotalComments == 0 && len(s.CopilotCommentsByIteration) > 0 {
		for _, c := range s.CopilotCommentsByIteration {
			s.TotalComments += c
		}
	}

	if estimatedSecondsPerComment <= 0 {
		estimatedSecondsPerComment = 240
	}
	s.EstimatedTimeSavedSeconds = s.TotalComments * estimatedSecondsPerComment
}

// UnmarshalJSON keeps Approved consistent when older or shell-written session
// files omit the explicit "approved" field and only persist "outcome".
func (s *Session) UnmarshalJSON(data []byte) error {
	type sessionAlias Session
	aux := struct {
		sessionAlias
		Approved *bool `json:"approved"`
	}{}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	*s = Session(aux.sessionAlias)
	if aux.Approved != nil {
		s.Approved = *aux.Approved
	} else {
		s.Approved = s.Outcome == OutcomeApproved || s.Outcome == OutcomeMerged
	}

	return nil
}

// MarshalJSON ensures Go-written session files always emit an "approved" value
// that is consistent with the outcome. When Outcome is empty (e.g. older
// shell-written files that only set "approved"), the existing Approved value
// is preserved so re-marshaling never corrupts it.
func (s Session) MarshalJSON() ([]byte, error) {
	type sessionAlias Session
	alias := sessionAlias(s)
	if s.Outcome != "" {
		alias.Approved = s.Outcome == OutcomeApproved || s.Outcome == OutcomeMerged
	}

	return json.Marshal(alias)
}

// SessionsDir returns the directory where session JSON files are stored.
// The RINSE_SESSIONS_DIR environment variable overrides the default location,
// which is useful for test isolation.
func SessionsDir() (string, error) {
	if override := os.Getenv("RINSE_SESSIONS_DIR"); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".rinse", "sessions"), nil
}

// Save writes the session as a JSON file in SessionsDir.
// The filename includes the session ID to avoid collisions when multiple runs
// start within the same second. The file is written via a temp file and
// atomically renamed so readers never see a partial write.
func Save(s Session) error {
	dir, err := SessionsDir()
	if err != nil {
		return fmt.Errorf("stats: cannot determine sessions dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("stats: cannot create sessions dir: %w", err)
	}

	if s.SessionID == "" {
		s.SessionID = newUUID()
	}

	repoSlug := strings.ReplaceAll(s.Repo, "/", "-")
	fname := fmt.Sprintf("%s-%s-PR%s-%s.json",
		s.StartedAt.Format("20060102-150405"),
		repoSlug,
		s.PR,
		s.SessionID,
	)
	path := filepath.Join(dir, fname)

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("stats: cannot marshal session: %w", err)
	}

	// Write atomically: create a uniquely-named temp file in the same directory,
	// write to it, then rename to the final path to avoid clobbering concurrent
	// saves or leaving a stale .tmp on error.
	tmp, err := os.CreateTemp(dir, fname+".tmp-*")
	if err != nil {
		return fmt.Errorf("stats: cannot create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, werr := tmp.Write(data); werr != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("stats: cannot write temp file: %w", werr)
	}
	if cerr := tmp.Close(); cerr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("stats: cannot close temp file: %w", cerr)
	}
	if rerr := os.Rename(tmpPath, path); rerr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("stats: cannot rename temp file: %w", rerr)
	}
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
		sessions = append(sessions, s)
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].StartedAt.Before(sessions[j].StartedAt)
	})
	return sessions, nil
}

// Summary holds aggregated metrics across a set of sessions.
type Summary struct {
	TotalSessions         int
	TotalComments         int
	TotalIterations       int
	ApprovedSessions      int
	TotalDurationSec      float64
	TotalTimeSavedSeconds int
	PatternCounts         map[string]int
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
		sum := Summary{PatternCounts: make(map[string]int)}
		for _, s := range ss {
			sum.TotalSessions++
			sum.TotalComments += s.TotalComments
			sum.TotalIterations += s.Iterations
			sum.TotalDurationSec += s.DurationSeconds()
			// Accumulate time-saved using the per-session precomputed value when
			// available, falling back to 240 s/comment for older session files.
			if s.EstimatedTimeSavedSeconds > 0 {
				sum.TotalTimeSavedSeconds += s.EstimatedTimeSavedSeconds
			} else {
				sum.TotalTimeSavedSeconds += s.TotalComments * 240
			}
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
		end := now.AddDate(0, 0, -7*weeksAgo)
		weeks[i].label = start.Format("Jan 02")
		_ = end
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
