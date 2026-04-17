// Package engine defines the Agent interface for executing PR review cycles.
// Implementations (opencode, claude) will replace the shell scripts over time.
package engine

// Agent is the interface for running a single PR review cycle iteration.
// Implementations read Copilot comments, apply fixes, and report results.
type Agent interface {
	// Name returns the human-readable runner name (e.g. "opencode", "claude").
	Name() string

	// Run executes one fix iteration for the given PR in the given repo working
	// directory. It returns an error only on hard failure; soft outcomes (no
	// comments, approved) are expressed through Result.
	Run(opts RunOpts) (Result, error)
}

// RunOpts carries all parameters for a single review-cycle iteration.
type RunOpts struct {
	PR    string // PR number as string
	Repo  string // owner/repo
	CWD   string // local working directory
	Model string // model override (empty = use runner default)
}

// Result captures the outcome of one fix iteration.
type Result struct {
	// Comments is the number of Copilot comments addressed in this iteration.
	Comments int

	// Approved reports whether Copilot approved the PR in this iteration.
	Approved bool
}
