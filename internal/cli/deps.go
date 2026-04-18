package cli

// deps.go — first-time dependency detection with guided, styled setup output.
//
// When RINSE is launched and required tools are missing, or the user has not
// authenticated with GitHub CLI, this file prints clear, actionable error
// messages — styled with the RINSE Lip Gloss palette — before exiting.
//
// Call CheckDependencies() and CheckGHAuth() from TryDispatch() (or from
// main()) before tui.Run() so errors are surfaced immediately rather than
// manifesting as cryptic failures deep inside the TUI.

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/orsharon7/rinse/internal/theme"
)

// ── Dependency registry ───────────────────────────────────────────────────────

// depSpec describes a required external dependency.
type depSpec struct {
	cmd         string   // executable name looked up in PATH
	name        string   // human-readable display name
	description string   // one-line role description
	installDocs string   // URL for full installation documentation
	installHint []string // OS-specific quick-install commands shown inline
}

// requiredDeps lists every tool RINSE needs to function.
var requiredDeps = []depSpec{
	{
		cmd:         "git",
		name:        "Git",
		description: "version control — detects the current repository and branch",
		installDocs: "https://git-scm.com/downloads",
		installHint: osHints(
			"brew install git",
			"sudo apt install git        # Debian/Ubuntu\n  sudo dnf install git        # Fedora",
			"winget install --id Git.Git # Windows",
		),
	},
	{
		cmd:         "gh",
		name:        "GitHub CLI (gh)",
		description: "GitHub API access — lists PRs, fetches review state, triggers actions",
		installDocs: "https://cli.github.com",
		installHint: osHints(
			"brew install gh",
			"sudo apt install gh         # Debian/Ubuntu (GitHub apt repo)\n  sudo dnf install gh         # Fedora",
			"winget install --id GitHub.cli # Windows",
		),
	},
}

// osHints picks the platform-appropriate install hint.
// Pass hints in order: mac, linux, windows.  Use "" to skip a platform.
func osHints(mac, linux, windows string) []string {
	switch runtime.GOOS {
	case "darwin":
		if mac != "" {
			return []string{mac}
		}
	case "linux":
		if linux != "" {
			return []string{linux}
		}
	case "windows":
		if windows != "" {
			return []string{windows}
		}
	}
	return nil
}

// ── Missing dep record ────────────────────────────────────────────────────────

type missingDep struct {
	spec depSpec
}

// ── Public API ────────────────────────────────────────────────────────────────

// CheckDependencies inspects PATH for each required dependency.
// If any are missing it prints a styled error and exits 1.
// Returns normally when all dependencies are present.
func CheckDependencies() {
	var missing []missingDep
	for _, d := range requiredDeps {
		if _, err := exec.LookPath(d.cmd); err != nil {
			missing = append(missing, missingDep{spec: d})
		}
	}
	if len(missing) > 0 {
		printDepError(missing)
		os.Exit(1)
	}
}

// CheckGHAuth verifies that `gh auth status` succeeds (user is logged in).
// If not authenticated it prints a styled error and exits 1.
// Must only be called after CheckDependencies() confirms gh is present.
func CheckGHAuth() {
	cmd := exec.Command("gh", "auth", "status")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		printAuthError()
		os.Exit(1)
	}
}

// ── Styled error printers ─────────────────────────────────────────────────────

func printDepError(missing []missingDep) {
	fmt.Fprintln(os.Stderr)

	for i, m := range missing {
		if i > 0 {
			fmt.Fprintln(os.Stderr)
		}
		fmt.Fprintln(os.Stderr, theme.StyleErr.Render("  ✗  "+m.spec.name))
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "     "+theme.StyleMuted.Render(m.spec.description))

		if len(m.spec.installHint) > 0 {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "     "+theme.StyleMuted.Render("Install:"))
			for _, hint := range m.spec.installHint {
				for _, line := range strings.Split(hint, "\n") {
					fmt.Fprintln(os.Stderr, "       "+theme.StyleTeal.Render("$ "+strings.TrimSpace(line)))
				}
			}
		}
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "     "+theme.StyleMuted.Render("Docs: "+m.spec.installDocs))
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprint(os.Stderr, "  "+theme.StyleMuted.Render("After installing, re-run:  "))
	fmt.Fprintln(os.Stderr, theme.StyleVal.Render("rinse"))
	fmt.Fprintln(os.Stderr)
}

func printAuthError() {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, theme.StyleErr.Render("  ✗  GitHub CLI is not authenticated"))
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "     "+theme.StyleMuted.Render("RINSE needs GitHub CLI to be signed in to access your PRs."))
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "     "+theme.StyleMuted.Render("Run:"))
	fmt.Fprintln(os.Stderr, "       "+theme.StyleTeal.Render("$ gh auth login"))
	fmt.Fprintln(os.Stderr)
	fmt.Fprint(os.Stderr, "  "+theme.StyleMuted.Render("Then re-run:  "))
	fmt.Fprintln(os.Stderr, theme.StyleVal.Render("rinse"))
	fmt.Fprintln(os.Stderr)
}
