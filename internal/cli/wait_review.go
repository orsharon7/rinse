package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/orsharon7/rinse/internal/wait"
)

// WaitReviewResult is the JSON envelope for `rinse wait-review --json`.
type WaitReviewResult struct {
	OK      bool         `json:"ok"`
	PR      string       `json:"pr"`
	Repo    string       `json:"repo"`
	Outcome wait.Outcome `json:"outcome"`
	Method  string       `json:"method"`
	// int64 ms, not time.Duration: a Duration marshals as nanoseconds.
	Elapsed int64  `json:"elapsed_ms"`
	Error   string `json:"error,omitempty"`
}

func runWaitReviewCmd(args []string) {
	var (
		prNum    string
		repo     string
		strategy string
		botLogin string
		timeout  = 5 * time.Minute
		interval = 5 * time.Second
		asJSON   bool
		quiet    bool
	)

	for _, a := range args {
		if a == "--json" {
			asJSON = true
			break
		}
	}

	prNum, rest := extractPositionalPR(args)
	if prNum == "" {
		fatalf(asJSON, "usage: rinse wait-review <pr> [--repo X] [--timeout 5m] [--interval 5s] [--strategy auto|webhook|poll] [--quiet] [--json]")
	}
	prInt, err := strconv.Atoi(prNum)
	if err != nil || prInt <= 0 {
		fatalf(asJSON, "PR number must be a positive integer, got: %s", prNum)
	}

	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--repo":
			i++
			if i >= len(rest) || strings.HasPrefix(rest[i], "-") {
				fatalf(asJSON, "--repo requires a value")
			}
			repo = rest[i]
		case "--pr":
			i++
			if i >= len(rest) || strings.HasPrefix(rest[i], "-") {
				fatalf(asJSON, "--pr requires a value")
			}
			prNum = rest[i]
			n, perr := strconv.Atoi(prNum)
			if perr != nil || n <= 0 {
				fatalf(asJSON, "PR number must be a positive integer, got: %s", prNum)
			}
			prInt = n
		case "--timeout":
			i++
			if i >= len(rest) || strings.HasPrefix(rest[i], "-") {
				fatalf(asJSON, "--timeout requires a value (e.g. 5m, 300s)")
			}
			d, terr := time.ParseDuration(rest[i])
			if terr != nil || d <= 0 {
				fatalf(asJSON, "--timeout invalid: %s", rest[i])
			}
			timeout = d
		case "--interval":
			i++
			if i >= len(rest) || strings.HasPrefix(rest[i], "-") {
				fatalf(asJSON, "--interval requires a value (e.g. 5s)")
			}
			d, terr := time.ParseDuration(rest[i])
			if terr != nil || d <= 0 {
				fatalf(asJSON, "--interval invalid: %s", rest[i])
			}
			interval = d
		case "--strategy":
			i++
			if i >= len(rest) || strings.HasPrefix(rest[i], "-") {
				fatalf(asJSON, "--strategy requires a value (auto|webhook|poll)")
			}
			strategy = rest[i]
		case "--bot":
			i++
			if i >= len(rest) || strings.HasPrefix(rest[i], "-") {
				fatalf(asJSON, "--bot requires a value")
			}
			botLogin = rest[i]
		case "--quiet":
			quiet = true
		case "--json":
			asJSON = true
		default:
			fatalf(asJSON, "unknown flag: %s", rest[i])
		}
	}

	if repo == "" {
		repo = detectRepo()
		if repo == "" {
			fatalf(asJSON, "no repository detected — run from inside a git checkout or pass --repo")
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var progressOut = os.Stderr
	if quiet {
		progressOut = nil
	}

	res, runErr := wait.ForCopilotReview(ctx, wait.Opts{
		Repo:         repo,
		PR:           prInt,
		Timeout:      timeout,
		PollInterval: interval,
		BotLogin:     botLogin,
		ProgressOut:  progressOut,
		Strategy:     strategy,
	})

	if asJSON {
		out := WaitReviewResult{
			OK:      runErr == nil && res.Outcome == wait.OutcomeArrived,
			PR:      prNum,
			Repo:    repo,
			Outcome: res.Outcome,
			Method:  res.Method,
			Elapsed: res.Elapsed.Milliseconds(),
		}
		if runErr != nil {
			out.Error = runErr.Error()
		}
		emitJSON(out)
	} else if !quiet {
		switch res.Outcome {
		case wait.OutcomeArrived:
			fmt.Fprintf(os.Stderr, "✓ Copilot review arrived via %s (%s)\n", res.Method, res.Elapsed.Round(time.Second))
		case wait.OutcomeTimeout:
			fmt.Fprintf(os.Stderr, "⏰ Timed out after %s waiting for Copilot review\n", res.Elapsed.Round(time.Second))
		case wait.OutcomeCanceled:
			fmt.Fprintf(os.Stderr, "○ Canceled waiting for Copilot review (%s elapsed)\n", res.Elapsed.Round(time.Second))
		default:
			if runErr != nil {
				fmt.Fprintf(os.Stderr, "✗ %v\n", runErr)
			}
		}
	}

	switch res.Outcome {
	case wait.OutcomeArrived:
		os.Exit(0)
	case wait.OutcomeTimeout:
		os.Exit(1)
	case wait.OutcomeCanceled:
		os.Exit(130)
	default:
		os.Exit(2)
	}
}
