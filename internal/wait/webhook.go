package wait

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// hasWebhookExtension reports whether `gh webhook` is callable. The gh-webhook
// extension provides this subcommand; without it the push strategy is unusable.
func hasWebhookExtension() bool {
	cmd := exec.Command("gh", "webhook", "--help")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run() == nil
}

// cleanupStaleForwarderHook deletes any pre-existing "cli" webhook on the repo
// whose config URL points at github.com's webhook-forwarder. gh-webhook creates
// exactly one such hook per repo and will fail with HTTP 422 if a stale one
// remains from a previous SIGKILLed run. We only delete hooks matching both
// name=="cli" AND the forwarder URL, so we never touch user-managed webhooks.
func cleanupStaleForwarderHook(ctx context.Context, repo string) {
	out, err := exec.CommandContext(ctx, "gh", "api",
		fmt.Sprintf("/repos/%s/hooks", repo),
		"--jq", `.[] | select(.name=="cli" and (.config.url // "" | contains("webhook-forwarder"))) | .id`,
	).Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		id := strings.TrimSpace(line)
		if id == "" {
			continue
		}
		_ = exec.CommandContext(ctx, "gh", "api",
			fmt.Sprintf("/repos/%s/hooks/%s", repo, id),
			"-X", "DELETE",
		).Run()
	}
}

// runWebhook spawns `gh webhook forward` and listens on a local HTTP port for
// `pull_request_review` events. Returns OutcomeArrived when a matching event
// (action=submitted on the target PR by the configured bot) arrives.
func runWebhook(ctx context.Context, opts Opts, start time.Time) (Result, error) {
	cleanupStaleForwarderHook(ctx, opts.Repo)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Result{Outcome: OutcomeError, Method: "webhook"}, fmt.Errorf("wait: bind localhost: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	arrived := make(chan struct{}, 1)
	var (
		matchMu   sync.Mutex
		matchErr  error
		matchOnce sync.Once
	)
	readMatchErr := func() error {
		matchMu.Lock()
		defer matchMu.Unlock()
		return matchErr
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/copilot", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		matched, mErr := MatchReviewEvent(body, opts.PR, opts.BotLogin)
		if mErr != nil {
			matchOnce.Do(func() {
				matchMu.Lock()
				matchErr = mErr
				matchMu.Unlock()
			})
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		if matched {
			select {
			case arrived <- struct{}{}:
			default:
			}
		}
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	serverDone := make(chan struct{})
	go func() {
		_ = srv.Serve(listener)
		close(serverDone)
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = srv.Shutdown(shutdownCtx)
		cancel()
		<-serverDone
	}()

	url := fmt.Sprintf("http://127.0.0.1:%d/copilot", port)
	ghCtx, ghCancel := context.WithCancel(ctx)
	defer ghCancel()
	gh := exec.CommandContext(ghCtx, "gh", "webhook", "forward",
		"--repo", opts.Repo,
		"--events", "pull_request_review",
		"--url", url,
	)
	gh.Stdout = io.Discard
	var ghStderr strings.Builder
	gh.Stderr = &ghStderr
	if err := gh.Start(); err != nil {
		return Result{Outcome: OutcomeError, Method: "webhook"}, fmt.Errorf("wait: start gh webhook forward: %w", err)
	}
	ghExit := make(chan error, 1)
	go func() { ghExit <- gh.Wait() }()

	tickInterval := opts.PollInterval
	if tickInterval <= 0 {
		tickInterval = 5 * time.Second
	}
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	timeout := opts.Timeout

	emitTick(opts.ProgressOut, "webhook", time.Since(start), timeout)

	for {
		select {
		case <-arrived:
			return Result{Outcome: OutcomeArrived, Method: "webhook", Elapsed: time.Since(start)}, nil
		case <-ctx.Done():
			_ = gh.Process.Kill()
			if ctx.Err() == context.DeadlineExceeded {
				return Result{Outcome: OutcomeTimeout, Method: "webhook", Elapsed: time.Since(start)}, nil
			}
			return Result{Outcome: OutcomeCanceled, Method: "webhook", Elapsed: time.Since(start)}, ctx.Err()
		case err := <-ghExit:
			if err != nil {
				stderr := strings.TrimSpace(ghStderr.String())
				return Result{Outcome: OutcomeError, Method: "webhook"},
					fmt.Errorf("wait: gh webhook forward exited: %w: %s", err, stderr)
			}
			return Result{Outcome: OutcomeError, Method: "webhook"},
				fmt.Errorf("wait: gh webhook forward exited unexpectedly")
		case <-ticker.C:
			emitTick(opts.ProgressOut, "webhook", time.Since(start), timeout)
			if mErr := readMatchErr(); mErr != nil {
				return Result{Outcome: OutcomeError, Method: "webhook"}, mErr
			}
		}
	}
}

// MatchReviewEvent inspects a `pull_request_review` webhook payload and reports
// whether it matches the target PR + bot. Exported for unit testing.
func MatchReviewEvent(body []byte, pr int, botLogin string) (bool, error) {
	var ev struct {
		Action string `json:"action"`
		Review struct {
			State string `json:"state"`
			User  struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			} `json:"user"`
		} `json:"review"`
		PullRequest struct {
			Number int `json:"number"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		return false, fmt.Errorf("wait: parse webhook payload: %w", err)
	}
	if ev.Action != "submitted" {
		return false, nil
	}
	if ev.PullRequest.Number != pr {
		return false, nil
	}
	if t := ev.Review.User.Type; t != "" && !strings.EqualFold(t, "Bot") {
		return false, nil
	}
	if !strings.Contains(strings.ToLower(ev.Review.User.Login), strings.ToLower(botLogin)) {
		return false, nil
	}
	return true, nil
}
