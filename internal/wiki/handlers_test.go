package wiki

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRealTemplatesParse loads every page template from web/templates against
// the real base layout and fails if any of them no longer parse. This catches
// stray "{{" in script blocks, which html/template would silently reject and
// then leave the route returning "template not found".
func TestRealTemplatesParse(t *testing.T) {
	templatesDir, err := filepath.Abs(filepath.Join("..", "..", "web", "templates"))
	if err != nil {
		t.Fatalf("resolve templates dir: %v", err)
	}
	if _, err := os.Stat(templatesDir); err != nil {
		t.Skipf("templates dir not present: %v", err)
	}
	store := NewPageStore(t.TempDir())
	h := NewHandler(store, NewMarkdownRenderer(), templatesDir, nil, nil, nil, AllMCPSections)

	expected := []string{
		"view", "edit", "new", "search", "pages", "history",
		"history_diff", "images", "diff", "graph", "recent_edits", "share",
		"doc", "docs", "skills", "skill_view", "skill_edit",
	}
	for _, name := range expected {
		if _, ok := h.tmplCache[name]; !ok {
			t.Errorf("template %q failed to parse and is missing from cache", name)
		}
	}
}

// newTestHandler creates a Handler with a temp directory and no templates (JSON/redirect-only tests).
func newTestHandler(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	store := NewPageStore(dir)
	renderer := NewMarkdownRenderer()
	h := NewHandler(store, renderer, t.TempDir(), nil, nil, nil, AllMCPSections)
	return h, h.Routes()
}

// ── Index handler ─────────────────────────────────────────────────────────

func TestHandleIndexRedirects(t *testing.T) {
	_, handler := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/wiki/Home" {
		t.Fatalf("expected redirect to /wiki/Home, got %s", loc)
	}
}

func TestHandleIndexNotFoundForOtherPaths(t *testing.T) {
	_, handler := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// ── ConvertMediaWiki handler ─────────────────────────────────────────────

func TestHandleConvertMediaWiki(t *testing.T) {
	_, handler := newTestHandler(t)

	form := url.Values{"wikitext": {"== Heading =="}}
	req := httptest.NewRequest(http.MethodPost, "/convert/mediawiki", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if ok, _ := payload["ok"].(bool); !ok {
		t.Fatalf("expected ok=true, got %v", payload)
	}
	if md, _ := payload["markdown"].(string); !strings.Contains(md, "## Heading") {
		t.Fatalf("expected converted markdown, got: %s", md)
	}
}

func TestHandleConvertMediaWikiEmpty(t *testing.T) {
	_, handler := newTestHandler(t)

	form := url.Values{"wikitext": {""}}
	req := httptest.NewRequest(http.MethodPost, "/convert/mediawiki", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleConvertMediaWikiWrongMethod(t *testing.T) {
	_, handler := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/convert/mediawiki", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

// ── ImageList handler (JSON) ──────────────────────────────────────────────

func TestHandleImageListEmpty(t *testing.T) {
	_, handler := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/images/list", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if ok, _ := payload["ok"].(bool); !ok {
		t.Fatalf("expected ok=true")
	}
	images, _ := payload["images"].([]any)
	if len(images) != 0 {
		t.Fatalf("expected 0 images, got %d", len(images))
	}
}

func TestHandleImageListWithFiles(t *testing.T) {
	h, handler := newTestHandler(t)

	imagesDir := h.store.ImagesDir()
	_ = os.WriteFile(filepath.Join(imagesDir, "test.png"), []byte("fake"), 0o644)

	req := httptest.NewRequest(http.MethodGet, "/images/list", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	images, _ := payload["images"].([]any)
	if len(images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(images))
	}
}

func TestHandleImageListWrongMethod(t *testing.T) {
	_, handler := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/images/list", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

// ── ImageDelete handler ──────────────────────────────────────────────────

func TestHandleImageDeleteInvalidFilename(t *testing.T) {
	_, handler := newTestHandler(t)

	form := url.Values{"filename": {"../../../etc/passwd"}}
	req := httptest.NewRequest(http.MethodPost, "/images/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleImageDeleteEmptyFilename(t *testing.T) {
	_, handler := newTestHandler(t)

	form := url.Values{"filename": {""}}
	req := httptest.NewRequest(http.MethodPost, "/images/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleImageDeleteNonExistent(t *testing.T) {
	_, handler := newTestHandler(t)

	form := url.Values{"filename": {"no-such-file.png"}}
	req := httptest.NewRequest(http.MethodPost, "/images/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for missing file, got %d", rec.Code)
	}
}

func TestHandleImageDeleteSuccess(t *testing.T) {
	h, handler := newTestHandler(t)

	imagesDir := h.store.ImagesDir()
	_ = os.WriteFile(filepath.Join(imagesDir, "deleteme.png"), []byte("img"), 0o644)

	form := url.Values{"filename": {"deleteme.png"}}
	req := httptest.NewRequest(http.MethodPost, "/images/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/images" {
		t.Fatalf("expected redirect to /images, got %s", loc)
	}

	if _, err := os.Stat(filepath.Join(imagesDir, "deleteme.png")); !os.IsNotExist(err) {
		t.Fatal("expected file to be deleted")
	}
}

// ── DeletePage handler ────────────────────────────────────────────────────

func TestHandleDeletePageNotFound(t *testing.T) {
	_, handler := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/delete/NoSuchPage", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleDeletePageMissingSlug(t *testing.T) {
	_, handler := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/delete/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleDeletePageWrongMethod(t *testing.T) {
	_, handler := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/delete/SomePage", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestHandleDeletePageSuccess(t *testing.T) {
	h, handler := newTestHandler(t)
	_ = h.store.Save(KindPage, "ToDelete", "content")

	req := httptest.NewRequest(http.MethodPost, "/delete/ToDelete", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/pages" {
		t.Fatalf("expected redirect to /pages, got %s", loc)
	}
}

// ── View handler redirects ────────────────────────────────────────────────

func TestHandleViewEmptySlugRedirects(t *testing.T) {
	_, handler := newTestHandler(t)

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

func TestHandleViewNonExistentRedirectsToEdit(t *testing.T) {
	_, handler := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/wiki/NewPage", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/edit/NewPage") {
		t.Fatalf("expected redirect to /edit/NewPage, got %s", loc)
	}
}

func TestHandleViewWrongMethod(t *testing.T) {
	_, handler := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/wiki/Home", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

// ── Edit handler edge cases ───────────────────────────────────────────────

func TestHandleEditMissingSlug(t *testing.T) {
	_, handler := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/edit/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleEditWrongMethod(t *testing.T) {
	_, handler := newTestHandler(t)

	req := httptest.NewRequest(http.MethodDelete, "/edit/SomePage", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

// ── Pure functions ────────────────────────────────────────────────────────

func TestSanitizeImageBasename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"my-photo.png", "my-photo"},
		{"Screenshot 2024.png", "screenshot-2024"},
		{"image.png", ""},
		{"pasted-image.jpg", ""},
		{"screenshot.webp", ""},
		{"UPPERCASE_NAME.png", "uppercase-name"},
		{"  ", ""},
		{"my document (final).pdf", "my-document-final"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeImageBasename(tt.input)
			if got != tt.want {
				t.Fatalf("sanitizeImageBasename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestMinLen(t *testing.T) {
	if got := minLen(3, 5); got != 3 {
		t.Fatalf("minLen(3,5) = %d, want 3", got)
	}
	if got := minLen(7, 2); got != 2 {
		t.Fatalf("minLen(7,2) = %d, want 2", got)
	}
	if got := minLen(4, 4); got != 4 {
		t.Fatalf("minLen(4,4) = %d, want 4", got)
	}
}

func TestBuildShareURL(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/share/test", nil)
	req.Host = "wiki.example.com"

	got := buildShareURL(req, "abc123")
	if got != "http://wiki.example.com/public/abc123" {
		t.Fatalf("unexpected URL: %s", got)
	}

	// With X-Forwarded-Proto
	req.Header.Set("X-Forwarded-Proto", "https")
	got = buildShareURL(req, "abc123")
	if got != "https://wiki.example.com/public/abc123" {
		t.Fatalf("unexpected URL with proto header: %s", got)
	}
}

func TestDocTitleFromSlug(t *testing.T) {
	tests := []struct {
		slug string
		want string
	}{
		{"authentication", "Authentication"},
		{"my-guide", "My Guide"},
		{"getting-started", "Getting Started"},
	}

	for _, tt := range tests {
		t.Run(tt.slug, func(t *testing.T) {
			got := docTitleFromSlug(tt.slug)
			if got != tt.want {
				t.Fatalf("docTitleFromSlug(%q) = %q, want %q", tt.slug, got, tt.want)
			}
		})
	}
}

// ── Search handler (method check) ─────────────────────────────────────────

func TestHandleSearchWrongMethod(t *testing.T) {
	_, handler := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/search?q=test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

// ── Pages handler (method check) ──────────────────────────────────────────

func TestHandlePagesWrongMethod(t *testing.T) {
	_, handler := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/pages", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

// ── History handler edge cases ────────────────────────────────────────────

func TestHandleHistoryMissingSlug(t *testing.T) {
	_, handler := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/history/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleHistoryWrongMethod(t *testing.T) {
	_, handler := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/history/Home", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

// ── HistoryDiff handler edge cases ────────────────────────────────────────

func TestHandleHistoryDiffMissingParams(t *testing.T) {
	_, handler := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/history-diff/Home", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleHistoryDiffMissingSlug(t *testing.T) {
	_, handler := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/history-diff/?from=a&to=b", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// ── ImageUpload handler edge cases ────────────────────────────────────────

func TestHandleImageUploadWrongMethod(t *testing.T) {
	_, handler := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/images/upload", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

// ── NewPage handler ───────────────────────────────────────────────────────

func TestHandleNewPageWrongMethod(t *testing.T) {
	_, handler := newTestHandler(t)

	req := httptest.NewRequest(http.MethodDelete, "/new", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

// ── MCP route auth enforcement ────────────────────────────────────────────

// newTestHandlerWithOAuth creates a Handler that has OAuth configured.
// Returns the Handler, the DB (for minting tokens), and the Routes handler.
func newTestHandlerWithOAuth(t *testing.T) (*Handler, *DB, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	store := NewPageStore(dir)
	renderer := NewMarkdownRenderer()
	db := openTestDB(t)
	oauth := NewOAuthServer("test-pass", "https://wiki.example.com", time.Hour, db)
	h := NewHandler(store, renderer, t.TempDir(), nil, oauth, db, AllMCPSections)
	return h, db, h.Routes()
}

// pingBody is a minimal JSON-RPC request suitable for testing /mcp.
var pingBody = []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)

func TestMCPRouteNotRegisteredWithoutOAuth(t *testing.T) {
	_, handler := newTestHandler(t) // no OAuth configured

	for _, path := range []string{"/mcp", "/mcp/external"} {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(pingBody))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404 for %s without OAuth, got %d", path, rec.Code)
		}
	}
}

func TestMCPRouteRequiresTokenWhenOAuthConfigured(t *testing.T) {
	_, _, handler := newTestHandlerWithOAuth(t)

	for _, path := range []string{"/mcp", "/mcp/external"} {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(pingBody))
		req.Header.Set("Content-Type", "application/json")
		// No Authorization header.
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 for %s without token, got %d", path, rec.Code)
		}
		if rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("expected WWW-Authenticate header on 401 for %s", path)
		}
	}
}

func TestMCPRouteRejectsInvalidToken(t *testing.T) {
	_, _, handler := newTestHandlerWithOAuth(t)

	for _, path := range []string{"/mcp", "/mcp/external"} {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(pingBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer not-a-real-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 for %s with invalid token, got %d", path, rec.Code)
		}
	}
}

func TestMCPRouteAcceptsValidToken(t *testing.T) {
	_, db, handler := newTestHandlerWithOAuth(t)

	db.SaveOAuthToken("valid-token", time.Now().Add(time.Hour))

	for _, path := range []string{"/mcp", "/mcp/external"} {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(pingBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer valid-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 for %s with valid token, got %d; body: %s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestMCPRouteRejectsExpiredToken(t *testing.T) {
	_, db, handler := newTestHandlerWithOAuth(t)

	db.SaveOAuthToken("expired-token", time.Now().Add(-time.Hour))

	for _, path := range []string{"/mcp", "/mcp/external"} {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(pingBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer expired-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 for %s with expired token, got %d", path, rec.Code)
		}
	}
}

func TestHandleNewPagePostRedirects(t *testing.T) {
	_, handler := newTestHandler(t)

	form := url.Values{"title": {"My New Page"}}
	req := httptest.NewRequest(http.MethodPost, "/new", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d; body: %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/edit/My_New_Page") {
		t.Fatalf("expected redirect to /edit/My_New_Page, got %s", loc)
	}
}
