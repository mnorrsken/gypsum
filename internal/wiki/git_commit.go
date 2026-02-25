package wiki

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type HistoryEntry struct {
	Hash    string
	Author  string
	Date    time.Time
	Message string
}

type GitAutoCommitter struct {
	dataDir string
}

func NewGitAutoCommitter(dataDir string) *GitAutoCommitter {
	c := &GitAutoCommitter{dataDir: dataDir}
	c.markSafeDirectory()
	c.ensureRepo()
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
	if err := c.runGit("rm", "--cached", "--ignore-unmatch", "--", relPath); err != nil {
		return err
	}
	return c.runGit(
		"-c", "user.name=Gypsum",
		"-c", "user.email=gypsum@local",
		"commit", "-m", fmt.Sprintf("wiki: delete image %s", filename),
		"--allow-empty",
	)
}

func (c *GitAutoCommitter) commitFile(relativeFilePath, message string) error {
	if c == nil || c.dataDir == "" || !c.isOwnRepo() {
		return nil
	}

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

	return c.runGit(
		"-c", "user.name=Gypsum",
		"-c", "user.email=gypsum@local",
		"commit", "-m", message,
		"--", relativeFilePath,
	)
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
