// Package runner implements the core PR review cycle loop that drives agents
// until Copilot approves or max iterations are reached.
// This package will replace the shell scripts in pr-review/ over time.
package runner

// TODO: Implement the core cycle loop in Go.
// This package will expose a Run() function that:
//   1. Detects PR review state (no review / pending / new comments / approved / merged)
//   2. Invokes the configured engine.Agent to fix comments
//   3. Pushes fixes and re-requests Copilot review
//   4. Loops until approved or max iterations reached
//   5. Optionally triggers the reflect step to extract coding rules
