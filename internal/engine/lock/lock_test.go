package lock_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/orsharon7/rinse/internal/engine/lock"
)

func TestAcquireRelease(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lock.Dir = dir

	l, err := lock.Acquire("owner/repo", "42")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestAcquireTwice_SameProcess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lock.Dir = dir

	l1, err := lock.Acquire("owner/repo", "1")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer l1.Release() //nolint:errcheck

	// A second acquire from the same process on the same PR should fail because
	// the first lock is still held (our PID is alive).
	_, err = lock.Acquire("owner/repo", "1")
	if err == nil {
		t.Fatal("expected ErrLocked on second Acquire, got nil")
	}
}

func TestAcquireStale(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lock.Dir = dir

	// Manually plant a stale lock with PID=1 (init/launchd — almost certainly
	// alive) would be wrong; use PID=2 on a system where that is long dead.
	// Instead, use a known-dead PID by planting a lock then deleting the meta PID.
	// We simulate a stale lock by writing PID=0 (always invalid).
	key := "owner_repo#99"
	lockDir := filepath.Join(dir, key+".lock")
	if err := os.Mkdir(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := []byte(`{"pid":0}`)
	if err := os.WriteFile(filepath.Join(lockDir, "meta.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}

	// Acquire should detect PID=0 as stale, clean up, and succeed.
	l, err := lock.Acquire("owner/repo", "99")
	if err != nil {
		t.Fatalf("Acquire on stale lock: %v", err)
	}
	defer l.Release() //nolint:errcheck
}

func TestIsHeld(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lock.Dir = dir

	if lock.IsHeld("owner/repo", "7") {
		t.Fatal("expected IsHeld=false before acquire")
	}

	l, err := lock.Acquire("owner/repo", "7")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if !lock.IsHeld("owner/repo", "7") {
		t.Fatal("expected IsHeld=true after acquire")
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if lock.IsHeld("owner/repo", "7") {
		t.Fatal("expected IsHeld=false after release")
	}
}

func TestDifferentPRs_DoNotConflict(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lock.Dir = dir

	l1, err := lock.Acquire("owner/repo", "10")
	if err != nil {
		t.Fatalf("Acquire PR10: %v", err)
	}
	defer l1.Release() //nolint:errcheck

	l2, err := lock.Acquire("owner/repo", "11")
	if err != nil {
		t.Fatalf("Acquire PR11 (different PR, should not conflict): %v", err)
	}
	defer l2.Release() //nolint:errcheck
}
