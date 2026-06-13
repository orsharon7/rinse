// Package wait blocks until a Copilot review is submitted on a pull request,
// or a timeout elapses. Two strategies are tried in order:
//
//  1. Push: spawn `gh webhook forward` (cli/gh-webhook extension) and listen
//     on a local HTTP port for `pull_request_review` events. Sub-second
//     detection latency.
//  2. Poll: ETag-conditional GET against /repos/{owner}/{repo}/pulls/{n}/reviews
//     every PollInterval. 304 Not Modified responses are free of rate-limit
//     cost so polling can be aggressive without burning the API budget.
//
// The poll strategy is always available; the webhook strategy requires the
// gh-webhook extension and repo `admin:repo_hook` scope. When the webhook
// strategy fails to initialise (extension missing, permission denied), wait
// transparently falls back to polling.
package wait

import (
	"context"
	"fmt"
	"io"
	"time"
)

// Opts configures a single wait-for-review operation.
type Opts struct {
	Repo         string
	PR           int
	Timeout      time.Duration
	PollInterval time.Duration
	// BotLogin matches the review author's login (case-insensitive substring).
	// Defaults to "copilot" so both "Copilot" and "copilot-pull-request-reviewer[bot]" match.
	BotLogin string
	// ProgressOut receives human-readable tick lines compatible with the existing
	// TUI parser (`Copilot reviewing  ━━━━ 60s / 300s (20%)`). Nil silences ticks.
	ProgressOut io.Writer
	// Strategy forces a specific strategy. "" = auto (webhook with poll fallback).
	Strategy string
}

// Outcome describes how the wait ended.
type Outcome string

const (
	OutcomeArrived  Outcome = "arrived"
	OutcomeTimeout  Outcome = "timeout"
	OutcomeError    Outcome = "error"
	OutcomeCanceled Outcome = "canceled"
)

// Result is what ForCopilotReview returns.
type Result struct {
	Outcome Outcome
	Method  string
	Elapsed time.Duration
}

// ForCopilotReview blocks until Copilot submits a review on the configured
// pull request, the timeout elapses, or ctx is cancelled.
//
// An upfront REST check returns OutcomeArrived immediately when Copilot is
// neither in `requested_reviewers` nor has a PENDING review entry — i.e. a
// previous review is sitting unread. This makes the subcommand safe to call
// after a previous fix iteration without waiting for an event that already
// fired.
func ForCopilotReview(ctx context.Context, opts Opts) (Result, error) {
	if err := validate(&opts); err != nil {
		return Result{Outcome: OutcomeError}, err
	}

	deadline := time.Now().Add(opts.Timeout)
	waitCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	start := time.Now()

	if alreadyDone, err := copilotAlreadyDone(waitCtx, opts); err == nil && alreadyDone {
		return Result{Outcome: OutcomeArrived, Method: "already-done", Elapsed: time.Since(start)}, nil
	}

	switch opts.Strategy {
	case "poll":
		return runPoll(waitCtx, opts, start)
	case "webhook":
		return runWebhook(waitCtx, opts, start)
	case "", "auto":
		if hasWebhookExtension() {
			return runRace(waitCtx, opts, start)
		}
		return runPoll(waitCtx, opts, start)
	default:
		return Result{Outcome: OutcomeError}, fmt.Errorf("wait: unknown strategy %q", opts.Strategy)
	}
}

// runRace runs webhook and poll in parallel; whichever detects the review first
// wins. This closes the pre-check → subscription race: if Copilot submits the
// review in the few hundred ms between copilotAlreadyDone returning false and
// `gh webhook forward` registering, the webhook misses the event entirely, but
// the parallel poll catches it within PollInterval. Both methods share the
// same deadline ctx, so they finish together on timeout.
//
// Progress ticks come from webhook only — poll runs silent to avoid duplicate
// lines in the TUI. The chosen method name is reported on the winning result.
func runRace(ctx context.Context, opts Opts, start time.Time) (Result, error) {
	type rResult struct {
		r   Result
		err error
	}
	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan rResult, 2)

	go func() {
		r, err := runWebhook(raceCtx, opts, start)
		results <- rResult{r, err}
	}()

	pollOpts := opts
	pollOpts.ProgressOut = nil
	go func() {
		r, err := runPoll(raceCtx, pollOpts, start)
		results <- rResult{r, err}
	}()

	var bestResult Result
	var bestErr error
	hasBest := false
	for i := 0; i < 2; i++ {
		sr := <-results
		if sr.err == nil && sr.r.Outcome == OutcomeArrived {
			cancel()
			return sr.r, nil
		}
		// Prefer a clean (no-error) outcome over an errored one. If both
		// strategies error, the second-seen wins arbitrarily, which is fine
		// because the user gets enough signal to retry.
		if !hasBest || (bestErr != nil && sr.err == nil) {
			bestResult = sr.r
			bestErr = sr.err
			hasBest = true
		}
	}
	if bestResult.Outcome == "" {
		bestResult = Result{Outcome: OutcomeTimeout, Method: "auto", Elapsed: time.Since(start)}
	}
	return bestResult, bestErr
}

func validate(o *Opts) error {
	if o.Repo == "" {
		return fmt.Errorf("wait: Repo is required")
	}
	if o.PR <= 0 {
		return fmt.Errorf("wait: PR must be positive, got %d", o.PR)
	}
	if o.Timeout <= 0 {
		o.Timeout = 5 * time.Minute
	}
	if o.PollInterval <= 0 {
		o.PollInterval = 5 * time.Second
	}
	if o.BotLogin == "" {
		o.BotLogin = "copilot"
	}
	return nil
}
