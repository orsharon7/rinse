package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// stateDir is the base directory for per-PR state files.
// Note: the shell scripts use /tmp/pr-review-state/... while this Go
// implementation uses ~/.pr-review/state/... — they do not share state files.
var stateDir = runnerStateDir()

func runnerStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME") // safe fallback
	}
	return filepath.Join(home, ".pr-review", "state")
}

// PRState is the checkpoint persisted between runner iterations.
// It enables crash recovery and partial-resume: if the process dies mid-cycle,
// the next invocation reads this file and continues from the last known-good point.
type PRState struct {
	// Repo is the "owner/repo" string for this PR.
	Repo string `json:"repo"`

	// PR is the PR number as a string.
	PR string `json:"pr"`

	// Iteration is the number of completed fix iterations so far.
	Iteration int `json:"iteration"`

	// LastReviewID is the GitHub review ID that was current as of the last
	// completed iteration (used to detect new reviews via --last-known).
	LastReviewID string `json:"last_review_id,omitempty"`

	// LastAgentAction records what the agent did in the last iteration
	// ("fixed", "no_comments", "approved", "error").
	LastAgentAction string `json:"last_agent_action,omitempty"`

	// UpdatedAt is the wall-clock time of the last successful checkpoint write.
	UpdatedAt time.Time `json:"updated_at"`
}

// statePath returns the file path for the given repo+PR combination.
func statePath(repo, pr string) string {
	safe := strings.ReplaceAll(repo, "/", "_")
	dir := filepath.Join(stateDir, safe)
	return filepath.Join(dir, pr+".json")
}

// loadState reads the persisted PRState for the given PR.
// Returns a zero PRState (not an error) when no checkpoint exists yet.
func loadState(repo, pr string) (PRState, error) {
	path := statePath(repo, pr)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return PRState{Repo: repo, PR: pr}, nil
	}
	if err != nil {
		return PRState{}, fmt.Errorf("runner: read state %s: %w", path, err)
	}

	var s PRState
	if err := json.Unmarshal(data, &s); err != nil {
		// Corrupt checkpoint — log and start fresh rather than failing hard.
		// This matches the "never assume state" engineering standard.
		return PRState{Repo: repo, PR: pr}, nil
	}
	return s, nil
}

// saveState atomically writes the PRState checkpoint.
// It writes to a temp file in the same directory and renames to avoid
// partial-write corruption.
func saveState(s PRState) error {
	s.UpdatedAt = time.Now().UTC()

	path := statePath(s.Repo, s.PR)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("runner: create state dir: %w", err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("runner: marshal state: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("runner: write state tmp: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("runner: rename state: %w", err)
	}
	return nil
}

// clearState removes the state checkpoint for the given PR (e.g. after approval).
func clearState(repo, pr string) error {
	path := statePath(repo, pr)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("runner: clear state: %w", err)
	}
	return nil
}
