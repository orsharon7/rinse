// Package onboarding manages first-run state, TOML config, and cycle creation.
package onboarding

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"time"
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

// StatePath returns the path to the onboarding state file:
// <user config dir>/rinse/onboarding-state.json
// where <user config dir> is os.UserConfigDir() (e.g. ~/.config on Linux,
// ~/Library/Application Support on macOS).
func StatePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		// os.UserHomeDir() is safer than os.Getenv("HOME") — it never returns "".
		home, herr := os.UserHomeDir()
		if herr != nil {
			dir = os.TempDir()
		} else {
			dir = filepath.Join(home, ".config")
		}
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

// stepOrdinal returns a numeric ordering for CompletedStep so that
// SaveState can refuse to regress progress.
func stepOrdinal(step Step) int {
	switch step {
	case StepA:
		return 1
	case StepB:
		return 2
	case StepC:
		return 3
	case StepD:
		return 4
	case StepE:
		return 5
	default:
		return 0
	}
}

// stateLockPath returns the path to the advisory lock directory used to
// serialize concurrent SaveState calls across goroutines and processes.
func stateLockPath() string {
	return StatePath() + ".lock"
}

// acquireStateLock acquires a cross-process advisory lock using mkdir atomicity.
// It retries with exponential backoff until the lock is obtained or the timeout
// (5 s) elapses. Callers must call releaseStateLock when done.
func acquireStateLock() error {
	lockDir := stateLockPath()
	deadline := time.Now().Add(5 * time.Second)
	wait := 10 * time.Millisecond
	for {
		err := os.Mkdir(lockDir, 0o700)
		if err == nil {
			return nil // lock acquired
		}
		if !errors.Is(err, os.ErrExist) {
			return err // unexpected error
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for onboarding state lock")
		}
		time.Sleep(wait)
		if wait < 500*time.Millisecond {
			wait *= 2
		}
	}
}

// releaseStateLock releases the advisory lock acquired by acquireStateLock.
func releaseStateLock() {
	_ = os.Remove(stateLockPath())
}

// SaveState writes the state atomically (write-to-temp + rename).
// A unique temp file is used per write so concurrent saves do not clobber
// each other's temp file. SaveState returns any write error; callers are
// responsible for logging or surfacing it.
//
// SaveState is monotonic with respect to CompletedStep: if the on-disk state
// already records a step that is ahead of s.CompletedStep, the higher step is
// preserved so that a stale async write cannot regress onboarding progress.
func SaveState(s State) error {
	// Ensure the parent config directory exists before attempting to acquire the
	// mkdir-based lock, which lives in the same directory. On a fresh install the
	// directory does not exist yet, so os.Mkdir(lockDir) would fail with ENOENT.
	if err := os.MkdirAll(filepath.Dir(StatePath()), 0o755); err != nil {
		return err
	}

	// Serialize concurrent SaveState calls with a cross-process mkdir-based lock
	// so that the monotonic read+compare+write is atomic across goroutines and
	// processes, preventing a slow writer from overwriting a newer step.
	if err := acquireStateLock(); err != nil {
		return err
	}
	defer releaseStateLock()

	// Monotonic guard: never regress CompletedStep. If the on-disk state is
	// already ahead (e.g. a later async write finished first), keep the higher
	// step so stale Bubble Tea commands cannot overwrite newer progress.
	if existing, err := LoadState(); err == nil && existing != nil {
		if stepOrdinal(existing.CompletedStep) > stepOrdinal(s.CompletedStep) {
			// The on-disk state is ahead: preserve its step AND all fields that
			// belong to those later steps so a stale write cannot clobber them.
			s.CompletedStep = existing.CompletedStep
			s.Defaults = existing.Defaults
			s.CycleNameDraft = existing.CycleNameDraft
			s.Skipped = existing.Skipped
		}
	}

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
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return replaceFile(tmp, path)
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

// replaceFile moves src to dst atomically where the OS supports it.
// On Windows, os.Rename fails if dst already exists, so we remove dst first.
// There is a small race window on Windows, but this is the safest portable
// approach without platform-specific syscalls.
func replaceFile(src, dst string) error {
	if runtime.GOOS == "windows" {
		if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.Rename(src, dst)
}
