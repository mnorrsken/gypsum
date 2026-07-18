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

	if err := committer.CommitSave(KindPage,"Home", ""); err != nil {
		t.Fatalf("CommitPageSave failed after auto-init: %v", err)
	}
}

func TestGitAutoCommitterRecoversFromStaleLock(t *testing.T) {
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

	committer := NewGitAutoCommitter(dataDir, nil) // auto-initializes repo

	// Simulate a git process that was killed mid-write, leaving index.lock.
	lock := filepath.Join(dataDir, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatalf("failed to plant stale lock: %v", err)
	}

	if err := committer.CommitSave(KindPage, "Home", ""); err != nil {
		t.Fatalf("CommitSave should recover from stale lock, got: %v", err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("expected stale lock to be removed, stat err: %v", err)
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
	if err := committer.CommitSave(KindPage,"Home", ""); err != nil {
		t.Fatalf("CommitPageSave failed: %v", err)
	}

	count := strings.TrimSpace(runGit(t, dataDir, "rev-list", "--count", "HEAD"))
	if count != "1" {
		t.Fatalf("expected one commit, got %q", count)
	}

	if err := committer.CommitSave(KindPage,"Home", ""); err != nil {
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
	if err := committer.CommitSave(KindPage,"_favorites", ""); err != nil {
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

func TestSyncStatusNoRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dataDir := t.TempDir()
	committer := NewGitAutoCommitter(dataDir, nil)
	st := committer.SyncStatus()
	if st.Enabled {
		t.Fatalf("expected Enabled=false when no remote is configured")
	}
}

func TestSyncStatusRecordsFetchFailure(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "pages"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "pages", "Home.md"), []byte("# Home"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Point at a non-existent local repo so the initial fetch fails fast
	// without touching the network.
	bogus := filepath.Join(t.TempDir(), "does-not-exist.git")
	committer := NewGitAutoCommitter(dataDir, &GitRemoteConfig{
		RemoteName: "origin",
		RemoteURL:  "file://" + bogus,
	})
	// Create a commit so a branch exists, then attempt a sync.
	if err := committer.CommitSave(KindPage, "Home", ""); err != nil {
		t.Fatalf("CommitSave: %v", err)
	}
	committer.mu.Lock()
	committer.pullRebase()
	committer.mu.Unlock()

	st := committer.SyncStatus()
	if !st.Enabled {
		t.Fatalf("expected Enabled=true with remote configured")
	}
	if st.OK {
		t.Fatalf("expected OK=false after a failed fetch, got status %+v", st)
	}
	if st.Error == "" {
		t.Fatalf("expected a non-empty error message after failed fetch")
	}
	if st.Syncing {
		t.Fatalf("expected Syncing=false once the fetch attempt returned")
	}
}

func TestSanitizeGitError(t *testing.T) {
	in := "fetch failed: git [fetch origin] failed: https://user:secret@example.com/repo.git not found"
	got := sanitizeGitError(in)
	if strings.Contains(got, "secret") {
		t.Fatalf("sanitizeGitError leaked credentials: %q", got)
	}
	if !strings.Contains(got, "://***@") {
		t.Fatalf("expected masked credentials, got %q", got)
	}
}
