package wiki

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type HistoryEntry struct {
	Hash    string
	Author  string
	Date    time.Time
	Message string
}

// GitRemoteConfig holds optional remote sync settings.
type GitRemoteConfig struct {
	RemoteName  string // e.g. "origin"
	RemoteURL   string // full URL (with auth baked in if needed)
	CommitName  string // user.name for commits
	CommitEmail string // user.email for commits
}

type GitAutoCommitter struct {
	dataDir string
	remote  *GitRemoteConfig
	mu      sync.Mutex // serialise git operations
	stopCh  chan struct{}
}

func NewGitAutoCommitter(dataDir string, remote *GitRemoteConfig) *GitAutoCommitter {
	c := &GitAutoCommitter{
		dataDir: dataDir,
		remote:  remote,
		stopCh:  make(chan struct{}),
	}
	c.markSafeDirectory()
	c.ensureRepo()
	c.configureRemote()
	c.initialPull()
	return c
}

func (c *GitAutoCommitter) CommitPageSave(slug string) error {
	return c.commitFile(filepath.Join("pages", MarkdownFilename(slug)), fmt.Sprintf("wiki: update page %s", slug))
}

func (c *GitAutoCommitter) CommitImageSave(filename string) error {
	return c.commitFile(filepath.Join("images", filename), fmt.Sprintf("wiki: upload image %s", filename))
}

func (c *GitAutoCommitter) CommitImageDelete(filename string) error {
	relPath := filepath.Join("images", filename)
	if c == nil || c.dataDir == "" || !c.isOwnRepo() {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.pullRebase() // best-effort pull before commit

	if err := c.runGit("rm", "--cached", "--ignore-unmatch", "--", relPath); err != nil {
		return err
	}
	if err := c.runGit(
		"-c", fmt.Sprintf("user.name=%s", c.commitName()),
		"-c", fmt.Sprintf("user.email=%s", c.commitEmail()),
		"commit", "-m", fmt.Sprintf("wiki: delete image %s", filename),
		"--allow-empty",
	); err != nil {
		return err
	}
	c.pushAsync()
	return nil
}

func (c *GitAutoCommitter) commitFile(relativeFilePath, message string) error {
	if c == nil || c.dataDir == "" || !c.isOwnRepo() {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.pullRebase() // best-effort pull before commit

	if err := c.runGit("add", "-f", "--", relativeFilePath); err != nil {
		return err
	}

	hasChanges, err := c.hasStagedChanges(relativeFilePath)
	if err != nil {
		return err
	}
	if !hasChanges {
		return nil
	}

	if err := c.runGit(
		"-c", fmt.Sprintf("user.name=%s", c.commitName()),
		"-c", fmt.Sprintf("user.email=%s", c.commitEmail()),
		"commit", "-m", message,
		"--", relativeFilePath,
	); err != nil {
		return err
	}
	c.pushAsync()
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
	cmd := exec.Command("git", "init", c.dataDir)
	_ = cmd.Run()
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
// remote at the given interval. Call Stop() to terminate it.
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
				c.mu.Lock()
				c.pullRebase()
				c.mu.Unlock()
			case <-c.stopCh:
				return
			}
		}
	}()
	log.Printf("git: periodic pull every %s", interval)
}

// Stop terminates the periodic pull goroutine.
func (c *GitAutoCommitter) Stop() {
	if c == nil {
		return
	}
	close(c.stopCh)
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
	if err := c.runGit("fetch", remoteName); err != nil {
		log.Printf("git: fetch failed: %v", err)
		return
	}

	// Check if the remote branch exists
	remoteRef := remoteName + "/" + branch
	if err := c.runGit("rev-parse", "--verify", remoteRef); err != nil {
		// Remote branch doesn't exist yet; nothing to pull.
		return
	}

	// Try rebase on top of remote
	err := c.runGit("rebase", remoteRef)
	if err != nil {
		log.Printf("git: rebase failed, aborting and keeping local state: %v", err)
		_ = c.runGit("rebase", "--abort")
		// Force-push to overwrite remote with local (ours wins)
		c.forcePush()
		return
	}
}

// pushAsync pushes to the remote in the background (fire-and-forget).
// Must be called with c.mu held (it spawns a goroutine that acquires its own lock).
func (c *GitAutoCommitter) pushAsync() {
	if !c.hasRemote() {
		return
	}
	go func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.push()
	}()
}

// push pushes the current branch to the remote. Falls back to force-push on rejection.
// Must be called with c.mu held.
func (c *GitAutoCommitter) push() {
	branch := c.currentBranch()
	if branch == "" {
		return
	}
	remoteName := c.remoteName()
	if err := c.runGit("push", remoteName, branch); err != nil {
		log.Printf("git: push rejected, force-pushing: %v", err)
		c.forcePush()
	}
}

// forcePush force-pushes the current branch (ours-wins strategy).
func (c *GitAutoCommitter) forcePush() {
	branch := c.currentBranch()
	if branch == "" {
		return
	}
	if err := c.runGit("push", "--force", c.remoteName(), branch); err != nil {
		log.Printf("git: force-push failed: %v", err)
	}
}

func (c *GitAutoCommitter) hasRemote() bool {
	return c != nil && c.remote != nil && c.remote.RemoteURL != "" && c.isOwnRepo()
}

func (c *GitAutoCommitter) remoteName() string {
	if c.remote != nil && c.remote.RemoteName != "" {
		return c.remote.RemoteName
	}
	return "origin"
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
	cmd := exec.Command("git", "-C", c.dataDir, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
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
	cmd := exec.Command("git", "config", "--global", "--add", "safe.directory", absPath)
	_ = cmd.Run()
}

// isOwnRepo checks whether dataDir itself contains a .git directory
// (as opposed to being inside a parent repo that may gitignore it).
func (c *GitAutoCommitter) isOwnRepo() bool {
	info, err := os.Stat(filepath.Join(c.dataDir, ".git"))
	return err == nil && info.IsDir()
}

func (c *GitAutoCommitter) hasStagedChanges(relativeFilePath string) (bool, error) {
	cmd := exec.Command("git", "-C", c.dataDir, "diff", "--cached", "--quiet", "--", relativeFilePath)
	err := cmd.Run()
	if err == nil {
		return false, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, err
}

func (c *GitAutoCommitter) runGit(args ...string) error {
	fullArgs := make([]string, 0, len(args)+2)
	fullArgs = append(fullArgs, "-C", c.dataDir)
	fullArgs = append(fullArgs, args...)
	cmd := exec.Command("git", fullArgs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v failed: %v (%s)", args, err, string(out))
	}
	return nil
}

// PageHistory returns the git log for a page file.
func (c *GitAutoCommitter) PageHistory(slug string, maxEntries int) ([]HistoryEntry, error) {
	if c == nil || c.dataDir == "" || !c.isOwnRepo() {
		return nil, nil
	}

	relPath := filepath.Join("pages", MarkdownFilename(slug))
	format := "%H%n%an%n%aI%n%s%n---" // hash, author, ISO date, subject, separator

	cmd := exec.Command("git", "-C", c.dataDir,
		"log", fmt.Sprintf("--max-count=%d", maxEntries),
		fmt.Sprintf("--format=%s", format),
		"--", relPath,
	)
	out, err := cmd.Output()
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
