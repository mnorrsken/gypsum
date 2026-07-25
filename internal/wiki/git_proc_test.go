//go:build !windows

package wiki

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestGitEnvIsNonInteractive asserts that git can never block on a credential
// prompt or an askpass helper — a blocked git process is what leads to the
// timeout kills that used to leak helper processes.
func TestGitEnvIsNonInteractive(t *testing.T) {
	t.Setenv("GIT_ASKPASS", "/usr/bin/some-gui-prompt")
	t.Setenv("SSH_ASKPASS", "/usr/bin/some-gui-prompt")
	t.Setenv("GIT_TERMINAL_PROMPT", "1")
	t.Setenv("GIT_SSH_COMMAND", "")

	env := gitEnv()
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_ASKPASS=") || strings.HasPrefix(kv, "SSH_ASKPASS=") {
			t.Errorf("askpass helper not stripped from git env: %q", kv)
		}
	}
	want := []string{"GIT_TERMINAL_PROMPT=0", "GIT_HTTP_LOW_SPEED_LIMIT=1000"}
	for _, w := range want {
		if !containsEnv(env, w) {
			t.Errorf("git env missing %q", w)
		}
	}
	if !containsPrefix(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes") {
		t.Error("git env should force ssh into batch mode")
	}

	// An explicit GIT_SSH_COMMAND from the operator wins.
	t.Setenv("GIT_SSH_COMMAND", "ssh -i /keys/id_ed25519")
	if !containsEnv(gitEnv(), "GIT_SSH_COMMAND=ssh -i /keys/id_ed25519") {
		t.Error("operator-provided GIT_SSH_COMMAND was overridden")
	}
}

// TestTimedOutGitLeavesNoHelperProcess is the regression test for the leak:
// when a git command is killed by its deadline, the helper processes git
// forked must die with it instead of surviving as orphans that eventually
// exhaust the container's PID limit.
func TestTimedOutGitLeavesNoHelperProcess(t *testing.T) {
	dataDir := t.TempDir()
	pidFile := filepath.Join(t.TempDir(), "helper.pid")

	// Shrink the deadlines so the test does not wait two minutes.
	restore := shrinkGitTimeouts(t, 2*time.Second, 200*time.Millisecond)
	defer restore()

	c := NewGitAutoCommitter(dataDir, nil)

	// git's ext:: transport runs an arbitrary command as the remote helper.
	// This one records its own pid and then hangs, standing in for a wedged
	// git-remote-https or ssh talking to an unreachable remote.
	helperPath := filepath.Join(t.TempDir(), "helper.sh")
	script := "#!/bin/sh\necho $$ > " + pidFile + "\nsleep 60\n"
	if err := os.WriteFile(helperPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing helper: %v", err)
	}
	if err := c.runGit("remote", "add", "stuck", "ext::"+helperPath); err != nil {
		t.Fatalf("adding remote: %v", err)
	}

	start := time.Now()
	// protocol.ext.allow is required because ext:: is disabled by default.
	if err := c.runGit("-c", "protocol.ext.allow=always", "fetch", "stuck"); err == nil {
		t.Fatal("expected the hanging fetch to fail")
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("fetch took %s — the deadline did not cut it short", elapsed)
	}

	pid := readPID(t, pidFile)
	if pid == 0 {
		t.Skip("git did not start the ext:: helper; nothing to assert")
	}
	// Signal 0 only checks for existence. The helper must already be gone:
	// killing git alone would have left it sleeping for another minute.
	if err := syscall.Kill(pid, 0); err == nil {
		_ = syscall.Kill(pid, syscall.SIGKILL) // don't leak it out of the test
		t.Fatalf("helper process %d survived the killed git command", pid)
	}
}

// TestReadOnlyGitIsBounded verifies the concurrency cap on read-only git
// commands: a burst of history requests must not fork one process per request.
func TestReadOnlyGitIsBounded(t *testing.T) {
	if cap(gitReadSem) < 1 {
		t.Fatal("gitReadSem must have capacity")
	}
	dataDir := t.TempDir()
	c := NewGitAutoCommitter(dataDir, nil)

	// Saturate the semaphore, leaving one slot.
	for i := 0; i < cap(gitReadSem)-1; i++ {
		gitReadSem <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(gitReadSem)-1; i++ {
			<-gitReadSem
		}
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.currentBranch() // must still complete through the last free slot
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("read-only git command deadlocked on the semaphore")
	}
}

func shrinkGitTimeouts(t *testing.T, timeout, waitDelay time.Duration) func() {
	t.Helper()
	oldTimeout, oldLocal, oldDelay := gitTimeout, gitLocalTimeout, gitWaitDelay
	gitTimeout, gitLocalTimeout, gitWaitDelay = timeout, timeout, waitDelay
	return func() {
		gitTimeout, gitLocalTimeout, gitWaitDelay = oldTimeout, oldLocal, oldDelay
	}
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0
	}
	return pid
}

func containsEnv(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}

func containsPrefix(env []string, prefix string) bool {
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return true
		}
	}
	return false
}
