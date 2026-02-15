package wiki

import (
	"fmt"
	"os/exec"
	"path/filepath"
)

type GitAutoCommitter struct {
	dataDir string
}

func NewGitAutoCommitter(dataDir string) *GitAutoCommitter {
	return &GitAutoCommitter{dataDir: dataDir}
}

func (c *GitAutoCommitter) CommitPageSave(slug string) error {
	return c.commitFile(filepath.Join("pages", MarkdownFilename(slug)), fmt.Sprintf("wiki: update page %s", slug))
}

func (c *GitAutoCommitter) CommitSecureBlockSave(pageSlug, blockID string) error {
	filename := fmt.Sprintf("%s__%s.json", pageSlug, blockID)
	return c.commitFile(filepath.Join("secure", filename), fmt.Sprintf("wiki: update secure block %s/%s", pageSlug, blockID))
}

func (c *GitAutoCommitter) commitFile(relativeFilePath, message string) error {
	if c == nil || c.dataDir == "" || !c.isRepo() {
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

func (c *GitAutoCommitter) isRepo() bool {
	cmd := exec.Command("git", "-C", c.dataDir, "rev-parse", "--is-inside-work-tree")
	return cmd.Run() == nil
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
