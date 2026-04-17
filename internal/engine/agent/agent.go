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
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/orsharon7/rinse/internal/ignore"
)

// ReviewState is the parsed outcome of `pr-review.sh status` (which uses `gh api`) for a Copilot review.
type ReviewState struct {
	// Status is one of: pending, new_review, approved, clean, no_change,
	// no_reviews, merged, closed, error — mirrors pr-review.sh status output.
	Status string `json:"status"`

	// ReviewID is the GitHub review ID string (empty if no review exists yet).
	ReviewID string `json:"review_id,omitempty"`

	// CommentCount is the raw comment_count reported by pr-review.sh status.
	// It reflects the total review comments returned by that command's JSON.
	CommentCount int `json:"comment_count,omitempty"`

	// Message carries the human-readable error message emitted by pr-review.sh
	// when Status == "error", so callers can surface actionable diagnostics.
	Message string `json:"message,omitempty"`
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
// lastKnownReviewID, if non-empty, is passed via --last-known so the script
// can return "no_change" instead of re-surfacing an already-processed review.
func GetReviewState(scriptDir, repo, pr, cwd, lastKnownReviewID string) (ReviewState, error) {
	// pr-review.sh <pr> status --repo <repo> [--last-known <id>] outputs JSON to stdout.
	args := []string{
		filepath.Join(scriptDir, "pr-review.sh"),
		pr, "status",
		"--repo", repo,
	}
	if strings.TrimSpace(lastKnownReviewID) != "" {
		args = append(args, "--last-known", lastKnownReviewID)
	}
	cmd := exec.Command("bash", args...)
	cmd.Dir = cwd
	var stderrBuf bytes.Buffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stdout := strings.TrimSpace(string(out))
			stderr := strings.TrimSpace(stderrBuf.String())
			switch {
			case stdout != "" && stderr != "":
				return ReviewState{}, fmt.Errorf(
					"agent: pr-review.sh status: exit %d: stdout: %s; stderr: %s",
					exitErr.ExitCode(), stdout, stderr,
				)
			case stdout != "":
				return ReviewState{}, fmt.Errorf(
					"agent: pr-review.sh status: exit %d: stdout: %s",
					exitErr.ExitCode(), stdout,
				)
			case stderr != "":
				return ReviewState{}, fmt.Errorf(
					"agent: pr-review.sh status: exit %d: stderr: %s",
					exitErr.ExitCode(), stderr,
				)
			default:
				return ReviewState{}, fmt.Errorf("agent: pr-review.sh status: exit %d", exitErr.ExitCode())
			}
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
// If reviewID is non-empty, --review-id is passed to pr-review.sh so that
// comments are fetched for the same review that GetReviewState returned,
// avoiding a TOCTOU race where a newer review lands between the two calls.
func GetComments(scriptDir, repo, pr, cwd, reviewID string) ([]Comment, error) {
	args := []string{
		filepath.Join(scriptDir, "pr-review.sh"),
		pr, "comments",
		"--repo", repo,
	}
	if reviewID != "" {
		args = append(args, "--review-id", reviewID)
	}

	cmd := exec.Command("bash", args...)
	cmd.Dir = cwd
	var stderrBuf bytes.Buffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stdout := strings.TrimSpace(string(out))
			stderr := strings.TrimSpace(stderrBuf.String())
			switch {
			case stdout != "" && stderr != "":
				return nil, fmt.Errorf(
					"agent: pr-review.sh comments: exit %d: stdout: %s; stderr: %s",
					exitErr.ExitCode(), stdout, stderr,
				)
			case stdout != "":
				return nil, fmt.Errorf(
					"agent: pr-review.sh comments: exit %d: stdout: %s",
					exitErr.ExitCode(), stdout,
				)
			case stderr != "":
				return nil, fmt.Errorf(
					"agent: pr-review.sh comments: exit %d: stderr: %s",
					exitErr.ExitCode(), stderr,
				)
			default:
				return nil, fmt.Errorf("agent: pr-review.sh comments: exit %d", exitErr.ExitCode())
			}
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
	b.WriteString("   gh api repos/" + ctx.Repo + "/pulls/" + ctx.PR + `/requested_reviewers -X POST --input - <<< '{"reviewers":["copilot-pull-request-reviewer[bot]"]}'` + "\n")
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

// PushAndRequestReview delegates to pr-review.sh push and then pr-review.sh
// request; depending on repository state, the push step may stage, commit,
// and push changes before re-requesting Copilot review.
func PushAndRequestReview(scriptDir, repo, pr, cwd string) error {
	pushCmd := exec.Command("bash",
		filepath.Join(scriptDir, "pr-review.sh"),
		pr, "push",
		"--repo", repo,
	)
	pushCmd.Dir = cwd
	pushCmd.Stdout = os.Stdout
	pushCmd.Stderr = os.Stderr
	pushCmd.Stdin = os.Stdin
	if err := pushCmd.Run(); err != nil {
		return fmt.Errorf("agent: pr-review.sh push: %w", err)
	}

	requestCmd := exec.Command("bash",
		filepath.Join(scriptDir, "pr-review.sh"),
		pr, "request",
		"--repo", repo,
	)
	requestCmd.Dir = cwd
	requestCmd.Stdout = os.Stdout
	requestCmd.Stderr = os.Stderr
	requestCmd.Stdin = os.Stdin
	if err := requestCmd.Run(); err != nil {
		return fmt.Errorf("agent: pr-review.sh request: %w", err)
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
		scriptPath := filepath.Join(candidate, "pr-review.sh")
		if _, err := os.Stat(scriptPath); err == nil {
			return candidate, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("agent: stat %s: %w", scriptPath, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
		}
		dir = parent
	}
	return "", fmt.Errorf("agent: cannot locate scripts/pr-review.sh relative to %s", cwd)
}

// SplitByIgnore partitions comments into two groups using the provided Matcher:
//   - active: comments whose file paths are NOT ignored (should be fixed)
//   - skipped: comments whose file paths match an ignore pattern (should be acknowledged)
//
// Only top-level comments (InReplyToID == nil) are ever placed in skipped;
// replies to ignored comments are dropped entirely because the parent thread
// will be acknowledged and closed by AcknowledgeIgnored.
func SplitByIgnore(comments []Comment, m ignore.Matcher) (active, skipped []Comment) {
	// Build a set of top-level ignored IDs so we can drop their replies too.
	ignoredIDs := map[int64]bool{}
	for _, c := range comments {
		if c.InReplyToID == nil && m.Matches(c.Path) {
			ignoredIDs[c.ID] = true
		}
	}

	for _, c := range comments {
		switch {
		case c.InReplyToID == nil && ignoredIDs[c.ID]:
			skipped = append(skipped, c)
		case c.InReplyToID != nil && ignoredIDs[*c.InReplyToID]:
			// Drop replies to ignored top-level comments.
		default:
			active = append(active, c)
		}
	}
	return active, skipped
}

// AcknowledgeIgnored posts a reply to each skipped comment explaining that
// the file is excluded by .rinseignore. Errors are logged but non-fatal so
// that a transient gh CLI failure does not abort the entire review cycle.
func AcknowledgeIgnored(repo, pr string, skipped []Comment, logger func(format string, args ...any)) {
	const replyBody = "Skipped — file is excluded by `.rinseignore`. RINSE will not auto-fix comments on this path."
	for _, c := range skipped {
		if c.InReplyToID != nil {
			continue // only reply to top-level comments
		}
		args := []string{
			"api",
			fmt.Sprintf("repos/%s/pulls/%s/comments/%d/replies", repo, pr, c.ID),
			"-X", "POST",
			"-f", "body=" + replyBody,
		}
		cmd := exec.Command("gh", args...)
		cmd.Stdout = os.Stderr // route to stderr so it doesn't pollute structured output
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			if logger != nil {
				logger("agent: acknowledge ignored comment %d on %s: %v", c.ID, c.Path, err)
			}
		}
	}
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
