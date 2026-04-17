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
)

// DefaultMaxIterations is used when Opts.MaxIterations is 0.
const DefaultMaxIterations = 10

// ErrMaxIterations is returned when the runner exits because it reached the
// configured iteration limit without Copilot approval.
var ErrMaxIterations = errors.New("runner: max iterations reached without approval")

// ErrAlreadyRunning is returned when another process is actively running the
// review cycle for the same PR.
var ErrAlreadyRunning = errors.New("runner: another process is already running for this PR")

// Opts carries all configuration for a single PR's review lifecycle.
type Opts struct {
	// Repo is the "owner/repo" string (e.g. "orsharon7/rinse").
	Repo string

	// PR is the pull request number as a string.
	PR string

	// CWD is the local working directory for the repository checkout.
	CWD string

	// Model is an optional model override forwarded to the Agent.
	Model string

	// MaxIterations caps the number of fix-and-review cycles.
	// Defaults to DefaultMaxIterations when 0.
	MaxIterations int

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
func Run(opts Opts) (Result, error) {
	if err := validateOpts(&opts); err != nil {
		return Result{}, err
	}

	log := opts.Logger
	if log == nil {
		log = slog.Default()
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
	for state.Iteration < opts.MaxIterations {
		log.Info("runner: starting iteration",
			"repo", opts.Repo,
			"pr", opts.PR,
			"iteration", state.Iteration+1,
			"max", opts.MaxIterations,
		)

		agentResult, err := opts.Agent.Run(engine.RunOpts{
			PR:    opts.PR,
			Repo:  opts.Repo,
			CWD:   opts.CWD,
			Model: opts.Model,
		})
		if err != nil {
			// Hard agent failure — persist state so we can resume, then surface.
			state.LastAgentAction = "error"
			_ = saveState(state)
			return Result{
				Iterations:           state.Iteration,
				ResumedFromIteration: resumedFrom,
			}, fmt.Errorf("runner: agent %s iteration %d: %w", opts.Agent.Name(), state.Iteration+1, err)
		}

		state.Iteration++

		if agentResult.Approved {
			state.LastAgentAction = "approved"
			log.Info("runner: Copilot approved PR",
				"repo", opts.Repo,
				"pr", opts.PR,
				"iterations", state.Iteration,
			)
			// Terminal success — clear the checkpoint.
			_ = clearState(opts.Repo, opts.PR)
			return Result{
				Approved:             true,
				Iterations:           state.Iteration,
				ResumedFromIteration: resumedFrom,
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
	return Result{
		Approved:             false,
		Iterations:           state.Iteration,
		ResumedFromIteration: resumedFrom,
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
	if o.PollInterval <= 0 {
		o.PollInterval = 30 * time.Second
	}
	return nil
}
