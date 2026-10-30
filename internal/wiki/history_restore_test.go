package wiki

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mnorrsken/gypsum/web"
)

func TestRestorePageRevision(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dataDir := t.TempDir()
	store := NewPageStore(filepath.Join(dataDir, "pages"))
	commit := NewGitAutoCommitter(dataDir, nil)
	h := NewHandler(store, NewMarkdownRenderer(), nil, commit, nil, nil, AllMCPSections)
	routes := h.Routes()

	save := func(content string) {
		t.Helper()
		if err := store.Save(KindPage, "Notes", content); err != nil {
			t.Fatal(err)
		}
		if err := commit.CommitSave(KindPage, "Notes", "tester"); err != nil {
			t.Fatal(err)
		}
	}
	save("# Notes\n\nFirst version.\n")
	first := strings.TrimSpace(runGit(t, dataDir, "rev-parse", "HEAD"))
	save("# Notes\n\nSecond version.\n")

	restore := func(hash string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/restore/Notes", strings.NewReader(url.Values{"hash": {hash}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, req)
		return rec
	}

	rec := restore(first)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/wiki/Notes" {
		t.Fatalf("restore: status %d, location %q, body %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	page, _ := store.Load(KindPage, "Notes")
	if page.Content != "# Notes\n\nFirst version.\n" {
		t.Fatalf("content after restore = %q", page.Content)
	}
	if msg := strings.TrimSpace(runGit(t, dataDir, "log", "-1", "--format=%s")); msg != "wiki: restore page Notes to "+first[:7] {
		t.Errorf("restore commit message = %q", msg)
	}
	if n := strings.Count(runGit(t, dataDir, "log", "--format=%h", "--", "pages/Notes.md"), "\n"); n != 3 {
		t.Errorf("history has %d revisions, want 3 (the restore is a new one)", n)
	}

	// Restoring the version that is already current changes nothing.
	if rec := restore(first); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/history/Notes" {
		t.Errorf("no-op restore: status %d, location %q", rec.Code, rec.Header().Get("Location"))
	}
	if n := strings.Count(runGit(t, dataDir, "log", "--format=%h", "--", "pages/Notes.md"), "\n"); n != 3 {
		t.Errorf("no-op restore added a commit (%d revisions)", n)
	}

	if rec := restore("not-a-hash"); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid hash: status %d, want 400", rec.Code)
	}
	if rec := restore("0123456789abcdef0123456789abcdef01234567"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown revision: status %d, want 404", rec.Code)
	}
}

func TestHistoryTabsMatchKind(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dataDir := t.TempDir()
	store := NewPageStore(filepath.Join(dataDir, "pages"))
	commit := NewGitAutoCommitter(dataDir, nil)
	h := NewHandler(store, NewMarkdownRenderer(), web.Templates(), commit, nil, nil, AllMCPSections)
	routes := h.Routes()

	tests := []struct {
		kind       DocKind
		path       string
		want, deny string
	}{
		{KindPage, "/history/Same_Slug", `href="/share/Same_Slug"`, `href="/share-skill/Same_Slug"`},
		{KindSkill, "/skill-history/Same_Slug", `href="/share-skill/Same_Slug"`, `href="/share/Same_Slug"`},
	}
	for _, tc := range tests {
		if err := store.Save(tc.kind, "Same_Slug", "# Same Slug\n"); err != nil {
			t.Fatal(err)
		}
		if err := commit.CommitSave(tc.kind, "Same_Slug", "tester"); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, tc.want) || strings.Contains(body, tc.deny) {
			t.Errorf("%s: status %d, want link %s and not %s\n%s", tc.path, rec.Code, tc.want, tc.deny, body)
		}
	}
}
