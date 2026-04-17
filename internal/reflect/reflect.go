// Package reflect implements the RINSE reflection and optimization steps in Go.
//
// Reflection extracts generalizable coding rules from Copilot review comments
// and writes them to AGENTS.md on the main branch via a git worktree — never
// polluting the PR branch. Optimization consolidates the rules section to
// remove duplication and reduce token cost.
package reflect

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Opts configures a single reflection pass.
type Opts struct {
	// PR is the pull request number as a string.
	PR string

	// Repo is the "owner/repo" string.
	Repo string

	// CWD is the local repo working directory (PR branch checkout).
	CWD string

	// MainBranch is the branch to which rules are committed (e.g. "main").
	// Defaults to "main" when empty.
	MainBranch string

	// CommentsJSON is the raw JSON of the Copilot review comments. When set,
	// the gh API call to fetch comments is skipped. Used by the runner to pass
	// the already-fetched comments from the fix iteration.
	CommentsJSON string

	// Model is the AI model to use for reflection. Defaults to
	// "github-copilot/claude-sonnet-4.6" when empty.
	Model string

	// AgentCLI selects the AI CLI ("opencode" or "claude"). Defaults to
	// "opencode" when empty.
	AgentCLI string

	// LineSink receives human-readable progress lines (optional).
	LineSink func(string)
}

// OptimizeOpts configures a single optimization pass.
type OptimizeOpts struct {
	// Repo is the "owner/repo" string.
	Repo string

	// CWD is the local repo working directory.
	CWD string

	// MainBranch is the branch that holds AGENTS.md. Defaults to "main".
	MainBranch string

	// Model is the AI model to use. Defaults to
	// "github-copilot/claude-sonnet-4.6" when empty.
	Model string

	// AgentCLI selects the AI CLI ("opencode" or "claude"). Defaults to
	// "opencode" when empty.
	AgentCLI string

	// LineSink receives human-readable progress lines (optional).
	LineSink func(string)
}

const defaultModel = "github-copilot/claude-sonnet-4.6"
const defaultAgentCLI = "opencode"

// Reflect runs one reflection pass: fetches Copilot review comments, builds a
// prompt, invokes the AI agent in a git worktree on the main branch, and
// commits any rule additions to AGENTS.md.
//
// Reflect never writes to the PR branch. It uses a temporary worktree checked
// out at origin/<MainBranch> and removes it on exit (trap equivalent via defer).
func Reflect(opts Opts) error {
	if err := validateReflectOpts(&opts); err != nil {
		return err
	}
	emit := opts.LineSink

	log := func(msg string) {
		if emit != nil {
			emit(fmt.Sprintf("◎ reflect | %s", msg))
		}
	}

	log(fmt.Sprintf("starting  (model: %s → %s)", opts.Model, opts.MainBranch))

	// ── 1. Fetch comments if not pre-supplied ─────────────────────────────────
	commentsJSON := opts.CommentsJSON
	if commentsJSON == "" {
		var err error
		commentsJSON, err = fetchCopilotComments(opts.Repo, opts.PR)
		if err != nil {
			return fmt.Errorf("reflect: fetch comments: %w", err)
		}
	}

	// Quick sanity check — skip if the array is empty or null.
	if isEmpty(commentsJSON) {
		log("no comments to reflect on — skipping")
		return nil
	}

	// ── 2. Set up git worktree on main ────────────────────────────────────────
	worktreeDir, cleanup, err := createWorktree(opts.CWD, opts.MainBranch)
	if err != nil {
		return fmt.Errorf("reflect: create worktree: %w", err)
	}
	defer cleanup()

	log(fmt.Sprintf("worktree ready at %s", worktreeDir))

	// Ensure CLAUDE.md → AGENTS.md symlink exists in worktree.
	ensureSymlink(worktreeDir)

	// ── 3. Read current AGENTS.md content ────────────────────────────────────
	agentsPath := filepath.Join(worktreeDir, "AGENTS.md")
	agentsContent, err := os.ReadFile(agentsPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reflect: read AGENTS.md: %w", err)
	}

	// ── 4. Build prompt ────────────────────────────────────────────────────────
	prompt := buildReflectPrompt(opts.PR, opts.Repo, worktreeDir, commentsJSON, string(agentsContent))

	// ── 5. Invoke AI agent in the worktree ────────────────────────────────────
	log(fmt.Sprintf("invoking %s", opts.AgentCLI))
	if err := runAgent(opts.AgentCLI, opts.Model, worktreeDir, prompt); err != nil {
		return fmt.Errorf("reflect: run agent: %w", err)
	}

	// ── 6. Validate and commit ────────────────────────────────────────────────
	if err := commitReflect(worktreeDir, opts.MainBranch, opts.PR, log); err != nil {
		return fmt.Errorf("reflect: commit: %w", err)
	}

	log("complete")
	return nil
}

// Optimize consolidates the <!-- BEGIN:COPILOT-RULES --> section in AGENTS.md
// to remove duplication and reduce token cost. It commits any changes to the
// main branch via a git worktree.
func Optimize(opts OptimizeOpts) error {
	if err := validateOptimizeOpts(&opts); err != nil {
		return err
	}

	log := func(msg string) {
		if opts.LineSink != nil {
			opts.LineSink(fmt.Sprintf("◎ reflect | optimize: %s", msg))
		}
	}

	log(fmt.Sprintf("starting  (model: %s → %s)", opts.Model, opts.MainBranch))

	worktreeDir, cleanup, err := createWorktree(opts.CWD, opts.MainBranch)
	if err != nil {
		return fmt.Errorf("optimize: create worktree: %w", err)
	}
	defer cleanup()

	agentsPath := filepath.Join(worktreeDir, "AGENTS.md")
	agentsContent, err := os.ReadFile(agentsPath)
	if err != nil {
		return fmt.Errorf("optimize: read AGENTS.md: %w", err)
	}

	prompt := buildOptimizePrompt(worktreeDir, string(agentsContent))

	log(fmt.Sprintf("invoking %s", opts.AgentCLI))
	if err := runAgent(opts.AgentCLI, opts.Model, worktreeDir, prompt); err != nil {
		return fmt.Errorf("optimize: run agent: %w", err)
	}

	// Commit if changed.
	changed, err := gitStatusChanged(worktreeDir, "AGENTS.md", "CLAUDE.md")
	if err != nil {
		return fmt.Errorf("optimize: git status: %w", err)
	}
	if !changed {
		log("no changes to commit")
		return nil
	}

	ensureSymlink(worktreeDir)

	if err := gitAddAndPush(worktreeDir, opts.MainBranch,
		"chore: optimize AI coding rules in AGENTS.md [skip ci]"); err != nil {
		return fmt.Errorf("optimize: git push: %w", err)
	}

	log("complete")
	return nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func validateReflectOpts(o *Opts) error {
	if o.Repo == "" {
		return errors.New("reflect: Opts.Repo is required")
	}
	if o.PR == "" {
		return errors.New("reflect: Opts.PR is required")
	}
	if o.CWD == "" {
		return errors.New("reflect: Opts.CWD is required")
	}
	if o.MainBranch == "" {
		o.MainBranch = "main"
	}
	if o.Model == "" {
		o.Model = defaultModel
	}
	if o.AgentCLI == "" {
		o.AgentCLI = defaultAgentCLI
	}
	return nil
}

func validateOptimizeOpts(o *OptimizeOpts) error {
	if o.Repo == "" {
		return errors.New("optimize: OptimizeOpts.Repo is required")
	}
	if o.CWD == "" {
		return errors.New("optimize: OptimizeOpts.CWD is required")
	}
	if o.MainBranch == "" {
		o.MainBranch = "main"
	}
	if o.Model == "" {
		o.Model = defaultModel
	}
	if o.AgentCLI == "" {
		o.AgentCLI = defaultAgentCLI
	}
	return nil
}

// fetchCopilotComments fetches unresolved Copilot review comments for the PR
// using the gh CLI and returns them as a JSON string.
func fetchCopilotComments(repo, pr string) (string, error) {
	// Use the gh API to fetch review comments, filtering to Copilot bot.
	out, err := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/pulls/%s/reviews", repo, pr),
		"--jq", `[.[] | select(.user.login == "copilot-pull-request-reviewer[bot]") | .id] | last`,
	).Output()
	if err != nil {
		return "", fmt.Errorf("gh api: list reviews: %w", err)
	}
	reviewID := strings.TrimSpace(string(out))
	if reviewID == "" || reviewID == "null" {
		return "[]", nil
	}

	comments, err := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/pulls/%s/reviews/%s/comments", repo, pr, reviewID),
		"--jq", `[.[] | {id, path, line, body, in_reply_to_id}]`,
	).Output()
	if err != nil {
		return "", fmt.Errorf("gh api: review comments: %w", err)
	}
	return strings.TrimSpace(string(comments)), nil
}

// isEmpty returns true if json is an empty array, null, or blank.
func isEmpty(jsonStr string) bool {
	trimmed := strings.TrimSpace(jsonStr)
	if trimmed == "" || trimmed == "null" || trimmed == "[]" {
		return true
	}
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &arr); err == nil && len(arr) == 0 {
		return true
	}
	return false
}

// createWorktree creates a temporary git worktree checked out at
// origin/<branch> and returns the directory path and a cleanup function.
func createWorktree(cwd, branch string) (dir string, cleanup func(), err error) {
	// Prune stale worktree references from previous crashed runs.
	_ = exec.Command("git", "-C", cwd, "worktree", "prune").Run()

	// Fetch so that origin/<branch> is up to date.
	if out, ferr := exec.Command("git", "-C", cwd, "fetch", "origin", branch).CombinedOutput(); ferr != nil {
		return "", nil, fmt.Errorf("git fetch origin %s: %w (output: %s)", branch, ferr, string(out))
	}

	tmpDir, err := os.MkdirTemp("", "pr-reflect-worktree.*")
	if err != nil {
		return "", nil, fmt.Errorf("mktemp: %w", err)
	}

	if out, werr := exec.Command("git", "-C", cwd, "worktree", "add", "--detach", tmpDir,
		"origin/"+branch).CombinedOutput(); werr != nil {
		_ = os.RemoveAll(tmpDir)
		return "", nil, fmt.Errorf("git worktree add: %w (output: %s)", werr, string(out))
	}

	cleanup = func() {
		_ = exec.Command("git", "-C", cwd, "worktree", "remove", "--force", tmpDir).Run()
		_ = os.RemoveAll(tmpDir)
	}
	return tmpDir, cleanup, nil
}

// ensureSymlink ensures CLAUDE.md → AGENTS.md exists in the worktree.
// If CLAUDE.md is a regular file (written by an agent), it is replaced with
// the symlink.
func ensureSymlink(dir string) {
	claudePath := filepath.Join(dir, "CLAUDE.md")
	agentsPath := filepath.Join(dir, "AGENTS.md")

	// Only create symlink if AGENTS.md exists.
	if _, err := os.Stat(agentsPath); err != nil {
		return
	}

	info, err := os.Lstat(claudePath)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			// Already a symlink — verify target.
			target, lerr := os.Readlink(claudePath)
			if lerr == nil && target == "AGENTS.md" {
				return // correct
			}
		}
		// Regular file or wrong symlink — replace.
		_ = os.Remove(claudePath)
	}
	_ = os.Symlink("AGENTS.md", claudePath)
}

// runAgent invokes the AI CLI in dir with the given prompt.
func runAgent(cli, model, dir, prompt string) error {
	var cmd *exec.Cmd
	switch cli {
	case "claude":
		cmd = exec.Command("claude", "--print", "--dangerously-skip-permissions", "--model", model, prompt)
	default: // opencode
		cmd = exec.Command("opencode", "run", "--model", model, "--dangerously-skip-permissions", prompt)
	}
	cmd.Dir = dir
	cmd.Stdout = os.Stderr // route to stderr so it doesn't interfere with the TUI
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// commitReflect checks whether AGENTS.md or CLAUDE.md changed, ensures the
// symlink invariant, and commits + pushes to the main branch.
func commitReflect(worktreeDir, mainBranch, pr string, log func(string)) error {
	changed, err := gitStatusChanged(worktreeDir, "AGENTS.md", "CLAUDE.md")
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	if !changed {
		log("no changes to AGENTS.md — nothing to commit")
		return nil
	}

	// Re-assert symlink invariant after agent run.
	claudePath := filepath.Join(worktreeDir, "CLAUDE.md")
	info, err := os.Lstat(claudePath)
	if err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			log("⚠️  CLAUDE.md is no longer a symlink after agent run — restoring")
			_ = os.Remove(claudePath)
			_ = os.Symlink("AGENTS.md", claudePath)
		} else {
			target, _ := os.Readlink(claudePath)
			if target != "AGENTS.md" {
				log("⚠️  CLAUDE.md symlink target is unexpected — aborting to preserve repo invariant")
				_ = exec.Command("git", "-C", worktreeDir, "checkout", "--", "CLAUDE.md").Run()
				return fmt.Errorf("reflect: CLAUDE.md symlink target is %q, expected AGENTS.md", target)
			}
		}
	}

	// Count new rule lines (lines starting with "- " added inside the COPILOT-RULES block).
	rulesAdded := countAddedRules(worktreeDir)

	msg := fmt.Sprintf("chore: update AI coding rules from Copilot review #%s [skip ci]", pr)
	if err := gitAddAndPush(worktreeDir, mainBranch, msg); err != nil {
		return err
	}

	log(fmt.Sprintf("✓ done — +%d rule(s) pushed to %s", rulesAdded, mainBranch))
	return nil
}

// gitStatusChanged returns true if any of the given files have uncommitted changes.
func gitStatusChanged(dir string, files ...string) (bool, error) {
	args := append([]string{"-C", dir, "status", "--porcelain"}, files...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// gitAddAndPush stages the listed files, commits with msg, and pushes to branch.
func gitAddAndPush(dir, branch, msg string) error {
	if out, err := exec.Command("git", "-C", dir, "add", "AGENTS.md", "CLAUDE.md").CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %w (output: %s)", err, string(out))
	}

	commitArgs := []string{
		"-C", dir, "commit",
		"-m", msg,
		"--author", "RINSE <rinse@users.noreply.github.com>",
	}
	if out, err := exec.Command("git", commitArgs...).CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %w (output: %s)", err, string(out))
	}

	// Retry push up to 3 times with exponential backoff.
	var pushErr error
	for attempt := 1; attempt <= 3; attempt++ {
		out, err := exec.Command("git", "-C", dir, "push", "origin", "HEAD:"+branch).CombinedOutput()
		if err == nil {
			return nil
		}
		pushErr = fmt.Errorf("git push (attempt %d): %w (output: %s)", attempt, err, string(out))
		if attempt < 3 {
			// simple backoff: 2s, 4s
			waitSec := 1 << attempt // 2, 4
			_ = exec.Command("sleep", fmt.Sprintf("%d", waitSec)).Run()
		}
	}
	return pushErr
}

// countAddedRules counts lines starting with "+- " in the diff of AGENTS.md,
// which approximates new rule bullet-points added to the COPILOT-RULES section.
func countAddedRules(dir string) int {
	out, err := exec.Command("git", "-C", dir, "diff", "--cached", "AGENTS.md").Output()
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "+- ") {
			count++
		}
	}
	return count
}

// ── Prompt builders ───────────────────────────────────────────────────────────

func buildReflectPrompt(pr, repo, worktreeDir, commentsJSON, agentsContent string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `You are a code quality analyst. Your job is to extract reusable coding rules from GitHub Copilot review comments and add them permanently to this project's AI agent instruction files.

## Copilot review comments to analyse (PR #%s in %s):
`+"```json\n%s\n```\n\n", pr, repo, commentsJSON)

	fmt.Fprintf(&b, "## Current AGENTS.md:\n```markdown\n%s\n```\n\n", agentsContent)

	b.WriteString(`## Your task

1. Analyse the Copilot comments above and identify **generalizable coding patterns** — not PR-specific fixes but rules that should apply broadly across the codebase.

2. Express each rule as a concise bullet point (one line, starting with "- ").

3. Check the existing ` + "`<!-- BEGIN:COPILOT-RULES -->`" + ` section in each file (if present) and **do not duplicate rules that already exist there**.

4. Add NEW rules ONLY inside the delimited section in ` + fmt.Sprintf("`%s/AGENTS.md`:\n", worktreeDir) + `
   - Find ` + "`<!-- BEGIN:COPILOT-RULES -->`" + ` and ` + "`<!-- END:COPILOT-RULES -->`" + ` markers
   - Insert new rules between those markers
   - Preserve the existing marker lines exactly

Example of the section structure that must be preserved:
` + "```markdown\n<!-- BEGIN:COPILOT-RULES -->\n## Coding Guidelines (AI-maintained)\n- existing rule\n- new rule here\n<!-- END:COPILOT-RULES -->\n```\n\n")

	b.WriteString(`Note: CLAUDE.md is a symlink to AGENTS.md — do NOT write to it separately.

## Rules
- Only add rules that generalize beyond this specific PR
- Do not add rules that are already present
- Keep each rule concise (one bullet line)
- Do NOT run any git commands — the script will handle committing and pushing
- If there are no new generalizable rules, make no changes to the file
`)
	return b.String()
}

func buildOptimizePrompt(worktreeDir, agentsContent string) string {
	var b strings.Builder
	b.WriteString(`You are a code quality analyst. Your job is to consolidate and deduplicate the coding rules in this project's AGENTS.md file to reduce token cost without losing any guidance.

`)
	fmt.Fprintf(&b, "## Current AGENTS.md:\n```markdown\n%s\n```\n\n", agentsContent)

	fmt.Fprintf(&b, `## Your task

1. Read the `+"`<!-- BEGIN:COPILOT-RULES -->`"+` section in `+"`%s/AGENTS.md`"+`.

2. Consolidate the rules:
   - Merge rules that cover the same concept into a single, more precise rule
   - Remove exact or near-exact duplicates
   - Keep all unique guidance; do not drop rules without a clear reason
   - Preserve the section markers exactly

3. Rewrite the section with the consolidated rules.

## Rules
- Preserve `+"`<!-- BEGIN:COPILOT-RULES -->`"+` and `+"`<!-- END:COPILOT-RULES -->`"+` markers exactly
- Do NOT run any git commands — the script will handle committing and pushing
- If no consolidation is possible, make no changes
`, worktreeDir)
	return b.String()
}
