package wiki

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitAutoCommitterAutoInitsRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "pages"), 0o755); err != nil {
		t.Fatalf("failed to create pages dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "pages", "Home.md"), []byte("# Home"), 0o644); err != nil {
		t.Fatalf("failed to write page: %v", err)
	}

	committer := NewGitAutoCommitter(dataDir, nil)

	// ensureRepo should have created a .git directory
	info, err := os.Stat(filepath.Join(dataDir, ".git"))
	if err != nil || !info.IsDir() {
		t.Fatalf("expected .git directory to be auto-created in data dir")
	}

	if err := committer.CommitPageSave("Home"); err != nil {
		t.Fatalf("CommitPageSave failed after auto-init: %v", err)
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

	pagePath := filepath.Join(dataDir, "pages", "Home.md")
	if err := os.WriteFile(pagePath, []byte("# Home\nhello"), 0o644); err != nil {
		t.Fatalf("failed to write page: %v", err)
	}

	committer := NewGitAutoCommitter(dataDir, nil) // auto-initializes repo
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

func TestGitAutoCommitterCommitsIgnoredFavoritesFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "pages"), 0o755); err != nil {
		t.Fatalf("failed to create pages dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dataDir, ".gitignore"), []byte("pages/_*.md\n"), 0o644); err != nil {
		t.Fatalf("failed to write gitignore: %v", err)
	}

	favoritesPath := filepath.Join(dataDir, "pages", "_favorites.md")
	if err := os.WriteFile(favoritesPath, []byte("[[Home]]"), 0o644); err != nil {
		t.Fatalf("failed to write favorites page: %v", err)
	}

	committer := NewGitAutoCommitter(dataDir, nil)
	if err := committer.CommitPageSave("_favorites"); err != nil {
		t.Fatalf("CommitPageSave for _favorites failed: %v", err)
	}

	count := strings.TrimSpace(runGit(t, dataDir, "rev-list", "--count", "HEAD"))
	if count != "1" {
		t.Fatalf("expected one commit, got %q", count)
	}

	tracked := strings.TrimSpace(runGit(t, dataDir, "ls-files", "--", "pages/_favorites.md"))
	if tracked != "pages/_favorites.md" {
		t.Fatalf("expected pages/_favorites.md to be tracked, got %q", tracked)
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
