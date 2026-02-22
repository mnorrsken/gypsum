package wiki

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type GitAutoCommitter struct {
	dataDir string
}

func NewGitAutoCommitter(dataDir string) *GitAutoCommitter {
	c := &GitAutoCommitter{dataDir: dataDir}
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

	if err := c.runGit("add", "--", relativeFilePath); err != nil {
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
