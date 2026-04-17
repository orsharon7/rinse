// Package agent provides shared utilities for engine.Agent implementations:
// fetching Copilot review state via the gh CLI, building the fix prompt,
// and pushing commits + re-requesting review.
//
// Individual agents (opencode, claude) use the shared helper functions in
// this package and supply their own CLI invocation logic.
package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ReviewState is the parsed outcome of `gh pr review` for a Copilot review.
type ReviewState struct {
	// Status is one of: pending, new_review, approved, clean, no_change,
	// no_reviews, merged, closed, error — mirrors pr-review.sh status output.
	Status string `json:"status"`

	// ReviewID is the GitHub review ID string (empty if no review exists yet).
	ReviewID string `json:"review_id,omitempty"`

	// CommentCount is the number of unresolved top-level Copilot comments.
	CommentCount int `json:"comment_count,omitempty"`
}

// Comment is a single Copilot review comment.
type Comment struct {
	ID          int64  `json:"id"`
	Path        string `json:"path"`
	Line        int    `json:"line"`
	Body        string `json:"body"`
	InReplyToID *int64 `json:"in_reply_to_id"`
}

// PRContext carries all parameters needed to build a fix prompt.
type PRContext struct {
	PR       string
	Repo     string
	CWD      string
	ReviewID string
	Comments []Comment
}

// GetReviewState fetches the current Copilot review state for the PR.
// It shells out to the pr-review.sh script to keep all GitHub API logic
// in one place during the transition from shell to Go.
func GetReviewState(scriptDir, repo, pr, cwd string) (ReviewState, error) {
	// pr-review.sh <pr> status --repo <repo> outputs JSON to stdout.
	cmd := exec.Command("bash",
		filepath.Join(scriptDir, "pr-review.sh"),
		pr, "status",
		"--repo", repo,
	)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return ReviewState{}, fmt.Errorf("agent: pr-review.sh status: exit %d: %s",
				exitErr.ExitCode(), string(exitErr.Stderr))
		}
		return ReviewState{}, fmt.Errorf("agent: pr-review.sh status: %w", err)
	}

	var rs ReviewState
	// Strip NUL bytes as pr-review.sh documents it may include them.
	clean := bytes.ReplaceAll(out, []byte{0}, nil)
	if err := json.Unmarshal(clean, &rs); err != nil {
		return ReviewState{}, fmt.Errorf("agent: parse review status: %w (raw: %q)", err, string(clean))
	}
	return rs, nil
}

// GetComments fetches the unresolved Copilot review comments for the PR.
func GetComments(scriptDir, repo, pr, cwd string) ([]Comment, error) {
	cmd := exec.Command("bash",
		filepath.Join(scriptDir, "pr-review.sh"),
		pr, "comments",
		"--repo", repo,
	)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("agent: pr-review.sh comments: exit %d: %s",
				exitErr.ExitCode(), string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("agent: pr-review.sh comments: %w", err)
	}

	clean := bytes.ReplaceAll(out, []byte{0}, nil)
	var wrapper struct {
		ReviewID string    `json:"review_id"`
		Count    int       `json:"count"`
		Comments []Comment `json:"comments"`
	}
	if err := json.Unmarshal(clean, &wrapper); err != nil {
		return nil, fmt.Errorf("agent: parse comments: %w", err)
	}
	return wrapper.Comments, nil
}

// BuildPrompt constructs the fix prompt that is passed verbatim to the agent CLI.
// The format matches the prompt used in pr-review-opencode.sh.
func BuildPrompt(ctx PRContext) (string, error) {
	commentsJSON, err := json.MarshalIndent(ctx.Comments, "", "  ")
	if err != nil {
		return "", fmt.Errorf("agent: marshal comments for prompt: %w", err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "You are fixing GitHub Copilot code review comments on PR #%s in %s.\n\n", ctx.PR, ctx.Repo)
	fmt.Fprintf(&b, "Local repo directory: %s\n", ctx.CWD)
	fmt.Fprintf(&b, "Review ID: %s\n", ctx.ReviewID)
	fmt.Fprintf(&b, "Total top-level comments: %d\n\n", countTopLevel(ctx.Comments))
	b.WriteString("## Review comments (JSON):\n```json\n")
	b.Write(commentsJSON)
	b.WriteString("\n```\n\n")
	b.WriteString("Each comment has: id, path (file), line, body (the review text), in_reply_to_id (null = top-level).\n\n")
	b.WriteString("## Your task\n\n")
	b.WriteString("1. For each top-level comment (in_reply_to_id == null):\n")
	b.WriteString("   a. Read `" + ctx.CWD + "/<path>`\n")
	b.WriteString("   b. Fix the issue at/around the given line\n")
	b.WriteString("   c. Make the minimal targeted change only\n\n")
	b.WriteString("2. Commit and push all fixes at once:\n")
	b.WriteString("   ```bash\n")
	b.WriteString("   cd \"" + ctx.CWD + "\" && git add -A && git commit -m \"fix: address Copilot review comments\" && git push\n")
	b.WriteString("   ```\n")
	b.WriteString("   (Skip commit/push if there are genuinely no code changes needed.)\n\n")
	b.WriteString("3. Request a new Copilot review:\n")
	b.WriteString("   ```bash\n")
	b.WriteString("   gh api repos/" + ctx.Repo + "/pulls/" + ctx.PR + "/requested_reviewers")
	b.WriteString(` -X POST --input - <<< '{"reviewers":["copilot-pull-request-reviewer[bot]"]}'` + "\n")
	b.WriteString("   ```\n\n")
	b.WriteString("4. Reply to every top-level comment:\n")
	b.WriteString("   ```bash\n")
	b.WriteString("   gh api repos/" + ctx.Repo + "/pulls/" + ctx.PR + "/comments/<id>/replies -X POST -f body=\"Fixed: <description> ✅\"\n")
	b.WriteString("   ```\n\n")
	b.WriteString("## Rules\n")
	b.WriteString("- Fix all comments before committing (one commit for all fixes)\n")
	b.WriteString("- Only change what each comment asks — no refactoring beyond the comment scope\n")
	b.WriteString("- Always request a new Copilot review after pushing (step 3)\n")
	b.WriteString("- Reply to every top-level comment (step 4)\n")
	b.WriteString("- If a comment is already fixed in the current code, still reply to confirm it\n")
	return b.String(), nil
}

// PushAndRequestReview commits staged changes (if any), pushes, and re-requests
// Copilot review. This matches the `pr-review.sh push` subcommand flow.
func PushAndRequestReview(scriptDir, repo, pr, cwd string) error {
	cmd := exec.Command("bash",
		filepath.Join(scriptDir, "pr-review.sh"),
		pr, "push",
		"--repo", repo,
	)
	cmd.Dir = cwd
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("agent: pr-review.sh push: %w", err)
	}
	return nil
}

// ScriptDir attempts to locate the scripts/ directory relative to the CWD.
// It walks up from cwd to the filesystem root until it finds a directory
// containing pr-review.sh.
func ScriptDir(cwd string) (string, error) {
	dir := cwd
	for {
		candidate := filepath.Join(dir, "scripts")
		if _, err := os.Stat(filepath.Join(candidate, "pr-review.sh")); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
		}
		dir = parent
	}
	return "", fmt.Errorf("agent: cannot locate scripts/pr-review.sh relative to %s", cwd)
}

// countTopLevel returns the number of comments with InReplyToID == nil.
func countTopLevel(comments []Comment) int {
	n := 0
	for _, c := range comments {
		if c.InReplyToID == nil {
			n++
		}
	}
	return n
}
