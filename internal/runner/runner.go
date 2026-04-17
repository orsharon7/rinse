// Package runner implements the core PR review cycle loop that drives agents
// until Copilot approves or max iterations are reached.
// This package replaces the shell scripts in scripts/ over time.
package runner

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/orsharon7/rinse/internal/engine"
	"github.com/orsharon7/rinse/internal/engine/lock"
	"github.com/orsharon7/rinse/internal/stats"
)

// DefaultMaxIterations is used when Opts.MaxIterations is 0.
const DefaultMaxIterations = 10

// ErrMaxIterations is returned when the runner exits because it reached the
// configured iteration limit without Copilot approval.
var ErrMaxIterations = errors.New("runner: max iterations reached without approval")

// ErrAlreadyRunning is returned when another process is actively running the
// review cycle for the same PR.
var ErrAlreadyRunning = errors.New("runner: another process is already running for this PR")

// ErrMaxWaitPolls is returned when the runner has polled for an actionable
// review more than MaxWaitPolls times without receiving one.
var ErrMaxWaitPolls = errors.New("runner: max wait polls reached without actionable review")

// Opts carries all configuration for a single PR's review lifecycle.
type Opts struct {
	// Repo is the "owner/repo" string (e.g. "orsharon7/rinse").
	Repo string

	// PR is the pull request number as a string.
	PR string

	// PRTitle is the PR title used for session recording (optional).
	PRTitle string

	// CWD is the local working directory for the repository checkout.
	CWD string

	// Model is an optional model override forwarded to the Agent.
	Model string

	// MaxIterations caps the number of fix-and-review cycles.
	// Defaults to DefaultMaxIterations when 0.
	MaxIterations int

	// MaxWaitPolls caps the number of consecutive "waiting" polls before Run
	// gives up waiting for an actionable review. Defaults to 60 (30 min at the
	// default 30 s PollInterval).
	MaxWaitPolls int

	// PollInterval is how long to wait between Copilot review status checks.
	// Defaults to 30s when zero.
	PollInterval time.Duration

	// Agent is the engine.Agent implementation to drive.
	Agent engine.Agent

	// Logger is an optional structured logger. Falls back to slog.Default().
	Logger *slog.Logger
}

// Result summarises the outcome of the complete run loop.
type Result struct {
	// Approved reports whether Copilot approved the PR.
	Approved bool

	// Iterations is the number of fix cycles that were executed.
	Iterations int

	// ResumedFromIteration is non-zero when the run resumed from a checkpoint.
	ResumedFromIteration int

	// Session is the recorded session metrics for this run.
	Session stats.Session
}

// Run drives the PR review lifecycle:
//
//  1. Acquire a per-PR on-disk lock (atomic, stale-lock aware).
//  2. Load any existing checkpoint (crash recovery / partial resume).
//  3. Loop: invoke Agent, checkpoint state, push & re-request review.
//  4. Exit when Copilot approves, PR is merged/closed, or max iterations reached.
//  5. Clear the checkpoint on terminal outcomes.
//
// Run honours the "never swallow errors" engineering standard: subprocess
// errors are propagated with context rather than swallowed.
//
// A stats.Session is created early and persisted to ~/.rinse/sessions/ on
// most exit paths. Early exits such as lock acquisition failure or
// checkpoint load failure do not persist a session record.
func Run(opts Opts) (Result, error) {
	if err := validateOpts(&opts); err != nil {
		return Result{}, err
	}

	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}

	// ── 0. Start session recording ────────────────────────────────────────────
	session := stats.NewSession(opts.Repo, opts.PR, opts.Agent.Name(), opts.Model)
	session.PRTitle = opts.PRTitle

	persistSession := func(outcome stats.Outcome) {
		session.Finish(outcome, 240)
		if err := stats.Save(session); err != nil {
			log.Error("runner: save session", "error", err)
		}
	}

	// ── 1. Acquire lock ──────────────────────────────────────────────────────
	l, err := lock.Acquire(opts.Repo, opts.PR)
	if err != nil {
		if errors.Is(err, lock.ErrLocked) {
			return Result{}, ErrAlreadyRunning
		}
		return Result{}, fmt.Errorf("runner: acquire lock: %w", err)
	}
	defer func() {
		if releaseErr := l.Release(); releaseErr != nil {
			log.Error("runner: release lock", "error", releaseErr)
		}
	}()

	// ── 2. Load checkpoint ────────────────────────────────────────────────────
	state, err := loadState(opts.Repo, opts.PR)
	if err != nil {
		return Result{}, fmt.Errorf("runner: load state: %w", err)
	}
	resumedFrom := state.Iteration

	if resumedFrom > 0 {
		log.Info("runner: resuming from checkpoint",
			"repo", opts.Repo,
			"pr", opts.PR,
			"iteration", resumedFrom,
			"last_review_id", state.LastReviewID,
		)
	}

	// ── 3. Main cycle loop ────────────────────────────────────────────────────
	waitPolls := 0
	for state.Iteration < opts.MaxIterations {
		log.Info("runner: starting iteration",
			"repo", opts.Repo,
			"pr", opts.PR,
			"iteration", state.Iteration+1,
			"max", opts.MaxIterations,
		)

		agentResult, err := opts.Agent.Run(engine.RunOpts{
			PR:                opts.PR,
			Repo:              opts.Repo,
			CWD:               opts.CWD,
			Model:             opts.Model,
			LastKnownReviewID: state.LastReviewID,
		})
		if err != nil {
			// Hard agent failure — persist state so we can resume, then surface.
			state.LastAgentAction = "error"
			_ = saveState(state)
			persistSession(stats.OutcomeError)
			return Result{
				Iterations:           state.Iteration,
				ResumedFromIteration: resumedFrom,
				Session:              session,
			}, fmt.Errorf("runner: agent %s iteration %d: %w", opts.Agent.Name(), state.Iteration+1, err)
		}

		if agentResult.Waiting {
			// Not actionable yet — do not count against MaxIterations.
			waitPolls++
			if waitPolls > opts.MaxWaitPolls {
				log.Warn("runner: max wait polls reached without actionable review",
					"repo", opts.Repo,
					"pr", opts.PR,
					"wait_polls", waitPolls,
				)
				persistSession(stats.OutcomeAborted)
				return Result{
					Iterations:           state.Iteration,
					ResumedFromIteration: resumedFrom,
					Session:              session,
				}, ErrMaxWaitPolls
			}
			log.Info("runner: waiting for actionable review",
				"repo", opts.Repo,
				"pr", opts.PR,
				"wait_poll", waitPolls,
				"max_wait_polls", opts.MaxWaitPolls,
			)
			time.Sleep(opts.PollInterval)
			continue
		}
		waitPolls = 0 // reset on actionable result

		state.Iteration++
		// Track per-iteration comment counts for the session.
		session.CopilotCommentsByIteration = append(session.CopilotCommentsByIteration, agentResult.Comments)
		session.Iterations = state.Iteration

		// Persist the review ID so the next iteration can detect no_change.
		if agentResult.ReviewID != "" {
			state.LastReviewID = agentResult.ReviewID
		}

		if agentResult.Approved {
			state.LastAgentAction = "approved"
			log.Info("runner: Copilot approved PR",
				"repo", opts.Repo,
				"pr", opts.PR,
				"iterations", state.Iteration,
			)
			// Terminal success — clear the checkpoint.
			_ = clearState(opts.Repo, opts.PR)
			persistSession(stats.OutcomeApproved)
			return Result{
				Approved:             true,
				Iterations:           state.Iteration,
				ResumedFromIteration: resumedFrom,
				Session:              session,
			}, nil
		}

		action := "fixed"
		if agentResult.Comments == 0 {
			action = "no_comments"
		}
		state.LastAgentAction = action

		// Checkpoint after each successful iteration.
		if err := saveState(state); err != nil {
			// Non-fatal: log and continue. Losing the checkpoint means we can't
			// resume if we crash, but it's better to keep running than to abort.
			log.Error("runner: save state", "error", err)
		}

		log.Info("runner: iteration complete",
			"repo", opts.Repo,
			"pr", opts.PR,
			"iteration", state.Iteration,
			"comments_addressed", agentResult.Comments,
		)

		// Wait before next poll.
		time.Sleep(opts.PollInterval)
	}

	// ── 4. Max iterations reached ─────────────────────────────────────────────
	log.Warn("runner: max iterations reached without approval",
		"repo", opts.Repo,
		"pr", opts.PR,
		"iterations", state.Iteration,
	)
	// Keep state on disk so a human or future run can inspect it.
	persistSession(stats.OutcomeMaxIter)
	return Result{
		Approved:             false,
		Iterations:           state.Iteration,
		ResumedFromIteration: resumedFrom,
		Session:              session,
	}, ErrMaxIterations
}

// validateOpts checks required fields and applies defaults.
func validateOpts(o *Opts) error {
	if o.Repo == "" {
		return errors.New("runner: Opts.Repo is required")
	}
	if o.PR == "" {
		return errors.New("runner: Opts.PR is required")
	}
	if o.CWD == "" {
		return errors.New("runner: Opts.CWD is required")
	}
	if o.Agent == nil {
		return errors.New("runner: Opts.Agent is required")
	}
	if o.MaxIterations <= 0 {
		o.MaxIterations = DefaultMaxIterations
	}
	if o.MaxWaitPolls <= 0 {
		o.MaxWaitPolls = 60
	}
	if o.PollInterval <= 0 {
		o.PollInterval = 30 * time.Second
	}
	return nil
}
