// Package lock provides atomic per-PR file locking for the RINSE engine.
//
// It replaces the bash acquire_dispatch_lock/release_dispatch_lock pattern in
// scripts/pr-review-daemon.sh with a Go implementation that is testable,
// race-free, and integrates cleanly with the engine.Runner lifecycle.
//
// Acquisition is atomic: we create a lock directory with os.Mkdir (which is
// O_EXCL on every OS) and write a metadata file containing the owning PID.
// A lock is considered stale when the recorded PID is no longer alive
// (kill -0 equivalent via os.FindProcess + process.Signal(0)).  Stale locks
// are cleared and re-acquired in a single retry.
//
// Callers must always defer lock.Release() after a successful Acquire.
package lock

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrLocked is returned by Acquire when another live process holds the lock.
var ErrLocked = errors.New("lock: held by another process")

// Dir is the default base directory for lock files.
// It mirrors the DAEMON_LOCK_DIR convention from the shell scripts.
var Dir = func() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".pr-review", "locks")
}()

// Lock represents a per-PR advisory lock on disk.
type Lock struct {
	dir string // full path to the .lock directory (e.g. <Dir>/owner_repo#42.lock)
}

// metadata is persisted inside the lock directory.
type metadata struct {
	PID int `json:"pid"`
}

// keyFor converts repo (owner/repo) + pr number into a filesystem-safe key.
func keyFor(repo, pr string) string {
	safe := strings.ReplaceAll(repo, "/", "_")
	return fmt.Sprintf("%s#%s", safe, pr)
}

// lockDir returns the path to the .lock directory for the given key.
func lockDir(key string) string {
	return filepath.Join(Dir, key+".lock")
}

// Acquire atomically acquires the lock for the given PR.
//
// Returns ErrLocked if another live process holds the lock.
// The caller must call Release (typically via defer) after a successful return.
func Acquire(repo, pr string) (*Lock, error) {
	if err := os.MkdirAll(Dir, 0o755); err != nil {
		return nil, fmt.Errorf("lock: create base dir: %w", err)
	}

	key := keyFor(repo, pr)
	dir := lockDir(key)

	l := &Lock{dir: dir}
	if err := l.tryAcquire(); err != nil {
		return nil, err
	}
	return l, nil
}

// tryAcquire attempts acquisition once, clears a stale lock, then retries.
func (l *Lock) tryAcquire() error {
	if err := os.Mkdir(l.dir, 0o755); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("lock: mkdir: %w", err)
		}
		// Fall through: directory already exists — check whether the owner is alive.
	} else {
		return l.writeMeta()
	}

	// Directory already exists — check whether the owner is alive.
	active, err := l.isActive()
	if err != nil {
		// Can't read metadata; treat as stale.
		_ = os.RemoveAll(l.dir)
	} else if active {
		return ErrLocked
	} else {
		// Stale lock — remove and retry once.
		if err := os.RemoveAll(l.dir); err != nil {
			return fmt.Errorf("lock: remove stale lock: %w", err)
		}
	}

	// Single retry after stale-lock removal.
	if err := os.Mkdir(l.dir, 0o755); err != nil {
		return ErrLocked // lost the race; another process won
	}
	return l.writeMeta()
}

// writeMeta persists the current PID inside the lock directory.
func (l *Lock) writeMeta() error {
	m := metadata{PID: os.Getpid()}
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("lock: marshal metadata: %w", err)
	}
	path := filepath.Join(l.dir, "meta.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		_ = os.RemoveAll(l.dir) // clean up on failure
		return fmt.Errorf("lock: write metadata: %w", err)
	}
	return nil
}

// isActive reports whether the recorded owner PID is still alive.
// Returns (false, nil) for a missing or unparseable metadata file (treat as stale).
func (l *Lock) isActive() (bool, error) {
	path := filepath.Join(l.dir, "meta.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock: read metadata: %w", err)
	}

	var m metadata
	if err := json.Unmarshal(data, &m); err != nil {
		return false, nil // corrupt file → stale
	}
	if m.PID <= 0 {
		return false, nil
	}

	return isProcessAlive(m.PID), nil
}

// Release removes the lock directory, releasing the lock.
// It is a no-op if the lock directory no longer exists (idempotent).
func (l *Lock) Release() error {
	if err := os.RemoveAll(l.dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("lock: release: %w", err)
	}
	return nil
}

// IsHeld reports whether a live process currently holds the lock for this PR.
// It does not acquire the lock.
func IsHeld(repo, pr string) bool {
	key := keyFor(repo, pr)
	dir := lockDir(key)

	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return false
	}

	l := &Lock{dir: dir}
	active, _ := l.isActive()
	return active
}
