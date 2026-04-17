package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"time"
)

// SendNotification sends a native desktop notification with the given title and body.
// Errors are silenced — notifications are always best-effort and must never crash
// the application or affect CI/headless environments.
func SendNotification(title, body string) {
	switch runtime.GOOS {
	case "darwin":
		// macOS: use osascript (always available, no extra deps)
		script := fmt.Sprintf(
			`display notification %q with title %q`,
			body, title,
		)
		_ = exec.Command("osascript", "-e", script).Run()
	case "linux":
		// Linux: use notify-send (libnotify). Best-effort — may not be installed.
		_ = exec.Command("notify-send", "--expire-time=8000", title, body).Run()
	case "windows":
		// Windows: PowerShell toast via BurntToast (if available), falling back to
		// a simple balloon tooltip via Shell.Application COM.
		psScript := fmt.Sprintf(`
$ErrorActionPreference = 'SilentlyContinue'
[Windows.UI.Notifications.ToastNotificationManager,Windows.UI.Notifications,ContentType=WindowsRuntime] | Out-Null
$template = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent(
    [Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$template.SelectSingleNode('//text[@id=1]').InnerText = %q
$template.SelectSingleNode('//text[@id=2]').InnerText = %q
$toast = [Windows.UI.Notifications.ToastNotification]::new($template)
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('RINSE').Show($toast)
`, title, body)
		_ = exec.Command("powershell", "-NoProfile", "-NonInteractive",
			"-ExecutionPolicy", "Bypass", "-Command", psScript).Run()
	}
}

// CycleNotification sends a desktop notification reflecting the outcome of a
// completed RINSE cycle. It is a no-op when notify is false.
//
// Parameters:
//   - notify:   whether notifications are enabled (--notify flag / config key)
//   - pr:       PR number string, e.g. "42"
//   - repo:     repo slug, e.g. "owner/repo"
//   - exitCode: runner exit code (0 = success, non-zero = failure)
//   - elapsed:  wall-clock duration of the cycle
func CycleNotification(notify bool, pr, repo string, exitCode int, elapsed time.Duration) {
	if !notify {
		return
	}

	cycleName := fmt.Sprintf("PR #%s (%s)", pr, repo)
	dur := fmtDuration(elapsed)

	if exitCode == 0 {
		title := "RINSE: Cycle Complete \u2713"
		body := fmt.Sprintf("Your %s cycle finished in %s. Tap to view results.", cycleName, dur)
		SendNotification(title, body)
	} else {
		title := "RINSE: Cycle Failed \u2717"
		body := fmt.Sprintf("%s encountered an error. Tap to view details.", cycleName)
		SendNotification(title, body)
	}
}

// fmtDuration formats a duration as a human-readable string, e.g. "2m 34s".
// Output is plain text (no ANSI codes) as required for notification bodies.
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
