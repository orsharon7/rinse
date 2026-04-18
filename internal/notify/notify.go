// Package notify provides best-effort desktop notifications for RINSE cycle events.
//
// Notifications are always opt-in via the --notify flag. Errors are never
// surfaced to the user — a failed notification must never interrupt the CLI.
// In CI / headless environments (no DISPLAY on Linux, TERM=dumb) notifications
// are skipped silently.
package notify

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Notify sends a native desktop notification with the given title and body.
// Errors are silenced — notifications are always best-effort.
func Notify(title, body string) error {
	if isHeadless() {
		return nil
	}
	switch runtime.GOOS {
	case "darwin":
		return notifyDarwin(title, body)
	case "linux":
		return notifyLinux(title, body)
	default:
		// Windows and other platforms: out of scope for v0.3.
		return nil
	}
}

// isHeadless returns true when the environment looks like CI or a headless
// server where desktop notifications cannot be displayed.
func isHeadless() bool {
	if strings.ToLower(os.Getenv("TERM")) == "dumb" {
		return true
	}
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return true
	}
	return false
}

// notifyDarwin uses osascript — always available on macOS, no external deps.
func notifyDarwin(title, body string) error {
	script := fmt.Sprintf(`display notification %q with title %q`, body, title)
	return exec.Command("osascript", "-e", script).Run()
}

// notifyLinux uses notify-send (libnotify). Silently skips when not installed.
func notifyLinux(title, body string) error {
	if _, err := exec.LookPath("notify-send"); err != nil {
		return nil // not installed — skip silently
	}
	return exec.Command("notify-send", "--expire-time=8000", title, body).Run()
}

// CycleResult describes why a RINSE cycle ended.
type CycleResult int

const (
	// ResultApproved means Copilot approved the PR.
	ResultApproved CycleResult = iota
	// ResultMaxIterations means the cycle hit the iteration cap with comments remaining.
	ResultMaxIterations
	// ResultError means the runner exited with a non-zero code.
	ResultError
)

// CycleParams bundles the data needed to compose a cycle-end notification.
type CycleParams struct {
	PR            string      // PR number string, e.g. "42"
	Repo          string      // repo slug, e.g. "owner/repo"
	Result        CycleResult
	Iterations    int
	CommentsFixed int
	CommentsLeft  int
	Elapsed       time.Duration
}

// CycleNotification sends a native desktop notification for a completed cycle.
// It is a no-op when notify is false. Errors are silenced.
func CycleNotification(notify bool, p CycleParams) {
	if !notify {
		return
	}

	title, body := composeCycleMessage(p)
	//nolint:errcheck // best-effort
	_ = Notify(title, body)
}

// composeCycleMessage returns the title and body for a cycle-end notification.
func composeCycleMessage(p CycleParams) (title, body string) {
	prLabel := fmt.Sprintf("PR #%s (%s)", p.PR, p.Repo)
	dur := fmtDuration(p.Elapsed)

	switch p.Result {
	case ResultApproved:
		title = "RINSE: PR #" + p.PR + " approved \u2713"
		body = fmt.Sprintf("Fixed %d comments in %d iterations. (%s)",
			p.CommentsFixed, p.Iterations, dur)
	case ResultMaxIterations:
		title = "RINSE: Cycle complete \u2014 review needed"
		body = fmt.Sprintf("%s: %d comments remain after %d iterations.",
			prLabel, p.CommentsLeft, p.Iterations)
	default: // ResultError
		title = "RINSE: Cycle failed"
		body = prLabel + " exited with an error. Check your terminal."
	}
	return
}

// fmtDuration formats a duration as human-readable text, e.g. "2m 34s".
// Output is plain text (no ANSI) as required for notification bodies.
func fmtDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
