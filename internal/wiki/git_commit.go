package wiki

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// gitTimeout is the maximum time allowed for any single git operation.
// Network operations (fetch, push) can legitimately take a while on slow
// connections, but should never block for hours if the server is unreachable.
// Vars rather than consts so tests can shrink them.
var gitTimeout = 2 * time.Minute

// gitLocalTimeout applies to purely local commands (status, log, show,
// rev-parse). These touch no network, so anything beyond this means the
// process is wedged and must be killed rather than waited on.
var gitLocalTimeout = 30 * time.Second

// gitWaitDelay is how long Go waits, after the git process has exited, for
// inherited stdout/stderr pipes to be closed before force-closing them. A
// lingering grandchild holding the pipe would otherwise block Wait() forever
// and leave the git process unreaped.
var gitWaitDelay = 5 * time.Second

// gitReadSem bounds the number of concurrent read-only git subprocesses.
// Every history, diff and revision request from HTTP or MCP forks a git
// process, and unlike the write path those calls do not hold c.mu — so a burst
// of requests could otherwise fork an unbounded number of processes and
// exhaust the container's PID limit ("git: cannot fork").
var gitReadSem = make(chan struct{}, 4)

type HistoryEntry struct {
	Hash    string
	Author  string
	Date    time.Time
	Message string
}

// GlobalHistoryEntry extends HistoryEntry with the affected page slug and title.
type GlobalHistoryEntry struct {
	HistoryEntry
	Slug  string
	Title string
}

// GitRemoteConfig holds optional remote sync settings.
type GitRemoteConfig struct {
	RemoteName  string        // e.g. "origin"
	RemoteURL   string        // full URL (with auth baked in if needed)
	CommitName  string        // user.name for commits
	CommitEmail string        // user.email for commits
	PushDelay   time.Duration // debounce window; 0 = push immediately
}

// defaultPushDelay is the debounce window applied when PushDelay is unset.
const defaultPushDelay = 30 * time.Second

type GitAutoCommitter struct {
	dataDir   string
	remote    *GitRemoteConfig
	mu        sync.Mutex // serialise git operations
	stopCh    chan struct{}
	pushMu    sync.Mutex    // guards pushTimer
	pushTimer *time.Timer   // active debounce timer (nil if none pending)
	pushDelay time.Duration // copy of remote.PushDelay for fast access
	pushWG    sync.WaitGroup
	afterPull func(kind DocKind, changedSlugs []string) // optional; called after pull per doc kind

	// Sync-health tracking, exposed via SyncStatus for the UI status indicator.
	statusMu    sync.Mutex
	syncing     bool      // a network op (fetch/push) is currently in flight
	lastErr     string    // last network error (empty when healthy)
	lastErrTime time.Time // when lastErr was recorded
	lastSyncOK  time.Time // when the last network op last succeeded
}

// GitSyncStatus is a snapshot of the committer's remote-sync health, returned
// to the UI so it can show a green/red indicator in the top bar.
type GitSyncStatus struct {
	Enabled     bool      `json:"enabled"`               // remote sync is configured
	Syncing     bool      `json:"syncing"`               // a fetch/push is in flight right now
	OK          bool      `json:"ok"`                    // last network op succeeded (true before first attempt)
	Error       string    `json:"error,omitempty"`       // last network error, sanitized
	LastSuccess time.Time `json:"lastSuccess,omitempty"` // last successful sync
	LastError   time.Time `json:"lastError,omitempty"`   // when the last error occurred
}

func NewGitAutoCommitter(dataDir string, remote *GitRemoteConfig) *GitAutoCommitter {
	delay := defaultPushDelay
	if remote != nil {
		delay = remote.PushDelay
	}
	if delay < 0 {
		delay = 0
	}
	c := &GitAutoCommitter{
		dataDir:   dataDir,
		remote:    remote,
		stopCh:    make(chan struct{}),
		pushDelay: delay,
	}
	c.markSafeDirectory()
	c.ensureRepo()
	c.configureRemote()
	c.initialPull()
	return c
}

// SetAfterPull registers a callback invoked after every successful pull/rebase.
// The callback receives the document kind and the slugs that changed; a nil
// slice means the diff could not be determined and a full reindex is recommended.
func (c *GitAutoCommitter) SetAfterPull(fn func(kind DocKind, changedSlugs []string)) {
	c.afterPull = fn
}

func (c *GitAutoCommitter) CommitSave(kind DocKind, slug, author string) error {
	return c.commitFile(filepath.Join(string(kind), MarkdownFilename(slug)), fmt.Sprintf("wiki: update %s %s", kind.Label(), slug), author)
}

func (c *GitAutoCommitter) CommitDelete(kind DocKind, slug, author string) error {
	relPath := filepath.Join(string(kind), MarkdownFilename(slug))
	return c.commitDelete(relPath, fmt.Sprintf("wiki: delete %s %s", kind.Label(), slug), author)
}

// noteRelPath returns the repo-relative path for a note file. Archived notes
// live under notes/archive/; active notes directly under notes/.
func noteRelPath(id string, archived bool) string {
	if archived {
		return filepath.Join("notes", "archive", MarkdownFilename(id))
	}
	return filepath.Join("notes", MarkdownFilename(id))
}

// CommitNoteSave commits a note file at its current location (active or archived).
func (c *GitAutoCommitter) CommitNoteSave(id string, archived bool, author string) error {
	return c.commitFile(noteRelPath(id, archived), fmt.Sprintf("wiki: update note %s", id), author)
}

// CommitNoteDelete commits the deletion of a note at its current location.
func (c *GitAutoCommitter) CommitNoteDelete(id string, archived bool, author string) error {
	return c.commitDelete(noteRelPath(id, archived), fmt.Sprintf("wiki: delete note %s", id), author)
}

// CommitNoteMove records an archive/restore as a single rename commit: the file
// moves between notes/ and notes/archive/.
func (c *GitAutoCommitter) CommitNoteMove(id string, toArchive bool, author string) error {
	if c == nil || c.dataDir == "" || !c.isOwnRepo() {
		return nil
	}
	from := noteRelPath(id, !toArchive)
	to := noteRelPath(id, toArchive)
	verb := "archive"
	if !toArchive {
		verb = "restore"
	}
	c.mu.Lock()
	if err := c.runGit("add", "-f", "--", to); err != nil {
		c.mu.Unlock()
		return err
	}
	if err := c.runGit("rm", "--cached", "--ignore-unmatch", "--", from); err != nil {
		c.mu.Unlock()
		return err
	}
	commitName, commitEmail := c.resolveAuthor(author)
	if err := c.runGit(
		"-c", fmt.Sprintf("user.name=%s", commitName),
		"-c", fmt.Sprintf("user.email=%s", commitEmail),
		"commit", "-m", fmt.Sprintf("wiki: %s note %s", verb, id),
		"--allow-empty",
	); err != nil {
		c.mu.Unlock()
		return err
	}
	c.mu.Unlock()
	c.schedulePush()
	return nil
}

func (c *GitAutoCommitter) CommitImageSave(filename, author string) error {
	return c.commitFile(filepath.Join("images", filename), fmt.Sprintf("wiki: upload image %s", filename), author)
}

func (c *GitAutoCommitter) CommitImageDelete(filename, author string) error {
	relPath := filepath.Join("images", filename)
	return c.commitDelete(relPath, fmt.Sprintf("wiki: delete image %s", filename), author)
}

func (c *GitAutoCommitter) commitDelete(relPath, message, author string) error {
	if c == nil || c.dataDir == "" || !c.isOwnRepo() {
		return nil
	}
	c.mu.Lock()
	if err := c.runGit("rm", "--cached", "--ignore-unmatch", "--", relPath); err != nil {
		c.mu.Unlock()
		return err
	}
	commitName, commitEmail := c.resolveAuthor(author)
	if err := c.runGit(
		"-c", fmt.Sprintf("user.name=%s", commitName),
		"-c", fmt.Sprintf("user.email=%s", commitEmail),
		"commit", "-m", message,
		"--allow-empty",
	); err != nil {
		c.mu.Unlock()
		return err
	}
	c.mu.Unlock()
	c.schedulePush()
	return nil
}

func (c *GitAutoCommitter) commitFile(relativeFilePath, message, author string) error {
	if c == nil || c.dataDir == "" || !c.isOwnRepo() {
		return nil
	}
	c.mu.Lock()
	if err := c.runGit("add", "-f", "--", relativeFilePath); err != nil {
		c.mu.Unlock()
		return err
	}

	hasChanges, err := c.hasStagedChanges(relativeFilePath)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	if !hasChanges {
		c.mu.Unlock()
		return nil
	}

	commitName, commitEmail := c.resolveAuthor(author)
	if err := c.runGit(
		"-c", fmt.Sprintf("user.name=%s", commitName),
		"-c", fmt.Sprintf("user.email=%s", commitEmail),
		"commit", "-m", message,
		"--", relativeFilePath,
	); err != nil {
		c.mu.Unlock()
		return err
	}
	c.mu.Unlock()
	c.schedulePush()
	return nil
}

// ensureRepo initializes a git repo inside dataDir if one doesn't exist there.
func (c *GitAutoCommitter) ensureRepo() {
	if c == nil || c.dataDir == "" {
		return
	}
	if c.isOwnRepo() {
		return
	}
	// Initialize a new repo inside dataDir
	_ = runGitBare("init", c.dataDir)
}

// runGitBare runs a git command that is not scoped to the repo (init, global
// config). It gets the same process-group, timeout and environment treatment
// as every other git invocation.
func runGitBare(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitLocalTimeout)
	defer cancel()
	return runGitProcess(ctx, newGitCmd(ctx, args...))
}

// configureRemote sets (or resets) the git remote URL on every startup so that
// credential or URL changes in environment variables take effect immediately.
func (c *GitAutoCommitter) configureRemote() {
	if c == nil || c.remote == nil || c.remote.RemoteURL == "" || !c.isOwnRepo() {
		return
	}
	name := c.remote.RemoteName
	if name == "" {
		name = "origin"
	}
	// Try set-url first; if the remote doesn't exist yet, add it.
	if err := c.runGit("remote", "set-url", name, c.remote.RemoteURL); err != nil {
		_ = c.runGit("remote", "add", name, c.remote.RemoteURL)
	}
	log.Printf("git: remote %q configured → %s", name, sanitizeURL(c.remote.RemoteURL))
}

// initialPull does a one-time pull at startup, handling the case where the
// local repo may be empty or diverged. Uses the "ours wins" strategy.
func (c *GitAutoCommitter) initialPull() {
	if !c.hasRemote() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pullRebase()
}

// StartPeriodicPull launches a background goroutine that pulls from the
// remote at the given interval. Call Stop() to terminate it. If a debounced
// push is pending when the ticker fires, it is folded into the same locked
// pull+push cycle so the two never duplicate git work back-to-back.
func (c *GitAutoCommitter) StartPeriodicPull(interval time.Duration) {
	if !c.hasRemote() {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.periodicSync()
			case <-c.stopCh:
				return
			}
		}
	}()
	log.Printf("git: periodic pull every %s", interval)
}

// periodicSync performs one periodic pull. If a debounced push is pending it
// is absorbed into this cycle (single pullRebase + push) so that the push
// callback never fires redundantly right after the periodic tick.
func (c *GitAutoCommitter) periodicSync() {
	foldPush := c.consumePendingPush()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pullRebase()
	if foldPush {
		c.push()
	}
}

// consumePendingPush cancels the debounce timer if one is armed and returns
// true when the caller should perform the push itself (the WaitGroup ticket
// that schedulePush reserved is released here). Returns false when no timer
// is pending, or when the timer already fired — in that case the existing
// callback will serialise on c.mu naturally, so there is nothing to fold.
func (c *GitAutoCommitter) consumePendingPush() bool {
	c.pushMu.Lock()
	defer c.pushMu.Unlock()
	if c.pushTimer == nil {
		return false
	}
	if !c.pushTimer.Stop() {
		// Timer already fired; its callback is running (or about to) and
		// will acquire c.mu after us. Leave pushTimer so schedulePush can
		// still observe it.
		return false
	}
	c.pushTimer = nil
	c.pushWG.Done()
	return true
}

// Stop terminates the periodic pull goroutine and flushes any pending push
// synchronously so that buffered commits are not lost on shutdown.
func (c *GitAutoCommitter) Stop() {
	if c == nil {
		return
	}
	close(c.stopCh)

	c.pushMu.Lock()
	t := c.pushTimer
	c.pushTimer = nil
	c.pushMu.Unlock()

	if t != nil && t.Stop() {
		// Cancelled a pending timer before it fired; flush its push now and
		// release the WaitGroup ticket that schedulePush reserved for it.
		c.doPush()
		c.pushWG.Done()
	}
	// Wait for any in-flight push callback to finish.
	c.pushWG.Wait()
}

// pullRebase fetches from the remote and rebases local commits on top.
// If rebase conflicts occur, abort and force-push local state (ours wins).
// Must be called with c.mu held.
func (c *GitAutoCommitter) pullRebase() {
	if !c.hasRemote() {
		return
	}
	remoteName := c.remoteName()
	branch := c.currentBranch()
	if branch == "" {
		return // detached HEAD or empty repo with no commits yet
	}

	// Fetch latest
	c.setSyncing(true)
	err := c.runGit("fetch", remoteName)
	c.setSyncing(false)
	if err != nil {
		log.Printf("git: fetch failed: %v", err)
		c.recordSyncErr("fetch", err)
		return
	}
	c.recordSyncOK()

	// Check if the remote branch exists
	remoteRef := remoteName + "/" + branch
	if err := c.runGit("rev-parse", "--verify", remoteRef); err != nil {
		// Remote branch doesn't exist yet; nothing to pull.
		return
	}

	// Record HEAD before rebase so we can diff afterwards.
	headBefore := c.revParseHEAD()

	// Stash uncommitted changes so rebase can proceed.
	stashed := false
	if c.hasUncommittedChanges() {
		if err := c.runGit("stash", "--include-untracked"); err == nil {
			stashed = true
		}
	}

	// Try rebase on top of remote
	err = c.runGit("rebase", remoteRef)
	if err != nil {
		log.Printf("git: rebase failed, aborting and keeping local state: %v", err)
		_ = c.runGit("rebase", "--abort")
		// Force-push to overwrite remote with local (ours wins)
		c.forcePush()
	}

	// Restore stashed changes.
	if stashed {
		if err := c.runGit("stash", "pop"); err != nil {
			log.Printf("git: stash pop failed: %v", err)
		}
	}

	if c.afterPull != nil {
		c.afterPull(KindPage, c.changedDocSlugs("pages", headBefore))
		c.afterPull(KindSkill, c.changedDocSlugs("skills", headBefore))
		// Notes may have moved between notes/ and notes/archive/; a nil slice
		// signals ReindexChanged to do a full note reindex.
		c.afterPull(KindNote, nil)
	}
}

// schedulePush debounces remote pushes so that a burst of edits results in a
// single pull+push after the burst settles. The first edit in a burst arms a
// timer; each subsequent edit cancels and re-arms it. When pushDelay is 0 the
// push runs immediately on a new goroutine. Must be called with c.mu released.
func (c *GitAutoCommitter) schedulePush() {
	if !c.hasRemote() {
		return
	}
	c.pushMu.Lock()
	defer c.pushMu.Unlock()

	// Cancel any pending timer. If Stop returns true the previous
	// WaitGroup ticket is still outstanding and can be reused for the
	// fresh timer; otherwise the old callback already (or will shortly)
	// call Done, so we must add a new ticket.
	reuseTicket := false
	if c.pushTimer != nil && c.pushTimer.Stop() {
		reuseTicket = true
	}
	if !reuseTicket {
		c.pushWG.Add(1)
	}
	c.pushTimer = time.AfterFunc(c.pushDelay, c.firePushCallback)
}

// firePushCallback is the AfterFunc callback; it is scheduled on its own
// goroutine by the runtime, so doPush's git work does not block the timer.
func (c *GitAutoCommitter) firePushCallback() {
	defer c.pushWG.Done()
	c.doPush()
}

// doPush performs the actual pull-rebase + push under the main git lock.
func (c *GitAutoCommitter) doPush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pullRebase()
	c.push()
}

// push pushes the current branch to the remote. Falls back to force-push on rejection.
// Must be called with c.mu held.
func (c *GitAutoCommitter) push() {
	branch := c.currentBranch()
	if branch == "" {
		return
	}
	remoteName := c.remoteName()
	c.setSyncing(true)
	err := c.runGit("push", remoteName, branch)
	c.setSyncing(false)
	if err != nil {
		// A rejection here (non-fast-forward) is expected under the ours-wins
		// strategy and does not by itself mean the remote is unreachable; let
		// forcePush record the final health of the sync.
		log.Printf("git: push rejected, force-pushing: %v", err)
		c.forcePush()
		return
	}
	c.recordSyncOK()
}

// forcePush force-pushes the current branch (ours-wins strategy).
func (c *GitAutoCommitter) forcePush() {
	branch := c.currentBranch()
	if branch == "" {
		return
	}
	c.setSyncing(true)
	err := c.runGit("push", "--force", c.remoteName(), branch)
	c.setSyncing(false)
	if err != nil {
		log.Printf("git: force-push failed: %v", err)
		c.recordSyncErr("push", err)
		return
	}
	c.recordSyncOK()
}

func (c *GitAutoCommitter) hasRemote() bool {
	return c != nil && c.remote != nil && c.remote.RemoteURL != "" && c.isOwnRepo()
}

// SyncStatus returns a snapshot of remote-sync health for the UI indicator.
// When no remote is configured it reports Enabled=false so the indicator hides.
func (c *GitAutoCommitter) SyncStatus() GitSyncStatus {
	if !c.hasRemote() {
		return GitSyncStatus{}
	}
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	return GitSyncStatus{
		Enabled:     true,
		Syncing:     c.syncing,
		OK:          c.lastErr == "",
		Error:       c.lastErr,
		LastSuccess: c.lastSyncOK,
		LastError:   c.lastErrTime,
	}
}

func (c *GitAutoCommitter) setSyncing(v bool) {
	c.statusMu.Lock()
	c.syncing = v
	c.statusMu.Unlock()
}

// recordSyncOK marks the last network op as successful and clears any error.
func (c *GitAutoCommitter) recordSyncOK() {
	c.statusMu.Lock()
	c.lastErr = ""
	c.lastSyncOK = time.Now()
	c.statusMu.Unlock()
}

// recordSyncErr records a failed network op. The message is sanitized so that
// any credentials baked into the remote URL are never surfaced to the UI.
func (c *GitAutoCommitter) recordSyncErr(op string, err error) {
	c.statusMu.Lock()
	c.lastErr = sanitizeGitError(fmt.Sprintf("%s failed: %v", op, err))
	c.lastErrTime = time.Now()
	c.statusMu.Unlock()
}

// credInURLRe matches the "user:pass@" (or "token@") credential portion of a
// URL so it can be masked before an error string reaches the UI or logs.
var credInURLRe = regexp.MustCompile(`://[^/@\s]+@`)

func sanitizeGitError(s string) string {
	s = credInURLRe.ReplaceAllString(s, "://***@")
	return strings.TrimSpace(s)
}

func (c *GitAutoCommitter) remoteName() string {
	if c.remote != nil && c.remote.RemoteName != "" {
		return c.remote.RemoteName
	}
	return "origin"
}

// resolveAuthor returns the commit name and email. If author is non-empty it
// is used as the commit name; otherwise the configured default is used.
func (c *GitAutoCommitter) resolveAuthor(author string) (string, string) {
	if author != "" {
		return author, c.commitEmail()
	}
	return c.commitName(), c.commitEmail()
}

func (c *GitAutoCommitter) commitName() string {
	if c.remote != nil && c.remote.CommitName != "" {
		return c.remote.CommitName
	}
	return "Gypsum"
}

func (c *GitAutoCommitter) commitEmail() string {
	if c.remote != nil && c.remote.CommitEmail != "" {
		return c.remote.CommitEmail
	}
	return "gypsum@local"
}

func (c *GitAutoCommitter) currentBranch() string {
	out, err := c.runGitLocal("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// sanitizeURL masks credentials in a URL for safe logging.
func sanitizeURL(u string) string {
	if i := strings.Index(u, "@"); i > 0 {
		scheme := "https://"
		if strings.HasPrefix(u, "http://") {
			scheme = "http://"
		}
		return scheme + "***@" + u[i+1:]
	}
	return u
}

// markSafeDirectory adds dataDir to git's global safe.directory list so that
// git operations succeed even when the directory is owned by a different user
// (common with Kubernetes PVC mounts).
func (c *GitAutoCommitter) markSafeDirectory() {
	if c == nil || c.dataDir == "" {
		return
	}
	absPath, err := filepath.Abs(c.dataDir)
	if err != nil {
		return
	}
	_ = runGitBare("config", "--global", "--add", "safe.directory", absPath)
}

// isOwnRepo checks whether dataDir itself contains a .git directory
// (as opposed to being inside a parent repo that may gitignore it).
func (c *GitAutoCommitter) isOwnRepo() bool {
	info, err := os.Stat(filepath.Join(c.dataDir, ".git"))
	return err == nil && info.IsDir()
}

// hasUncommittedChanges returns true if the working tree has any staged,
// unstaged, or untracked changes.
func (c *GitAutoCommitter) hasUncommittedChanges() bool {
	out, err := c.runGitLocal("status", "--porcelain")
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}

func (c *GitAutoCommitter) hasStagedChanges(relativeFilePath string) (bool, error) {
	_, err := c.runGitLocal("diff", "--cached", "--quiet", "--", relativeFilePath)
	if err == nil {
		return false, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, err
}

func (c *GitAutoCommitter) runGit(args ...string) error {
	err := c.runGitOnce(args...)
	if err != nil && c.clearStaleLock(err) {
		// A previous git process was killed (timeout, OOM, SIGTERM) mid-write
		// and left .git/index.lock behind. We are the sole owner of this repo
		// and all index-mutating ops are serialised on c.mu, so the lock is
		// always stale: remove it and retry once.
		err = c.runGitOnce(args...)
	}
	return err
}

func (c *GitAutoCommitter) runGitOnce(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := newGitCmd(ctx, c.repoArgs(args)...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := runGitProcess(ctx, cmd); err != nil {
		return fmt.Errorf("git %v failed: %v (%s)", args, err, buf.String())
	}
	return nil
}

// repoArgs prefixes args with "-C <dataDir>" so every command runs against the
// wiki repo regardless of the server's working directory.
func (c *GitAutoCommitter) repoArgs(args []string) []string {
	full := make([]string, 0, len(args)+2)
	full = append(full, "-C", c.dataDir)
	return append(full, args...)
}

// newGitCmd builds a git command that cannot outlive its context: it runs in
// its own process group, cancellation kills that entire group (not just git
// itself), and WaitDelay guarantees Wait() returns even if a helper process
// still holds the output pipes.
func newGitCmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = gitEnv()
	configureProcessGroup(cmd)
	cmd.Cancel = func() error {
		killProcessGroup(cmd)
		return nil
	}
	cmd.WaitDelay = gitWaitDelay
	return cmd
}

// runGitProcess runs cmd to completion and then kills anything left in its
// process group. git exits as soon as its own work is done, but helpers like
// git-remote-https or ssh can outlive it; once git is gone they are reparented
// to PID 1 and — since the server is PID 1 in the container image — would never
// be cleaned up. Sweeping the group here keeps that from accumulating.
func runGitProcess(ctx context.Context, cmd *exec.Cmd) error {
	err := cmd.Run()
	killProcessGroup(cmd)
	if err != nil && ctx.Err() != nil {
		// The process group was killed by the deadline; report that rather
		// than the "signal: killed" this surfaces as.
		return fmt.Errorf("%w (git killed after deadline)", ctx.Err())
	}
	return err
}

// gitEnv returns the environment for git subprocesses. Anything that could
// make git block waiting for a human (credential prompts, askpass helpers,
// interactive host-key confirmation) is disabled, and stalled HTTP transfers
// are aborted, so git fails fast instead of sitting in the process table until
// the timeout kills it.
func gitEnv() []string {
	base := os.Environ()
	env := make([]string, 0, len(base)+6)
	for _, kv := range base {
		switch {
		case strings.HasPrefix(kv, "GIT_ASKPASS="),
			strings.HasPrefix(kv, "SSH_ASKPASS="),
			strings.HasPrefix(kv, "GIT_TERMINAL_PROMPT="):
			continue // replaced below
		}
		env = append(env, kv)
	}
	env = append(env,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_PAGER=cat",
		"GIT_HTTP_LOW_SPEED_LIMIT=1000", // abort transfers stuck under 1 KB/s...
		"GIT_HTTP_LOW_SPEED_TIME=30",    // ...for 30 seconds
	)
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		env = append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ConnectTimeout=10")
	}
	return env
}

// clearStaleLock removes a stale .git/index.lock when err indicates a git
// command failed because the lock already existed. Returns true only if a lock
// file was actually removed (so the caller may retry). Safe because this
// committer is the sole git user of the repo and index-mutating ops hold c.mu.
func (c *GitAutoCommitter) clearStaleLock(err error) bool {
	if err == nil || !strings.Contains(err.Error(), "index.lock") {
		return false
	}
	lock := filepath.Join(c.dataDir, ".git", "index.lock")
	if rmErr := os.Remove(lock); rmErr != nil {
		if !os.IsNotExist(rmErr) {
			log.Printf("git: failed to remove stale lock %s: %v", lock, rmErr)
		}
		return false
	}
	log.Printf("git: removed stale lock %s left by a killed git process", lock)
	return true
}

func (c *GitAutoCommitter) runGitOutput(args ...string) (string, error) {
	out, err := c.runGitLocal(args...)
	return strings.TrimSpace(string(out)), err
}

// runGitLocal runs a read-only git command against the repo and returns its
// stdout. Concurrency is capped by gitReadSem and the shorter local timeout
// applies, so request bursts on the history/diff endpoints can never pile up
// unbounded git processes. The returned error is the raw one from exec, so
// callers can still inspect *exec.ExitError exit codes.
func (c *GitAutoCommitter) runGitLocal(args ...string) ([]byte, error) {
	gitReadSem <- struct{}{}
	defer func() { <-gitReadSem }()

	ctx, cancel := context.WithTimeout(context.Background(), gitLocalTimeout)
	defer cancel()
	cmd := newGitCmd(ctx, c.repoArgs(args)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := runGitProcess(ctx, cmd)
	return out.Bytes(), err
}

// revParseHEAD returns the current HEAD commit hash, or "" on error.
func (c *GitAutoCommitter) revParseHEAD() string {
	h, _ := c.runGitOutput("rev-parse", "HEAD")
	return h
}

// changedDocSlugs diffs HEAD against oldHead and returns slugs for any
// changed/added/deleted files under the given subdirectory (e.g. "pages" or
// "skills"). Returns nil if the diff cannot be determined (signals the caller
// to do a full reindex).
func (c *GitAutoCommitter) changedDocSlugs(subdir, oldHead string) []string {
	if oldHead == "" {
		return nil
	}
	newHead := c.revParseHEAD()
	if newHead == "" || newHead == oldHead {
		return []string{} // no change
	}
	out, err := c.runGitOutput("diff", "--name-only", oldHead, newHead, "--", subdir+"/")
	if err != nil || out == "" {
		return nil // can't determine — caller should full-reindex
	}
	var slugs []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		base := filepath.Base(line)
		if filepath.Ext(base) == ".md" {
			slugs = append(slugs, SlugFromFilename(base))
		}
	}
	return slugs
}

// DocHistory returns the git log for a document file (page or skill).
func (c *GitAutoCommitter) DocHistory(kind DocKind, slug string, maxEntries int) ([]HistoryEntry, error) {
	if c == nil || c.dataDir == "" || !c.isOwnRepo() {
		return nil, nil
	}

	relPath := filepath.Join(string(kind), MarkdownFilename(slug))
	format := "%H%n%an%n%aI%n%s%n---" // hash, author, ISO date, subject, separator

	out, err := c.runGitLocal(
		"log", fmt.Sprintf("--max-count=%d", maxEntries),
		fmt.Sprintf("--format=%s", format),
		"--", relPath,
	)
	if err != nil {
		return nil, nil // no history or file not tracked
	}

	var entries []HistoryEntry
	blocks := strings.Split(strings.TrimSpace(string(out)), "---")
	for _, block := range blocks {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if len(lines) < 4 {
			continue
		}
		t, _ := time.Parse(time.RFC3339, strings.TrimSpace(lines[2]))
		entries = append(entries, HistoryEntry{
			Hash:    strings.TrimSpace(lines[0]),
			Author:  strings.TrimSpace(lines[1]),
			Date:    t,
			Message: strings.TrimSpace(lines[3]),
		})
	}
	return entries, nil
}

// DocContentAtRevision returns the content of a document file at a specific git revision.
func (c *GitAutoCommitter) DocContentAtRevision(kind DocKind, slug, hash string) (string, error) {
	if c == nil || c.dataDir == "" || !c.isOwnRepo() {
		return "", fmt.Errorf("git not available")
	}

	relPath := filepath.Join(string(kind), MarkdownFilename(slug))
	out, err := c.runGitLocal("show", hash+":"+relPath)
	if err != nil {
		return "", fmt.Errorf("revision not found")
	}
	return string(out), nil
}

// GlobalHistory returns the most recent commits across all pages, with pagination.
// skip is the number of entries to skip (for paging), limit is the page size.
func (c *GitAutoCommitter) GlobalHistory(skip, limit int) ([]GlobalHistoryEntry, error) {
	if c == nil || c.dataDir == "" || !c.isOwnRepo() {
		return nil, nil
	}

	// Use a unique separator so --name-only filenames don't collide with
	// the record boundaries (a plain "---" would be interleaved with the
	// filename lines that git appends after each formatted record).
	const sep = "===COMMIT==="
	format := sep + "%n%H%n%an%n%aI%n%s"
	out, err := c.runGitLocal(
		"log",
		fmt.Sprintf("--skip=%d", skip),
		fmt.Sprintf("--max-count=%d", limit),
		fmt.Sprintf("--format=%s", format),
		"--name-only",
		"--", "pages/",
	)
	if err != nil {
		return nil, nil
	}

	var entries []GlobalHistoryEntry
	blocks := strings.Split(string(out), sep)
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		lines := strings.Split(block, "\n")
		if len(lines) < 4 {
			continue
		}
		t, _ := time.Parse(time.RFC3339, strings.TrimSpace(lines[2]))

		// Lines after the 4th are filenames (from --name-only).
		slug := ""
		for _, fn := range lines[4:] {
			fn = strings.TrimSpace(fn)
			if strings.HasPrefix(fn, "pages/") && strings.HasSuffix(fn, ".md") {
				slug = strings.TrimSuffix(strings.TrimPrefix(fn, "pages/"), ".md")
				break
			}
		}
		if slug == "" {
			continue
		}

		entries = append(entries, GlobalHistoryEntry{
			HistoryEntry: HistoryEntry{
				Hash:    strings.TrimSpace(lines[0]),
				Author:  strings.TrimSpace(lines[1]),
				Date:    t,
				Message: strings.TrimSpace(lines[3]),
			},
			Slug:  slug,
			Title: TitleFromSlug(slug),
		})
	}

	return entries, nil
}
