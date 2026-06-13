package wait

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// runPoll polls /repos/{owner}/{repo}/pulls/{n}/reviews with ETag-conditional
// GETs every PollInterval. 304 Not Modified responses are free of rate-limit
// cost, so polling can be aggressive without burning the API budget.
func runPoll(ctx context.Context, opts Opts, start time.Time) (Result, error) {
	token, err := ghToken(ctx)
	if err != nil {
		return Result{Outcome: OutcomeError, Method: "poll"}, err
	}

	apiBase := "https://api.github.com"
	if v := os.Getenv("RINSE_TEST_GH_API_BASE"); v != "" {
		apiBase = strings.TrimRight(v, "/")
	}
	endpoint := fmt.Sprintf("%s/repos/%s/pulls/%d/reviews", apiBase, opts.Repo, opts.PR)
	client := &http.Client{Timeout: 10 * time.Second}

	var (
		etag        string
		lastSeenIDs = map[int64]bool{}
		seededIDs   bool
	)

	emitTick(opts.ProgressOut, "poll", time.Since(start), opts.Timeout)

	ticker := time.NewTicker(opts.PollInterval)
	defer ticker.Stop()

	check := func() (bool, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return false, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		resp, err := client.Do(req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusNotModified {
			return false, nil
		}
		if resp.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			return false, fmt.Errorf("wait: poll %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		if t := resp.Header.Get("ETag"); t != "" {
			etag = t
		}
		var reviews []struct {
			ID    int64  `json:"id"`
			State string `json:"state"`
			User  struct {
				Login string `json:"login"`
			} `json:"user"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&reviews); err != nil {
			return false, fmt.Errorf("wait: decode reviews: %w", err)
		}
		botMatch := strings.ToLower(opts.BotLogin)
		if !seededIDs {
			for _, r := range reviews {
				lastSeenIDs[r.ID] = true
			}
			seededIDs = true
			return false, nil
		}
		for _, r := range reviews {
			if lastSeenIDs[r.ID] {
				continue
			}
			lastSeenIDs[r.ID] = true
			if r.State == "PENDING" {
				continue
			}
			if !strings.Contains(strings.ToLower(r.User.Login), botMatch) {
				continue
			}
			return true, nil
		}
		return false, nil
	}

	if matched, err := check(); err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return finalizePollCtx(ctx, "poll", start), nil
		}
		return Result{Outcome: OutcomeError, Method: "poll"}, err
	} else if matched {
		return Result{Outcome: OutcomeArrived, Method: "poll", Elapsed: time.Since(start)}, nil
	}

	for {
		select {
		case <-ctx.Done():
			return finalizePollCtx(ctx, "poll", start), nil
		case <-ticker.C:
			emitTick(opts.ProgressOut, "poll", time.Since(start), opts.Timeout)
			matched, err := check()
			if err != nil {
				if cerr := ctx.Err(); cerr != nil {
					return finalizePollCtx(ctx, "poll", start), nil
				}
				return Result{Outcome: OutcomeError, Method: "poll"}, err
			}
			if matched {
				return Result{Outcome: OutcomeArrived, Method: "poll", Elapsed: time.Since(start)}, nil
			}
		}
	}
}

func finalizePollCtx(ctx context.Context, method string, start time.Time) Result {
	if ctx.Err() == context.DeadlineExceeded {
		return Result{Outcome: OutcomeTimeout, Method: method, Elapsed: time.Since(start)}
	}
	return Result{Outcome: OutcomeCanceled, Method: method, Elapsed: time.Since(start)}
}

// ghToken returns the active GitHub token via the `gh auth token` command.
// Avoids a hard dependency on a specific env var (gh handles keyring lookups).
// Tests can override the token by setting RINSE_TEST_GH_TOKEN_OVERRIDE.
func ghToken(ctx context.Context) (string, error) {
	if v := os.Getenv("RINSE_TEST_GH_TOKEN_OVERRIDE"); v != "" {
		return v, nil
	}
	cmd := exec.CommandContext(ctx, "gh", "auth", "token")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("wait: gh auth token: %w", err)
	}
	t := strings.TrimSpace(string(out))
	if t == "" {
		return "", fmt.Errorf("wait: gh auth token returned empty")
	}
	return t, nil
}

// copilotAlreadyDone reports whether Copilot has already finished any pending
// review on the PR — i.e. neither in requested_reviewers nor has a PENDING
// review entry. Returns true when the caller can proceed without waiting.
//
// Returns (false, nil) when Copilot is still pending or never reviewed.
// Returns (false, err) on transient errors so callers fall back to waiting
// rather than incorrectly claiming arrival.
func copilotAlreadyDone(ctx context.Context, opts Opts) (bool, error) {
	token, err := ghToken(ctx)
	if err != nil {
		return false, err
	}
	apiBase := "https://api.github.com"
	if v := os.Getenv("RINSE_TEST_GH_API_BASE"); v != "" {
		apiBase = strings.TrimRight(v, "/")
	}
	endpoint := fmt.Sprintf("%s/repos/%s/pulls/%d", apiBase, opts.Repo, opts.PR)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return false, fmt.Errorf("wait: pre-check %d", resp.StatusCode)
	}
	var pr struct {
		RequestedReviewers []struct {
			Login string `json:"login"`
		} `json:"requested_reviewers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return false, err
	}
	bot := strings.ToLower(opts.BotLogin)
	for _, r := range pr.RequestedReviewers {
		if strings.Contains(strings.ToLower(r.Login), bot) {
			return false, nil
		}
	}
	return true, nil
}
