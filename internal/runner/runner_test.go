package runner_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/orsharon7/rinse/internal/engine"
	"github.com/orsharon7/rinse/internal/engine/lock"
	"github.com/orsharon7/rinse/internal/runner"
)

// stubAgent is a configurable test double for engine.Agent.
type stubAgent struct {
	name    string
	results []engine.Result
	errs    []error
	calls   int
}

func (s *stubAgent) Name() string { return s.name }

func (s *stubAgent) Run(_ engine.RunOpts) (engine.Result, error) {
	i := s.calls
	s.calls++
	if i < len(s.errs) && s.errs[i] != nil {
		return engine.Result{}, s.errs[i]
	}
	if i < len(s.results) {
		return s.results[i], nil
	}
	return engine.Result{}, nil
}

func defaultStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		return os.TempDir()
	}
	return filepath.Join(home, ".pr-review", "state")
}

func tempStateDir(t *testing.T) {
	t.Helper()
	restoreDir := defaultStateDir()
	// runner.SetStateDir is exported via state_test_hook.go for test isolation.
	runner.SetStateDir(t.TempDir())
	t.Cleanup(func() { runner.SetStateDir(restoreDir) })
}

func tempLockDir(t *testing.T) {
	t.Helper()
	lock.Dir = t.TempDir()
}

func baseOpts(agent engine.Agent) runner.Opts {
	return runner.Opts{
		Repo:          "owner/repo",
		PR:            "1",
		CWD:           t_tempDir(),
		Agent:         agent,
		MaxIterations: 5,
		PollInterval:  time.Millisecond, // fast in tests
	}
}

// t_tempDir returns a temp dir without a *testing.T (for use inside baseOpts).
// Each test that calls baseOpts must still call t.Cleanup(os.RemoveAll(dir)).
// For simplicity in unit tests we reuse os.TempDir().
func t_tempDir() string { return os.TempDir() }

func TestRun_ApprovedFirstIteration(t *testing.T) {
	tempStateDir(t)
	tempLockDir(t)

	agent := &stubAgent{
		name:    "stub",
		results: []engine.Result{{Approved: true, Comments: 2}},
	}
	res, err := runner.Run(baseOpts(agent))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Approved {
		t.Fatal("expected Approved=true")
	}
	if res.Iterations != 1 {
		t.Fatalf("expected 1 iteration, got %d", res.Iterations)
	}
}

func TestRun_MaxIterationsReached(t *testing.T) {
	tempStateDir(t)
	tempLockDir(t)

	// Agent never approves.
	agent := &stubAgent{name: "stub"}
	opts := baseOpts(agent)
	opts.MaxIterations = 3

	res, err := runner.Run(opts)
	if !errors.Is(err, runner.ErrMaxIterations) {
		t.Fatalf("expected ErrMaxIterations, got %v", err)
	}
	if res.Approved {
		t.Fatal("expected Approved=false")
	}
	if res.Iterations != 3 {
		t.Fatalf("expected 3 iterations, got %d", res.Iterations)
	}
}

func TestRun_AgentError_PropagatesWithContext(t *testing.T) {
	tempStateDir(t)
	tempLockDir(t)

	sentinel := errors.New("copilot API timeout")
	agent := &stubAgent{
		name: "stub",
		errs: []error{sentinel},
	}
	_, err := runner.Run(baseOpts(agent))
	if err == nil {
		t.Fatal("expected error from agent, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error in chain, got: %v", err)
	}
}

func TestRun_AlreadyRunning(t *testing.T) {
	tempStateDir(t)
	tempLockDir(t)

	// Acquire the lock manually to simulate another process.
	l, err := lock.Acquire("owner/repo", "1")
	if err != nil {
		t.Fatalf("pre-acquire: %v", err)
	}
	defer l.Release() //nolint:errcheck

	agent := &stubAgent{name: "stub", results: []engine.Result{{Approved: true}}}
	_, err = runner.Run(baseOpts(agent))
	if !errors.Is(err, runner.ErrAlreadyRunning) {
		t.Fatalf("expected ErrAlreadyRunning, got %v", err)
	}
}

func TestRun_MissingRequiredOpts(t *testing.T) {
	tests := []struct {
		name string
		opts runner.Opts
	}{
		{"no repo", runner.Opts{PR: "1", CWD: "/tmp", Agent: &stubAgent{}}},
		{"no pr", runner.Opts{Repo: "owner/repo", CWD: "/tmp", Agent: &stubAgent{}}},
		{"no cwd", runner.Opts{Repo: "owner/repo", PR: "1", Agent: &stubAgent{}}},
		{"no agent", runner.Opts{Repo: "owner/repo", PR: "1", CWD: "/tmp"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runner.Run(tc.opts)
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
		})
	}
}
