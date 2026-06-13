//go:build windows

package wait

import (
	"os/exec"
)

// setProcessGroup is a no-op on Windows. The Unix process-group semantics
// don't translate directly; we accept that on Windows a SIGKILL via
// exec.CommandContext only reaches the immediate child. The visible
// consequence is that a `rinse wait-review` timeout on Windows MAY leave the
// gh-webhook extension binary running for a short period until it notices its
// websocket connection dropped. Production rinse use today is macOS/Linux;
// proper Windows support would use Job Objects.
func setProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup falls back to killing the direct child only on Windows.
func killProcessGroup(p *exec.Cmd) {
	if p == nil || p.Process == nil {
		return
	}
	_ = p.Process.Kill()
}
