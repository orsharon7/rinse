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

	"github.com/charmbracelet/lipgloss"
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

var (
	depErrStyle  = lipgloss.NewStyle().Foreground(theme.Red).Bold(true)
	depNameStyle = lipgloss.NewStyle().Foreground(theme.Red).Bold(true)
	depDescStyle = theme.StyleMuted
	hintStyle    = lipgloss.NewStyle().Foreground(theme.Teal)
	docsStyle    = theme.StyleMuted
	sepStyle     = theme.StyleMuted
	labelStyle   = lipgloss.NewStyle().Foreground(theme.Overlay)
	tipStyle     = lipgloss.NewStyle().Foreground(theme.Subtext)
)

func sep() string {
	return sepStyle.Render(strings.Repeat("─", 60))
}

func printDepError(missing []missingDep) {
	noun := "dependency"
	if len(missing) != 1 {
		noun = "dependencies"
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, sep())
	fmt.Fprintln(os.Stderr, depErrStyle.Render(fmt.Sprintf("  rinse — missing required %s", noun)))
	fmt.Fprintln(os.Stderr, sep())

	for _, m := range missing {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "  "+theme.IconCross+"  "+depNameStyle.Render(m.spec.name))
		fmt.Fprintln(os.Stderr, "     "+depDescStyle.Render(m.spec.description))

		if len(m.spec.installHint) > 0 {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "     "+labelStyle.Render("Install:"))
			for _, hint := range m.spec.installHint {
				for _, line := range strings.Split(hint, "\n") {
					fmt.Fprintln(os.Stderr, "       "+hintStyle.Render("$ "+strings.TrimSpace(line)))
				}
			}
		}
		fmt.Fprintln(os.Stderr, "     "+labelStyle.Render("Docs:    ")+docsStyle.Render(m.spec.installDocs))
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, sep())
	fmt.Fprintln(os.Stderr, tipStyle.Render("  After installing, re-run:  rinse"))
	fmt.Fprintln(os.Stderr, sep())
	fmt.Fprintln(os.Stderr)
}

// ── Staged-changes guard ──────────────────────────────────────────────────────

// hasPRFlag reports whether args contains a --pr flag. When --pr is present the
// review targets a pull-request diff, not the local staging area, so the
// staged-changes guard must be skipped.
func hasPRFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--pr" {
			return true
		}
	}
	return false
}

// CheckStagedChanges guards the interactive TUI entry point. When nothing is
// staged and no --pr flag is present, it prints an actionable hint and exits 0
// (nothing to do — not an error).
//
// Skip conditions (fall through silently):
//   - --pr is present in os.Args (diff comes from the PR, not the staging area)
//   - git diff --cached fails (not a git repo — the runner will surface a cleaner error)
func CheckStagedChanges() {
	// When --pr is supplied the review targets a PR diff, not staged files.
	if hasPRFlag(os.Args[1:]) {
		return
	}

	out, err := exec.Command("git", "diff", "--cached", "--name-only").Output()
	if err != nil {
		// Not a git repo or git unavailable — fall through; the TUI / runner
		// will surface a cleaner error.
		return
	}
	if strings.TrimSpace(string(out)) != "" {
		// Files are staged — proceed normally.
		return
	}

	// Nothing staged: print the actionable empty-state hint and exit 0.
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	tealStyle := lipgloss.NewStyle().Foreground(theme.Teal)

	fmt.Println()
	fmt.Println("  " + mutedStyle.Render(theme.IconDiamond+"  Nothing staged."))
	fmt.Println("     " + mutedStyle.Render("Stage your changes first:"))
	fmt.Println("     " + tealStyle.Render("git add <files>") + mutedStyle.Render("  or  ") + tealStyle.Render("git add -p"))
	fmt.Println()
	os.Exit(0)
}


func printAuthError() {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, sep())
	fmt.Fprintln(os.Stderr, depErrStyle.Render("  rinse — GitHub CLI is not authenticated"))
	fmt.Fprintln(os.Stderr, sep())
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  "+depDescStyle.Render("rinse needs GitHub CLI to be logged in."))
	fmt.Fprintln(os.Stderr, "  "+depDescStyle.Render("Run the following command and follow the prompts:"))
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "    "+hintStyle.Render("$ gh auth login"))
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, sep())
	fmt.Fprintln(os.Stderr, tipStyle.Render("  Then re-run:  rinse"))
	fmt.Fprintln(os.Stderr, sep())
	fmt.Fprintln(os.Stderr)
}
