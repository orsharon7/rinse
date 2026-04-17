package main

// deps.go — first-time dependency detection and guided setup for RINSE.
//
// When RINSE is launched and required tools are missing, this file provides
// clear, actionable error messages with installation instructions instead of
// cryptic failures or silent no-op behaviour.

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// dep describes a required external dependency.
type dep struct {
	cmd         string   // executable name to look up in PATH
	name        string   // human-readable name
	description string   // one-line role description
	installDocs string   // URL for full install docs
	installHint []string // OS-specific quick-install hints shown inline
}

// requiredDeps lists the minimal set of tools required to launch the app and
// run status checks. Runner scripts may require additional tools (e.g. bash, jq).
var requiredDeps = []dep{
	{
		cmd:         "git",
		name:        "Git",
		description: "version control — used to detect the current repository and branch",
		installDocs: "https://git-scm.com/downloads",
		installHint: osInstallHints(
			"brew install git",
			"sudo apt install git  # Debian/Ubuntu\n  sudo dnf install git  # Fedora",
			"winget install --id Git.Git  # Windows",
		),
	},
	{
		cmd:         "gh",
		name:        "GitHub CLI (gh)",
		description: "GitHub API access — used to list PRs, fetch review state, and trigger actions",
		installDocs: "https://cli.github.com",
		installHint: osInstallHints(
			"brew install gh",
			"sudo apt install gh  # Debian/Ubuntu (via GitHub's apt repo)\n  sudo dnf install gh  # Fedora",
			"winget install --id GitHub.cli  # Windows",
		),
	},
}

// osInstallHints picks the appropriate install hint for the current OS.
// Provide hints in the order: macOS, linux, windows.  Pass "" to skip.
func osInstallHints(mac, linux, windows string) []string {
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

// missingDep holds a dependency that was not found on the system.
type missingDep struct {
	dep dep
	err error
}

// checkDependencies inspects PATH for each required dependency.
// Returns a slice of missing deps (empty = all present).
func checkDependencies() []missingDep {
	var missing []missingDep
	for _, d := range requiredDeps {
		if _, err := exec.LookPath(d.cmd); err != nil {
			missing = append(missing, missingDep{dep: d, err: err})
		}
	}
	return missing
}

// checkGHAuth verifies that `gh auth status` succeeds, i.e. the user is
// logged in to GitHub CLI.  Returns an error when not authenticated.
func checkGHAuth() error {
	cmd := exec.Command("gh", "auth", "status")
	cmd.Stderr = nil // suppress noise
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("not authenticated")
	}
	return nil
}

// PrintDependencyError writes a guided setup error to stderr and exits.
// Call this when checkDependencies() returns missing items.
func PrintDependencyError(missing []missingDep) {
	sep := strings.Repeat("─", 60)

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, sep)
	fmt.Fprintf(os.Stderr, "  rinse — missing required %s\n",
		pluralise(len(missing), "dependency", "dependencies"))
	fmt.Fprintln(os.Stderr, sep)
	fmt.Fprintln(os.Stderr)

	for _, m := range missing {
		fmt.Fprintf(os.Stderr, "  ✗  %s\n", m.dep.name)
		fmt.Fprintf(os.Stderr, "     %s\n", m.dep.description)
		if len(m.dep.installHint) > 0 {
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "     Install:")
			for _, hint := range m.dep.installHint {
				for _, line := range strings.Split(hint, "\n") {
					fmt.Fprintf(os.Stderr, "       $ %s\n", line)
				}
			}
		}
		fmt.Fprintf(os.Stderr, "     Docs:    %s\n", m.dep.installDocs)
		fmt.Fprintln(os.Stderr)
	}

	fmt.Fprintln(os.Stderr, sep)
	fmt.Fprintln(os.Stderr, "  After installing, re-run:  rinse")
	fmt.Fprintln(os.Stderr, sep)
	fmt.Fprintln(os.Stderr)

	os.Exit(1)
}

// PrintGHAuthError writes a guided authentication prompt to stderr and exits.
func PrintGHAuthError() {
	sep := strings.Repeat("─", 60)

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, sep)
	fmt.Fprintln(os.Stderr, "  rinse — GitHub CLI is not authenticated")
	fmt.Fprintln(os.Stderr, sep)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  rinse needs GitHub CLI to be logged in.")
	fmt.Fprintln(os.Stderr, "  Run the following command and follow the prompts:")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "    $ gh auth login")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  Then re-run:  rinse")
	fmt.Fprintln(os.Stderr, sep)
	fmt.Fprintln(os.Stderr)

	os.Exit(1)
}

func pluralise(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
