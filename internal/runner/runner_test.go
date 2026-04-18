package runner

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/orsharon7/rinse/internal/engine"
	"github.com/orsharon7/rinse/internal/engine/lock"
	"github.com/orsharon7/rinse/internal/stats"
)

// stubAgent is a configurable test double for engine.Agent.
type stubAgent struct {
	name        string
	results     []engine.Result
	errs        []error
	calls       int
	capturedOpts []engine.RunOpts
}

func (s *stubAgent) Name() string { return s.name }

func (s *stubAgent) Run(opts engine.RunOpts) (engine.Result, error) {
	s.capturedOpts = append(s.capturedOpts, opts)
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

func tempStateDir(t *testing.T) {
	t.Helper()

	// Capture the current value before overriding so we always restore it,
	// regardless of whether a home directory can be determined.
	restoreDir := GetStateDir()

	// SetStateDir is defined in state_test_hook_test.go for test isolation.
	SetStateDir(t.TempDir())
	t.Cleanup(func() {
		SetStateDir(restoreDir)
	})
}

func tempLockDir(t *testing.T) {
	t.Helper()
	lock.Dir = t.TempDir()
}

func tempSessionsDir(t *testing.T) {
	t.Helper()
	t.Setenv("RINSE_SESSIONS_DIR", t.TempDir())
	t.Setenv("RINSE_STATS_OPTIN", "1")
}

func baseOpts(agent engine.Agent) Opts {
	return Opts{
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
	tempSessionsDir(t)

	agent := &stubAgent{
		name:    "stub",
		results: []engine.Result{{Approved: true, Comments: 2}},
	}
	res, err := Run(baseOpts(agent))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Approved {
		t.Fatal("expected Approved=true")
	}
	if res.Iterations != 1 {
		t.Fatalf("expected 1 iteration, got %d", res.Iterations)
	}
	// Session field assertions.
	if res.Session.Outcome != stats.OutcomeApproved {
		t.Fatalf("expected session Outcome=approved, got %q", res.Session.Outcome)
	}
	if res.Session.TotalComments != 2 {
		t.Fatalf("expected session TotalComments=2, got %d", res.Session.TotalComments)
	}
	if len(res.Session.CopilotCommentsByIteration) != 1 {
		t.Fatalf("expected 1 entry in CopilotCommentsByIteration, got %d", len(res.Session.CopilotCommentsByIteration))
	}
	if res.Session.CopilotCommentsByIteration[0] != 2 {
		t.Fatalf("expected CopilotCommentsByIteration[0]=2, got %d", res.Session.CopilotCommentsByIteration[0])
	}
	// Assert exactly one session file was written to the temp sessions dir.
	sessionsDir := os.Getenv("RINSE_SESSIONS_DIR")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		t.Fatalf("reading sessions dir: %v", err)
	}
	var jsonFiles []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && len(e.Name()) > 5 && e.Name()[len(e.Name())-5:] == ".json" {
			jsonFiles = append(jsonFiles, e)
		}
	}
	if len(jsonFiles) != 1 {
		t.Fatalf("expected exactly 1 session file, got %d", len(jsonFiles))
	}
}

func TestRun_MaxIterationsReached(t *testing.T) {
	tempStateDir(t)
	tempLockDir(t)
	tempSessionsDir(t)

	// Agent never approves.
	agent := &stubAgent{name: "stub"}
	opts := baseOpts(agent)
	opts.MaxIterations = 3

	res, err := Run(opts)
	if !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("expected ErrMaxIterations, got %v", err)
	}
	if res.Approved {
		t.Fatal("expected Approved=false")
	}
	if res.Iterations != 3 {
		t.Fatalf("expected 3 iterations, got %d", res.Iterations)
	}
	if res.Session.Outcome != stats.OutcomeMaxIter {
		t.Fatalf("expected session Outcome=%q, got %q", stats.OutcomeMaxIter, res.Session.Outcome)
	}
	// Assert exactly one session file was written to the temp sessions dir.
	sessionsDir := os.Getenv("RINSE_SESSIONS_DIR")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		t.Fatalf("reading sessions dir: %v", err)
	}
	var jsonFiles []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && len(e.Name()) > 5 && e.Name()[len(e.Name())-5:] == ".json" {
			jsonFiles = append(jsonFiles, e)
		}
	}
	if len(jsonFiles) != 1 {
		t.Fatalf("expected exactly 1 session file, got %d", len(jsonFiles))
	}
}

func TestRun_AgentError_PropagatesWithContext(t *testing.T) {
	tempStateDir(t)
	tempLockDir(t)
	tempSessionsDir(t)

	sentinel := errors.New("copilot API timeout")
	agent := &stubAgent{
		name: "stub",
		errs: []error{sentinel},
	}
	_, err := Run(baseOpts(agent))
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
	tempSessionsDir(t)

	// Acquire the lock manually to simulate another process.
	l, err := lock.Acquire("owner/repo", "1")
	if err != nil {
		t.Fatalf("pre-acquire: %v", err)
	}
	defer l.Release() //nolint:errcheck

	agent := &stubAgent{name: "stub", results: []engine.Result{{Approved: true}}}
	_, err = Run(baseOpts(agent))
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("expected ErrAlreadyRunning, got %v", err)
	}
}

func TestRun_MissingRequiredOpts(t *testing.T) {
	tests := []struct {
		name string
		opts Opts
	}{
		{"no repo", Opts{PR: "1", CWD: "/tmp", Agent: &stubAgent{}}},
		{"no pr", Opts{Repo: "owner/repo", CWD: "/tmp", Agent: &stubAgent{}}},
		{"no cwd", Opts{Repo: "owner/repo", PR: "1", Agent: &stubAgent{}}},
		{"no agent", Opts{Repo: "owner/repo", PR: "1", CWD: "/tmp"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Run(tc.opts)
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
		})
	}
}

// TestRun_WaitingDoesNotCountAsIteration verifies that a Waiting result from
// the agent is not counted toward MaxIterations, and that the loop eventually
// exits via approval after waiting iterations.
func TestRun_WaitingDoesNotCountAsIteration(t *testing.T) {
	tempStateDir(t)
	tempLockDir(t)
	tempSessionsDir(t)

	// First two calls return Waiting, third call approves.
	agent := &stubAgent{
		name: "stub",
		results: []engine.Result{
			{Waiting: true},
			{Waiting: true},
			{Approved: true},
		},
	}
	opts := baseOpts(agent)
	opts.MaxIterations = 1 // only 1 real iteration allowed

	res, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Approved {
		t.Fatal("expected Approved=true")
	}
	// Waiting results must not increment Iterations.
	if res.Iterations != 1 {
		t.Fatalf("expected 1 iteration (Waiting calls excluded), got %d", res.Iterations)
	}
}

// TestRun_ReviewIDPassedOnSubsequentCall verifies that after a non-waiting
// iteration sets Result.ReviewID, the runner passes it as LastKnownReviewID
// on the next agent invocation.
func TestRun_ReviewIDPassedOnSubsequentCall(t *testing.T) {
	tempStateDir(t)
	tempLockDir(t)
	tempSessionsDir(t)

	const wantReviewID = "review-abc-123"

	// First call: returns a ReviewID but no approval, second call: approves.
	agent := &stubAgent{
		name: "stub",
		results: []engine.Result{
			{Comments: 1, ReviewID: wantReviewID},
			{Approved: true},
		},
	}
	opts := baseOpts(agent)
	opts.MaxIterations = 5

	res, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Approved {
		t.Fatal("expected Approved=true")
	}
	if agent.calls != 2 {
		t.Fatalf("expected 2 agent calls, got %d", agent.calls)
	}
	// The second call must have received the ReviewID from the first iteration.
	if got := agent.capturedOpts[1].LastKnownReviewID; got != wantReviewID {
		t.Fatalf("expected LastKnownReviewID=%q on second call, got %q", wantReviewID, got)
	}
}

// TestRun_MaxWaitPollsReached verifies that when the agent returns
// Result{Waiting:true} more than MaxWaitPolls times, Run returns
// ErrMaxWaitPolls and does not advance Iterations.
func TestRun_MaxWaitPollsReached(t *testing.T) {
	tempStateDir(t)
	tempLockDir(t)
	tempSessionsDir(t)

	// Agent always returns Waiting.
	agent := &stubAgent{
		name: "stub",
		results: []engine.Result{
			{Waiting: true},
			{Waiting: true},
			{Waiting: true},
		},
	}
	opts := baseOpts(agent)
	opts.MaxWaitPolls = 2 // exceeded after 3 Waiting results

	res, err := Run(opts)
	if !errors.Is(err, ErrMaxWaitPolls) {
		t.Fatalf("expected ErrMaxWaitPolls, got %v", err)
	}
	if res.Iterations != 0 {
		t.Fatalf("expected Iterations=0 (Waiting must not advance), got %d", res.Iterations)
	}
}

// TestRun_AgentError_WritesSessionFile verifies that when the agent returns a
// hard error, a session file is written with Outcome=error.
func TestRun_AgentError_WritesSessionFile(t *testing.T) {
	tempStateDir(t)
	tempLockDir(t)
	tempSessionsDir(t)

	sentinel := errors.New("hard agent failure")
	agent := &stubAgent{
		name: "stub",
		errs: []error{sentinel},
	}
	res, err := Run(baseOpts(agent))
	if err == nil {
		t.Fatal("expected error from agent, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error in chain, got: %v", err)
	}
	if res.Session.Outcome != stats.OutcomeError {
		t.Fatalf("expected session Outcome=%q, got %q", stats.OutcomeError, res.Session.Outcome)
	}

	// Assert a session file was written with the correct outcome.
	sessionsDir := os.Getenv("RINSE_SESSIONS_DIR")
	entries, err2 := os.ReadDir(sessionsDir)
	if err2 != nil {
		t.Fatalf("reading sessions dir: %v", err2)
	}
	var jsonFiles []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && len(e.Name()) > 5 && e.Name()[len(e.Name())-5:] == ".json" {
			jsonFiles = append(jsonFiles, e)
		}
	}
	if len(jsonFiles) != 1 {
		t.Fatalf("expected exactly 1 session file for agent error exit, got %d", len(jsonFiles))
	}
}

// TestRun_MaxWaitPollsAborted_WritesSessionFile verifies that when the max-wait
// poll limit is exceeded, a session file is written with Outcome=aborted.
func TestRun_MaxWaitPollsAborted_WritesSessionFile(t *testing.T) {
	tempStateDir(t)
	tempLockDir(t)
	tempSessionsDir(t)

	agent := &stubAgent{
		name: "stub",
		results: []engine.Result{
			{Waiting: true},
			{Waiting: true},
			{Waiting: true},
		},
	}
	opts := baseOpts(agent)
	opts.MaxWaitPolls = 2 // exceeded after 3 Waiting results

	res, err := Run(opts)
	if !errors.Is(err, ErrMaxWaitPolls) {
		t.Fatalf("expected ErrMaxWaitPolls, got %v", err)
	}
	if res.Session.Outcome != stats.OutcomeAborted {
		t.Fatalf("expected session Outcome=%q, got %q", stats.OutcomeAborted, res.Session.Outcome)
	}

	// Assert a session file was written with the correct outcome.
	sessionsDir := os.Getenv("RINSE_SESSIONS_DIR")
	entries, err2 := os.ReadDir(sessionsDir)
	if err2 != nil {
		t.Fatalf("reading sessions dir: %v", err2)
	}
	var jsonFiles []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && len(e.Name()) > 5 && e.Name()[len(e.Name())-5:] == ".json" {
			jsonFiles = append(jsonFiles, e)
		}
	}
	if len(jsonFiles) != 1 {
		t.Fatalf("expected exactly 1 session file for aborted exit, got %d", len(jsonFiles))
	}
}
