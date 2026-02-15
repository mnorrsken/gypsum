package wiki

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitAutoCommitterNoRepoIsNoop(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "pages"), 0o755); err != nil {
		t.Fatalf("failed to create pages dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "pages", "Home.md"), []byte("# Home"), 0o644); err != nil {
		t.Fatalf("failed to write page: %v", err)
	}

	committer := NewGitAutoCommitter(dataDir)
	if err := committer.CommitPageSave("Home"); err != nil {
		t.Fatalf("expected no error for non-repo data dir, got %v", err)
	}
}

func TestGitAutoCommitterCommitsChangedFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "pages"), 0o755); err != nil {
		t.Fatalf("failed to create pages dir: %v", err)
	}

	runGit(t, dataDir, "init")

	pagePath := filepath.Join(dataDir, "pages", "Home.md")
	if err := os.WriteFile(pagePath, []byte("# Home\nhello"), 0o644); err != nil {
		t.Fatalf("failed to write page: %v", err)
	}

	committer := NewGitAutoCommitter(dataDir)
	if err := committer.CommitPageSave("Home"); err != nil {
		t.Fatalf("CommitPageSave failed: %v", err)
	}

	count := strings.TrimSpace(runGit(t, dataDir, "rev-list", "--count", "HEAD"))
	if count != "1" {
		t.Fatalf("expected one commit, got %q", count)
	}

	if err := committer.CommitPageSave("Home"); err != nil {
		t.Fatalf("second CommitPageSave failed: %v", err)
	}
	count = strings.TrimSpace(runGit(t, dataDir, "rev-list", "--count", "HEAD"))
	if count != "1" {
		t.Fatalf("expected still one commit after no-op commit, got %q", count)
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v (%s)", args, err, string(output))
	}
	return string(output)
}
