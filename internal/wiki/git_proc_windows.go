//go:build windows

package wiki

import "os/exec"

// configureProcessGroup is a no-op on Windows; job objects would be needed to
// get the equivalent of a POSIX process group and the server is not deployed
// there (the Windows build exists for local CLI use only).
func configureProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup kills the git process itself. Helper processes it spawned
// are not tracked on Windows.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
