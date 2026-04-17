// Package summary posts a RINSE cycle summary comment on a GitHub PR when the
// review cycle terminates.
package summary

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// Outcome labels for each terminal cycle result.
const (
	OutcomeMerged       = "✅ Merged"
	OutcomeApproved     = "✅ Approved — ready to merge"
	OutcomeAlreadyClean = "✅ PR was already clean — 0 Copilot comments"
	OutcomeMaxIter      = "⚠️ Max iterations reached — manual review needed"
)

// Post posts a single RINSE cycle summary comment on the PR.
// It uses the gh CLI under the hood so no explicit token management is needed.
// Errors are returned but are non-fatal — callers should log and continue.
//
// Parameters:
//
//	repo           — "owner/repo"
//	pr             — PR number as a string
//	outcomeLabel   — human-readable outcome (use the Outcome* constants)
//	iterations     — number of fix iterations executed
//	totalComments  — total comments addressed across all iterations
//	duration       — total cycle duration
func Post(repo, pr, outcomeLabel string, iterations, totalComments int, duration time.Duration) error {
	durationMin := int(duration.Minutes())
	estSaved := totalComments * 4

	body := buildBody(outcomeLabel, iterations, totalComments, durationMin, estSaved)

	// Use gh CLI to post the issue comment (avoids token management).
	// A 30-second timeout prevents a hung gh process from blocking cycle exit.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{
		"api",
		fmt.Sprintf("repos/%s/issues/%s/comments", repo, pr),
		"-X", "POST",
		"-f", "body=" + body,
	}
	cmd := exec.CommandContext(ctx, "gh", args...) //nolint:gosec // args are controlled
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("summary: gh api: %w — %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// PostWithToken posts using a GitHub API token directly (for environments
// where the gh CLI is not available).
func PostWithToken(token, repo, pr, outcomeLabel string, iterations, totalComments int, duration time.Duration) error {
	durationMin := int(duration.Minutes())
	estSaved := totalComments * 4

	body := buildBody(outcomeLabel, iterations, totalComments, durationMin, estSaved)

	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return fmt.Errorf("summary: marshal payload: %w", err)
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/issues/%s/comments", repo, pr)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("summary: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("summary: http post: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("summary: GitHub API returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func buildBody(outcome string, iterations, totalComments, durationMin, estSaved int) string {
	return fmt.Sprintf(`🔁 **RINSE cycle complete**

| | |
|---|---|
| Outcome | %s |
| Iterations | %d |
| Comments fixed | %d |
| Duration | %d min |
| Est. time saved | ~%d min |

*Reviewed by [RINSE](https://github.com/orsharon7/rinse)*`,
		outcome, iterations, totalComments, durationMin, estSaved)
}
