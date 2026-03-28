package wiki

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// echoHandler is a simple handler that returns 200 and the authenticated username.
var echoHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	user := UsernameFromRequest(r)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(user))
})

func TestHeaderAuthAllowsValidUser(t *testing.T) {
	cfg := HeaderAuthConfig{UserHeader: "X-User"}
	mw := HeaderAuth(cfg)(echoHandler)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/wiki/Home", nil)
	r.Header.Set("X-User", "alice")
	mw.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != "alice" {
		t.Fatalf("username = %q, want alice", w.Body.String())
	}
}

func TestHeaderAuthRejectsMissingUser(t *testing.T) {
	cfg := HeaderAuthConfig{UserHeader: "X-User"}
	mw := HeaderAuth(cfg)(echoHandler)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/wiki/Home", nil)
	mw.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestHeaderAuthGroupRequired(t *testing.T) {
	cfg := HeaderAuthConfig{
		UserHeader:    "X-User",
		GroupHeader:   "X-Groups",
		RequiredGroup: "wiki-admins",
	}
	mw := HeaderAuth(cfg)(echoHandler)

	// User in the required group.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/pages", nil)
	r.Header.Set("X-User", "bob")
	r.Header.Set("X-Groups", "users,wiki-admins,editors")
	mw.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestHeaderAuthGroupMissing(t *testing.T) {
	cfg := HeaderAuthConfig{
		UserHeader:    "X-User",
		GroupHeader:   "X-Groups",
		RequiredGroup: "wiki-admins",
	}
	mw := HeaderAuth(cfg)(echoHandler)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/pages", nil)
	r.Header.Set("X-User", "charlie")
	r.Header.Set("X-Groups", "users,editors")
	mw.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestHeaderAuthGroupHeaderEmpty(t *testing.T) {
	cfg := HeaderAuthConfig{
		UserHeader:    "X-User",
		GroupHeader:   "X-Groups",
		RequiredGroup: "admins",
	}
	mw := HeaderAuth(cfg)(echoHandler)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/pages", nil)
	r.Header.Set("X-User", "dave")
	// No X-Groups header at all.
	mw.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestHeaderAuthSkipsOAuthPaths(t *testing.T) {
	cfg := HeaderAuthConfig{UserHeader: "X-User"}
	mw := HeaderAuth(cfg)(echoHandler)

	paths := []string{
		"/.well-known/oauth-authorization-server",
		"/.well-known/oauth-protected-resource",
		"/oauth/authorize",
		"/oauth/token",
		"/oauth/register",
		"/mcp/external",
	}

	for _, path := range paths {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		// No auth headers set — should still pass.
		mw.ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			t.Fatalf("path %s: status = %d, want 200 (auth bypass)", path, w.Code)
		}
	}
}

func TestHeaderAuthSkipsPublicPaths(t *testing.T) {
	cfg := HeaderAuthConfig{UserHeader: "X-User"}
	mw := HeaderAuth(cfg)(echoHandler)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/public/abc123token", nil)
	mw.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (public path bypass)", w.Code)
	}
}

func TestHeaderAuthSkipsStaticAssets(t *testing.T) {
	cfg := HeaderAuthConfig{UserHeader: "X-User"}
	mw := HeaderAuth(cfg)(echoHandler)

	paths := []string{"/static/style.css", "/static/app.js", "/static/fonts/inter.woff2"}

	for _, path := range paths {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		mw.ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			t.Fatalf("path %s: status = %d, want 200 (static bypass)", path, w.Code)
		}
	}
}

func TestHeaderAuthDoesNotSkipRegularPaths(t *testing.T) {
	cfg := HeaderAuthConfig{UserHeader: "X-User"}
	mw := HeaderAuth(cfg)(echoHandler)

	paths := []string{"/wiki/Home", "/edit/Home", "/pages", "/search", "/share/Home"}

	for _, path := range paths {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		// No auth headers.
		mw.ServeHTTP(w, r)

		if w.Code != http.StatusForbidden {
			t.Fatalf("path %s: status = %d, want 403 (auth required)", path, w.Code)
		}
	}
}

func TestUsernameFromRequestNoContext(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if got := UsernameFromRequest(r); got != "" {
		t.Fatalf("expected empty username, got %q", got)
	}
}

func TestUsernameFromRequestSetByMiddleware(t *testing.T) {
	cfg := HeaderAuthConfig{UserHeader: "Remote-User"}
	mw := HeaderAuth(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UsernameFromRequest(r)
		if user != "testuser" {
			t.Fatalf("username = %q, want testuser", user)
		}
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/wiki/Home", nil)
	r.Header.Set("Remote-User", "testuser")
	mw.ServeHTTP(w, r)
}

func TestContainsGroup(t *testing.T) {
	tests := []struct {
		header string
		needle string
		want   bool
	}{
		{"users,admins,editors", "admins", true},
		{"users,admins,editors", "root", false},
		{"admins", "admins", true},
		{"", "admins", false},
		{" admins , users ", "admins", true},
		{"admin", "admins", false},
	}

	for _, tt := range tests {
		got := containsGroup(tt.header, tt.needle)
		if got != tt.want {
			t.Errorf("containsGroup(%q, %q) = %v, want %v", tt.header, tt.needle, got, tt.want)
		}
	}
}
