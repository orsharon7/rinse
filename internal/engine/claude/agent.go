// Package claude implements engine.Agent using the Claude Code CLI.
//
// It invokes:
//
//	claude --print --dangerously-skip-permissions <prompt>
//
// in the PR's working directory, then delegates push+re-review to the shared
// agent.PushAndRequestReview helper.
package claude

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/orsharon7/rinse/internal/engine"
	"github.com/orsharon7/rinse/internal/engine/agent"
)

// Agent implements engine.Agent using the Claude Code CLI (`claude`).
type Agent struct {
	// ScriptDir is the path to the scripts/ directory containing pr-review.sh.
	// If empty, ScriptDir is discovered automatically relative to RunOpts.CWD.
	ScriptDir string
}

var _ engine.Agent = (*Agent)(nil)

// Name returns the human-readable agent name.
func (a *Agent) Name() string { return "claude" }

// Run executes one PR review fix iteration using the claude CLI:
//  1. Fetch current Copilot review state and comments.
//  2. If no review / no comments: return early.
//  3. Build the fix prompt and invoke `claude --print`.
//  4. Push changes and re-request Copilot review.
func (a *Agent) Run(opts engine.RunOpts) (engine.Result, error) {
	scriptDir, err := a.scriptDir(opts.CWD)
	if err != nil {
		return engine.Result{}, err
	}

	// 1. Review state.
	rs, err := agent.GetReviewState(scriptDir, opts.Repo, opts.PR, opts.CWD)
	if err != nil {
		return engine.Result{}, fmt.Errorf("claude: get review state: %w", err)
	}

	switch rs.Status {
	case "approved", "clean":
		return engine.Result{Approved: true}, nil
	case "merged", "closed":
		return engine.Result{Approved: true}, nil
	case "pending", "no_reviews", "no_change":
		return engine.Result{Comments: 0}, nil
	case "error":
		return engine.Result{}, fmt.Errorf("claude: review status error for PR %s", opts.PR)
	case "new_review":
		// fall through to fix
	default:
		return engine.Result{}, fmt.Errorf("claude: unknown review status %q for PR %s", rs.Status, opts.PR)
	}

	// 2. Fetch comments.
	comments, err := agent.GetComments(scriptDir, opts.Repo, opts.PR, opts.CWD)
	if err != nil {
		return engine.Result{}, fmt.Errorf("claude: get comments: %w", err)
	}
	if len(comments) == 0 {
		return engine.Result{Comments: 0}, nil
	}

	// 3. Build prompt and invoke claude.
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

	if err := runClaude(opts.CWD, opts.Model, prompt); err != nil {
		return engine.Result{}, fmt.Errorf("claude: run: %w", err)
	}

	// 4. Push + re-request review (belt-and-suspenders).
	if err := agent.PushAndRequestReview(scriptDir, opts.Repo, opts.PR, opts.CWD); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "claude: push warning: %v\n", err)
	}

	return engine.Result{Comments: len(comments)}, nil
}

// runClaude invokes the claude CLI.
// The --model flag is only included when a model override is specified;
// claude uses its default when omitted.
func runClaude(cwd, model, prompt string) error {
	args := []string{"--print", "--dangerously-skip-permissions"}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, prompt)

	cmd := exec.Command("claude", args...)
	cmd.Dir = cwd
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = nil

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("claude exited with code %d", exitErr.ExitCode())
		}
		return fmt.Errorf("claude: %w", err)
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
