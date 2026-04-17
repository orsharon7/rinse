// Package onboarding manages first-run state, TOML config, and cycle creation.
package onboarding

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

const StateVersion = 1

// Step represents an onboarding step completion marker.
type Step string

const (
	StepNone Step = ""
	StepA    Step = "A"
	StepB    Step = "B"
	StepC    Step = "C"
	StepD    Step = "D"
	StepE    Step = "E"
)

// Defaults holds user's chosen settings from Step C.
type Defaults struct {
	RemindOnComplete bool `json:"remindOnComplete"`
	AutoAdvance      bool `json:"autoAdvance"`
	SaveHistory      bool `json:"saveHistory"`
}

// DefaultDefaults returns the spec-defined defaults (from defaults-config doc).
func DefaultDefaults() Defaults {
	return Defaults{
		RemindOnComplete: true,
		AutoAdvance:      false,
		SaveHistory:      true,
	}
}

// State is the onboarding state persisted to disk after each step.
type State struct {
	Version        int      `json:"version"`
	CompletedStep  Step     `json:"completedStep"`
	Skipped        bool     `json:"skipped"`
	CycleNameDraft string   `json:"cycleNameDraft,omitempty"`
	Defaults       Defaults `json:"defaults"`
}

// StatePath returns ~/.config/rinse/onboarding-state.json
func StatePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.Getenv("HOME")
	}
	return filepath.Join(dir, "rinse", "onboarding-state.json")
}

// LoadState reads the onboarding state from disk.
// Returns (nil, nil) if the file does not exist (fresh install).
func LoadState() (*State, error) {
	data, err := os.ReadFile(StatePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// SaveState writes the state atomically (write-to-temp + rename).
// A unique temp file is used per write so concurrent saves do not clobber
// each other's temp file. Write failures are returned to the caller so it
// can log or ignore them without blocking UX.
func SaveState(s State) error {
	path := StatePath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	f, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)

	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return err
	}
	if _, err := io.WriteString(f, string(data)); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// DeleteState removes the onboarding state file.
// Called on "Start over" action. On Step E completion, state is updated
// (not deleted) so that IsComplete() continues to return true.
func DeleteState() error {
	err := os.Remove(StatePath())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// IsComplete returns true when onboarding has been fully completed or skipped.
func IsComplete() bool {
	s, err := LoadState()
	if err != nil || s == nil {
		return false
	}
	return s.CompletedStep == StepE || s.Skipped
}
