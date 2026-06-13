//go:build !windows

package wait

import (
	"os/exec"
	"syscall"
)

// setProcessGroup configures cmd so that the spawned process becomes the
// leader of a new process group. This lets killProcessGroup signal the entire
// tree — gh -> gh-webhook (extension binary) -> any helpers — in one shot.
//
// Without this, exec.CommandContext's auto-kill only sends SIGKILL to the
// immediate child (gh), leaving the extension binary orphaned with PPID=1.
// Observed in the wild: a `rinse wait-review` timeout left `gh webhook
// forward` running for 21 minutes after the parent exited.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup signals the process group led by p with SIGKILL. Uses the
// negative-PID convention of kill(2) to broadcast to every process whose PGID
// matches the leader's PID. No-op when p is nil or already exited.
func killProcessGroup(p *exec.Cmd) {
	if p == nil || p.Process == nil {
		return
	}
	pgid, err := syscall.Getpgid(p.Process.Pid)
	if err != nil {
		_ = p.Process.Kill()
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}
