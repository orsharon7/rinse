// Package opencode implements engine.Agent using the opencode CLI.
//
// It invokes:
//
//	opencode run --model <model> --dangerously-skip-permissions <prompt>
//
// in the PR's working directory, then delegates push+re-review to the shared
// agent.PushAndRequestReview helper (which shells out to pr-review.sh push
// and pr-review.sh request).
package opencode

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/orsharon7/rinse/internal/engine"
	"github.com/orsharon7/rinse/internal/engine/agent"
)

// DefaultModel is the opencode model string used when RunOpts.Model is empty.
const DefaultModel = "github-copilot/claude-sonnet-4.6"

// Agent implements engine.Agent using the opencode CLI.
type Agent struct {
	// ScriptDir is the path to the scripts/ directory containing pr-review.sh.
	// If empty, ScriptDir is discovered automatically relative to RunOpts.CWD.
	ScriptDir string
}

var _ engine.Agent = (*Agent)(nil)

// Name returns the human-readable agent name.
func (a *Agent) Name() string { return "opencode" }

// Run executes one PR review fix iteration using opencode:
//  1. Fetch current Copilot review state and comments.
//  2. If no review / no comments: return early (nothing to fix).
//  3. Build the fix prompt and invoke `opencode run`.
//  4. Push changes and re-request Copilot review via pr-review.sh push.
func (a *Agent) Run(opts engine.RunOpts) (engine.Result, error) {
	scriptDir, err := a.scriptDir(opts.CWD)
	if err != nil {
		return engine.Result{}, err
	}

	// 1. Review state.
	rs, err := agent.GetReviewState(scriptDir, opts.Repo, opts.PR, opts.CWD, opts.LastKnownReviewID)
	if err != nil {
		return engine.Result{}, fmt.Errorf("opencode: get review state: %w", err)
	}

	switch rs.Status {
	case "approved", "clean":
		return engine.Result{Approved: true}, nil
	case "merged", "closed":
		// PR is done — treat as approved for loop exit purposes.
		return engine.Result{Approved: true}, nil
	case "pending", "no_reviews", "no_change":
		// Nothing actionable yet; signal Waiting so the runner doesn't count this iteration.
		return engine.Result{Waiting: true}, nil
	case "error":
		return engine.Result{}, fmt.Errorf("opencode: review status error for PR %s", opts.PR)
	case "new_review":
		// fall through to fix
	default:
		return engine.Result{}, fmt.Errorf("opencode: unknown review status %q for PR %s", rs.Status, opts.PR)
	}

	// 2. Fetch comments.
	comments, err := agent.GetComments(scriptDir, opts.Repo, opts.PR, opts.CWD)
	if err != nil {
		return engine.Result{}, fmt.Errorf("opencode: get comments: %w", err)
	}
	if len(comments) == 0 {
		return engine.Result{Comments: 0}, nil
	}

	// 3. Build prompt and invoke opencode.
	prompt, err := agent.BuildPrompt(agent.PRContext{
		PR:       opts.PR,
		Repo:     opts.Repo,
		CWD:      opts.CWD,
		ReviewID: rs.ReviewID,
		Comments: comments,
	})
	if err != nil {
		return engine.Result{}, err
	}

	model := opts.Model
	if model == "" {
		model = DefaultModel
	}

	if err := runOpencode(opts.CWD, model, prompt); err != nil {
		return engine.Result{}, fmt.Errorf("opencode: run: %w", err)
	}

	// 4. Push + re-request review. opencode is expected to do this itself
	// (the prompt instructs it), but we belt-and-suspenders via pr-review.sh push
	// to guarantee the review request even if opencode skips that step.
	if err := agent.PushAndRequestReview(scriptDir, opts.Repo, opts.PR, opts.CWD); err != nil {
		// Non-fatal if opencode already pushed — log and continue.
		_, _ = fmt.Fprintf(os.Stderr, "opencode: push warning: %v\n", err)
	}

	return engine.Result{Comments: len(comments)}, nil
}

// runOpencode invokes the opencode CLI.
func runOpencode(cwd, model, prompt string) error {
	cmd := exec.Command("opencode", "run",
		"--model", model,
		"--dangerously-skip-permissions",
		prompt,
	)
	cmd.Dir = cwd
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("opencode exited with code %d", exitErr.ExitCode())
		}
		return fmt.Errorf("opencode: %w", err)
	}
	return nil
}

// scriptDir resolves the scripts/ directory, using the override if set.
func (a *Agent) scriptDir(cwd string) (string, error) {
	if a.ScriptDir != "" {
		return a.ScriptDir, nil
	}
	return agent.ScriptDir(cwd)
}
