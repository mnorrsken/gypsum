//go:build !windows

package wiki

import (
	"os/exec"
	"syscall"
)

// configureProcessGroup puts the command in its own process group so that the
// whole tree (git plus the helpers it forks: git-remote-https, ssh, credential
// helpers) can be signalled as a unit. Without this, killing a hung `git fetch`
// leaves its network helper running and reparented to PID 1 forever.
func configureProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup SIGKILLs every process in the group led by cmd. With
// Setpgid the group id equals the child's pid. A missing group (ESRCH) means
// everything already exited, which is the normal case.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		// Group signalling failed (e.g. Setpgid was not honoured); fall back to
		// the direct child so at least it does not survive.
		_ = cmd.Process.Kill()
	}
}
