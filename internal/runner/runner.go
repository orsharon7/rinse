// Package runner implements the core PR review cycle loop that drives agents
// until Copilot approves or max iterations are reached.
// This package replaces the shell scripts in scripts/ over time.
package runner

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/orsharon7/rinse/internal/db"
	"github.com/orsharon7/rinse/internal/engine"
	"github.com/orsharon7/rinse/internal/engine/lock"
	"github.com/orsharon7/rinse/internal/summary"
)

// DefaultMaxIterations is used when Opts.MaxIterations is 0.
const DefaultMaxIterations = 10

// DefaultPollInterval is used when Opts.PollInterval is 0.
const DefaultPollInterval = 30 * time.Second

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

	// PRTitle is the human-readable PR title, stored in telemetry.
	PRTitle string

	// Branch is the PR head branch name, stored in telemetry.
	Branch string

	// RunnerName is the agent CLI name ("opencode" or "claude"), stored in
	// telemetry. Defaults to "opencode" when empty.
	RunnerName string

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
	// Defaults to DefaultPollInterval when zero.
	PollInterval time.Duration

	// Agent is the engine.Agent implementation to drive.
	Agent engine.Agent

	// DB is an optional telemetry database handle. When non-nil, each run is
	// recorded to the sessions / comment_events tables. DB errors are logged
	// but never abort the review cycle (fire-and-forget semantics).
	DB *db.DB

	// Logger is an optional structured logger. Falls back to slog.Default().
	Logger *slog.Logger
}

// Result summarises the outcome of the complete run loop.
type Result struct {
	// Approved reports whether Copilot approved the PR.
	Approved bool

	// Iterations is the number of fix cycles that were executed.
	Iterations int

	// TotalComments is the sum of comments addressed across all iterations.
	TotalComments int

	// ResumedFromIteration is non-zero when the run resumed from a checkpoint.
	ResumedFromIteration int
}

// Run drives the PR review lifecycle:
//
//  1. Acquire a per-PR on-disk lock (atomic, stale-lock aware).
//  2. Load any existing checkpoint (crash recovery / partial resume).
//  3. Insert a telemetry session row (outcome="open").
//  4. Loop: invoke Agent, checkpoint state, push & re-request review.
//  5. Exit when Copilot approves, PR is merged/closed, or max iterations reached.
//  6. Clear the checkpoint on terminal outcomes.
//  7. Finalize the telemetry session row with outcome and duration.
//
// Run honours the "never swallow errors" engineering standard: subprocess
// errors are propagated with context rather than swallowed. DB errors are
// fire-and-forget and never abort the cycle.
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

	// ── 3. Telemetry session start ────────────────────────────────────────────
	startedAt := time.Now()
	sessionID := sessionID(opts.Repo, opts.PR, startedAt)
	prNum, _ := strconv.Atoi(opts.PR)

	if opts.DB != nil && resumedFrom == 0 {
		// Only insert a new row on fresh runs; resumed runs already have a row.
		if dbErr := opts.DB.InsertSession(db.SessionRow{
			ID:       sessionID,
			Repo:     opts.Repo,
			PRNumber: prNum,
			PRTitle:  opts.PRTitle,
			Branch:   opts.Branch,
			Runner:   opts.RunnerName,
			Model:    opts.Model,

			StartedAt:          startedAt,
			Iterations:         0,
			TotalCommentsFixed: 0,
			Outcome:            "open",
		}); dbErr != nil {
			log.Warn("runner: telemetry: insert session", "error", dbErr)
		}
	}

	// finalizeSession is deferred so every exit path records a terminal outcome.
	totalComments := 0
	finalizeSession := func(outcome string, iterations int) {
		if opts.DB == nil {
			return
		}
		now := time.Now()
		dur := int(now.Sub(startedAt).Seconds())
		if dbErr := opts.DB.FinalizeSession(sessionID, now, dur, totalComments, iterations, outcome); dbErr != nil {
			log.Warn("runner: telemetry: finalize session", "error", dbErr)
		}
	}

	// ── 4. Main cycle loop ────────────────────────────────────────────────────
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
			finalizeSession("failed", state.Iteration)
			return Result{
				Iterations:           state.Iteration,
				TotalComments:        totalComments,
				ResumedFromIteration: resumedFrom,
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
				finalizeSession("failed", state.Iteration)
				return Result{
					Iterations:           state.Iteration,
					TotalComments:        totalComments,
					ResumedFromIteration: resumedFrom,
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
		totalComments += agentResult.Comments

		// Persist the review ID so the next iteration can detect no_change.
		if agentResult.ReviewID != "" {
			state.LastReviewID = agentResult.ReviewID
		}

		// Record comment event for this iteration.
		if opts.DB != nil {
			evID := fmt.Sprintf("%s-iter%d", sessionID, state.Iteration)
			if dbErr := opts.DB.InsertCommentEvent(evID, sessionID, state.Iteration, agentResult.Comments); dbErr != nil {
				log.Warn("runner: telemetry: insert comment event", "error", dbErr)
			}
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
			finalizeSession("merged", state.Iteration)
			// Post cycle summary (non-fatal).
			if err := summary.Post(opts.Repo, opts.PR, summary.OutcomeApproved, state.Iteration, totalComments, time.Since(startedAt)); err != nil {
				log.Warn("runner: post cycle summary", "error", err)
			}
			return Result{
				Approved:             true,
				Iterations:           state.Iteration,
				TotalComments:        totalComments,
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

		// Wait before next poll only if another iteration will run.
		if state.Iteration < opts.MaxIterations {
			time.Sleep(opts.PollInterval)
		}
	}

	// ── 5. Max iterations reached ─────────────────────────────────────────────
	log.Warn("runner: max iterations reached without approval",
		"repo", opts.Repo,
		"pr", opts.PR,
		"iterations", state.Iteration,
	)
	finalizeSession("failed", state.Iteration)
	// Post cycle summary (non-fatal).
	if err := summary.Post(opts.Repo, opts.PR, summary.OutcomeMaxIter, state.Iteration, totalComments, time.Since(startedAt)); err != nil {
		log.Warn("runner: post cycle summary", "error", err)
	}
	// Keep state on disk so a human or future run can inspect it.
	return Result{
		Approved:             false,
		Iterations:           state.Iteration,
		TotalComments:        totalComments,
		ResumedFromIteration: resumedFrom,
	}, ErrMaxIterations
}

// sessionID returns a stable, unique session identifier for a run.
// Format: "{repo-slug}-pr{pr}-{unix-nano}" — ensures uniqueness even for
// concurrent runs on different machines.
func sessionID(repo, pr string, t time.Time) string {
	slug := strings.NewReplacer("/", "-", ".", "-").Replace(repo)
	return fmt.Sprintf("%s-pr%s-%d", slug, pr, t.UnixNano())
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
		o.PollInterval = DefaultPollInterval
	}
	if o.RunnerName == "" {
		o.RunnerName = "opencode"
	}
	return nil
}
