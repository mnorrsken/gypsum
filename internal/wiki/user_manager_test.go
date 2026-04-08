package wiki

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitTemplateConfigExpandRemote(t *testing.T) {
	tmpl := &GitTemplateConfig{
		URLTemplate: "https://git.example.com/~{user}/gypsum.git",
		RemoteName:  "origin",
		CommitEmail: "{user}@example.com",
		Token:       "mytoken",
	}

	remote := tmpl.ExpandRemote("alice")
	if remote == nil {
		t.Fatal("expected non-nil remote config")
	}
	if remote.RemoteName != "origin" {
		t.Fatalf("expected RemoteName=origin, got %s", remote.RemoteName)
	}
	if !strings.Contains(remote.RemoteURL, "mytoken@git.example.com") {
		t.Fatalf("expected token in URL, got %s", remote.RemoteURL)
	}
	if !strings.Contains(remote.RemoteURL, "/~alice/") {
		t.Fatalf("expected username in URL, got %s", remote.RemoteURL)
	}
	if remote.CommitName != "alice" {
		t.Fatalf("expected CommitName=alice, got %s", remote.CommitName)
	}
	if remote.CommitEmail != "alice@example.com" {
		t.Fatalf("expected CommitEmail=alice@example.com, got %s", remote.CommitEmail)
	}
}

func TestGitTemplateConfigExpandRemoteWithUsernamePassword(t *testing.T) {
	tmpl := &GitTemplateConfig{
		URLTemplate: "https://git.example.com/~{user}/wiki.git",
		Username:    "svcaccount",
		Password:    "s3cret",
	}

	remote := tmpl.ExpandRemote("bob")
	if remote == nil {
		t.Fatal("expected non-nil remote config")
	}
	if !strings.Contains(remote.RemoteURL, "svcaccount:s3cret@git.example.com") {
		t.Fatalf("expected username:password in URL, got %s", remote.RemoteURL)
	}
	if !strings.Contains(remote.RemoteURL, "/~bob/") {
		t.Fatalf("expected username in URL path, got %s", remote.RemoteURL)
	}
}

func TestGitTemplateConfigNilReturnsNil(t *testing.T) {
	var tmpl *GitTemplateConfig
	if remote := tmpl.ExpandRemote("alice"); remote != nil {
		t.Fatal("expected nil remote for nil template")
	}
}

func TestGitTemplateConfigEmptyTemplate(t *testing.T) {
	tmpl := &GitTemplateConfig{URLTemplate: ""}
	if remote := tmpl.ExpandRemote("alice"); remote != nil {
		t.Fatal("expected nil remote for empty template")
	}
}

func TestGitTemplateConfigDefaultEmail(t *testing.T) {
	tmpl := &GitTemplateConfig{
		URLTemplate: "https://git.example.com/{user}/wiki.git",
	}
	remote := tmpl.ExpandRemote("carol")
	if remote.CommitEmail != "carol@gypsum.local" {
		t.Fatalf("expected default email carol@gypsum.local, got %s", remote.CommitEmail)
	}
}

func TestUserManagerResolveSharedForEmpty(t *testing.T) {
	sharedStore := NewPageStore(t.TempDir())
	shared := &UserContext{Store: sharedStore}
	mgr := NewUserManager(UserManagerConfig{
		DataDir: t.TempDir(),
		Shared:  shared,
	})
	defer mgr.Stop()

	ctx, err := mgr.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if ctx != shared {
		t.Fatal("expected shared context for empty username")
	}
}

func TestUserManagerResolveProvisions(t *testing.T) {
	dataDir := t.TempDir()
	sharedStore := NewPageStore(t.TempDir())
	shared := &UserContext{Store: sharedStore}
	mgr := NewUserManager(UserManagerConfig{
		DataDir: dataDir,
		Shared:  shared,
	})
	defer mgr.Stop()

	ctx, err := mgr.Resolve("testuser")
	if err != nil {
		t.Fatal(err)
	}
	if ctx == shared {
		t.Fatal("expected per-user context, got shared")
	}
	if ctx.Store == nil {
		t.Fatal("expected non-nil store")
	}
	if ctx.AutoCommit == nil {
		t.Fatal("expected non-nil auto committer")
	}
	if ctx.DB == nil {
		t.Fatal("expected non-nil DB")
	}

	// Verify directories were created
	pagesDir := filepath.Join(dataDir, "users", "testuser", "repo", "pages")
	if _, err := os.Stat(pagesDir); err != nil {
		t.Fatalf("expected pages directory at %s: %v", pagesDir, err)
	}

	// Second call should return the same context (cached)
	ctx2, err := mgr.Resolve("testuser")
	if err != nil {
		t.Fatal(err)
	}
	if ctx2 != ctx {
		t.Fatal("expected same context on second resolve")
	}
}

func TestUserManagerShared(t *testing.T) {
	shared := &UserContext{Store: NewPageStore(t.TempDir())}
	mgr := NewUserManager(UserManagerConfig{
		DataDir: t.TempDir(),
		Shared:  shared,
	})
	defer mgr.Stop()

	if mgr.Shared() != shared {
		t.Fatal("Shared() should return the shared context")
	}
}

func TestUserManagerListUsers(t *testing.T) {
	dataDir := t.TempDir()
	shared := &UserContext{Store: NewPageStore(t.TempDir())}
	mgr := NewUserManager(UserManagerConfig{
		DataDir: dataDir,
		Shared:  shared,
	})
	defer mgr.Stop()

	// No users yet
	if users := mgr.ListUsers(); len(users) != 0 {
		t.Fatalf("expected 0 users, got %d", len(users))
	}

	// Provision a user
	if _, err := mgr.Resolve("alice"); err != nil {
		t.Fatal(err)
	}

	users := mgr.ListUsers()
	if len(users) != 1 || users[0] != "alice" {
		t.Fatalf("expected [alice], got %v", users)
	}
}

func TestUserManagerIsolation(t *testing.T) {
	dataDir := t.TempDir()
	shared := &UserContext{Store: NewPageStore(t.TempDir())}
	mgr := NewUserManager(UserManagerConfig{
		DataDir: dataDir,
		Shared:  shared,
	})
	defer mgr.Stop()

	aliceCtx, _ := mgr.Resolve("alice")
	bobCtx, _ := mgr.Resolve("bob")

	// Save a page to alice's wiki
	if err := aliceCtx.Store.Save(KindPage, "Secret", "alice's secret"); err != nil {
		t.Fatal(err)
	}

	// Bob should not see it
	_, err := bobCtx.Store.Load(KindPage, "Secret")
	if err == nil {
		t.Fatal("bob should not see alice's page")
	}

	// Alice should still see it
	page, err := aliceCtx.Store.Load(KindPage, "Secret")
	if err != nil {
		t.Fatal(err)
	}
	if page.Content != "alice's secret" {
		t.Fatalf("expected alice's secret, got %q", page.Content)
	}
}

// ── Multi-user routing tests ─────────────────────────────────────────────

func newMultiUserTestHandler(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	dataDir := t.TempDir()
	pagesDir := filepath.Join(dataDir, "repo", "pages")
	os.MkdirAll(pagesDir, 0o755)

	store := NewPageStore(pagesDir)
	crypto := NewServerCrypto("test-key")
	renderer := NewMarkdownRenderer()
	h := NewHandler(store, crypto, renderer, t.TempDir(), nil, nil, nil, AllMCPSections)

	shared := &UserContext{
		Store:      store,
		AutoCommit: &GitAutoCommitter{},
	}
	mgr := NewUserManager(UserManagerConfig{
		DataDir: dataDir,
		Shared:  shared,
	})
	h.SetUserManager(mgr)
	t.Cleanup(func() { mgr.Stop() })

	return h, h.Routes()
}

func TestMultiUserIndexRedirect(t *testing.T) {
	_, handler := newMultiUserTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/~alice/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/~alice/wiki/Home" {
		t.Fatalf("expected redirect to /~alice/wiki/Home, got %s", loc)
	}
}

func TestMultiUserViewRedirectsToEdit(t *testing.T) {
	_, handler := newMultiUserTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/~bob/wiki/NewPage", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/~bob/edit/NewPage") {
		t.Fatalf("expected redirect to /~bob/edit/NewPage, got %s", loc)
	}
}

func TestMultiUserMissingUsername(t *testing.T) {
	_, handler := newMultiUserTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/~", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestSharedWikiUnchanged(t *testing.T) {
	_, handler := newMultiUserTestHandler(t)

	// The shared wiki should still work at /wiki/
	req := httptest.NewRequest(http.MethodGet, "/wiki/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/wiki/Home" {
		t.Fatalf("expected redirect to /wiki/Home, got %s", loc)
	}
}

func TestMultiUserDeleteRedirect(t *testing.T) {
	h, handler := newMultiUserTestHandler(t)

	// Provision the user and create a page
	ctx, err := h.userMgr.Resolve("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := ctx.Store.Save(KindPage, "ToDelete", "content"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/~alice/delete/ToDelete", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/~alice/pages" {
		t.Fatalf("expected redirect to /~alice/pages, got %s", loc)
	}
}

// ── URL helper tests ─────────────────────────────────────────────────────

func TestInjectURLAuth(t *testing.T) {
	got := injectURLAuth("https://git.example.com/repo.git", "token123", "")
	if got != "https://token123@git.example.com/repo.git" {
		t.Fatalf("unexpected URL: %s", got)
	}

	got = injectURLAuth("https://git.example.com/repo.git", "user", "pass")
	if got != "https://user:pass@git.example.com/repo.git" {
		t.Fatalf("unexpected URL: %s", got)
	}

	// Non-HTTPS URLs are returned unchanged
	got = injectURLAuth("git@github.com:user/repo.git", "token", "")
	if got != "git@github.com:user/repo.git" {
		t.Fatalf("expected SSH URL unchanged, got %s", got)
	}
}

func TestURLPrefixFromContext(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/wiki/Home", nil)
	if p := urlPrefix(r); p != "" {
		t.Fatalf("expected empty prefix, got %q", p)
	}

	uc := &UserContext{Store: NewPageStore(t.TempDir())}
	r2 := withUserContext(r, uc, "/~alice", "alice")
	if p := urlPrefix(r2); p != "/~alice" {
		t.Fatalf("expected /~alice, got %q", p)
	}
	if u := targetUser(r2); u != "alice" {
		t.Fatalf("expected alice, got %q", u)
	}
}
