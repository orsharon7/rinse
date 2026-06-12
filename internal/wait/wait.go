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
func ForCopilotReview(ctx context.Context, opts Opts) (Result, error) {
	if err := validate(&opts); err != nil {
		return Result{Outcome: OutcomeError}, err
	}

	deadline := time.Now().Add(opts.Timeout)
	waitCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	start := time.Now()

	switch opts.Strategy {
	case "poll":
		return runPoll(waitCtx, opts, start)
	case "webhook":
		return runWebhook(waitCtx, opts, start)
	case "", "auto":
		if hasWebhookExtension() {
			r, err := runWebhook(waitCtx, opts, start)
			if err == nil {
				return r, nil
			}
			if waitCtx.Err() != nil {
				return r, err
			}
		}
		return runPoll(waitCtx, opts, start)
	default:
		return Result{Outcome: OutcomeError}, fmt.Errorf("wait: unknown strategy %q", opts.Strategy)
	}
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
