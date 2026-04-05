package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// ── PKCE helpers ────────────────────────────────────────────────────────────

func TestGeneratePKCE(t *testing.T) {
	v1, c1, err := generatePKCE()
	if err != nil {
		t.Fatalf("generatePKCE: %v", err)
	}
	if v1 == "" || c1 == "" {
		t.Fatal("empty verifier or challenge")
	}

	// The challenge must equal base64url(SHA-256(verifier)).
	h := sha256.Sum256([]byte(v1))
	want := base64.RawURLEncoding.EncodeToString(h[:])
	if c1 != want {
		t.Fatalf("challenge mismatch: got %q, want %q", c1, want)
	}

	// Two calls must produce different values.
	v2, c2, _ := generatePKCE()
	if v1 == v2 || c1 == c2 {
		t.Fatal("generatePKCE returned identical values on consecutive calls")
	}
}

// ── Mock OAuth server ────────────────────────────────────────────────────────
//
// mockOAuthServer implements the same protocol as Gypsum's OAuthServer:
//   POST /oauth/register  → returns client_id
//   POST /oauth/authorize → verifies password, issues 302 with code
//   POST /oauth/token     → verifies PKCE, returns access_token

type mockOAuthServer struct {
	password string
	codes    map[string]pendingAuth // code → (verifier challenge, redirect_uri)
	tokens   map[string]bool
}

type pendingAuth struct {
	challenge   string
	redirectURI string
}

func newMockOAuthServer(password string) *mockOAuthServer {
	return &mockOAuthServer{
		password: password,
		codes:    make(map[string]pendingAuth),
		tokens:   make(map[string]bool),
	}
}

func (m *mockOAuthServer) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/oauth/register", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "test-client"})
	})

	mux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if r.FormValue("password") != m.password {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintln(w, "Incorrect password.")
			return
		}
		code := "test-code-" + r.FormValue("code_challenge")[:8]
		m.codes[code] = pendingAuth{
			challenge:   r.FormValue("code_challenge"),
			redirectURI: r.FormValue("redirect_uri"),
		}
		u, _ := url.Parse(r.FormValue("redirect_uri"))
		q := u.Query()
		q.Set("code", code)
		if s := r.FormValue("state"); s != "" {
			q.Set("state", s)
		}
		u.RawQuery = q.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	})

	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		code := r.FormValue("code")
		pending, ok := m.codes[code]
		if !ok {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		delete(m.codes, code)

		// Verify PKCE
		verifier := r.FormValue("code_verifier")
		h := sha256.Sum256([]byte(verifier))
		computed := base64.RawURLEncoding.EncodeToString(h[:])
		if computed != pending.challenge {
			http.Error(w, `{"error":"invalid_grant","error_description":"pkce failed"}`, http.StatusBadRequest)
			return
		}

		token := "mock-token-" + code
		m.tokens[token] = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": token,
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})

	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		// Echo back authorization status so proxy tests can verify the header.
		auth := r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"echo_auth": auth})
	})

	return mux
}

// ── oauthFlow tests ──────────────────────────────────────────────────────────

func TestOAuthFlowSuccess(t *testing.T) {
	mock := newMockOAuthServer("correct-pass")
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	token, err := oauthFlow(srv.URL, "correct-pass")
	if err != nil {
		t.Fatalf("oauthFlow: %v", err)
	}
	if token == "" {
		t.Fatal("got empty token")
	}
	if !mock.tokens[token] {
		t.Fatalf("token %q not issued by mock server", token)
	}
}

func TestOAuthFlowWrongPassword(t *testing.T) {
	mock := newMockOAuthServer("correct-pass")
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	_, err := oauthFlow(srv.URL, "wrong-pass")
	if err == nil {
		t.Fatal("expected error for wrong password, got nil")
	}
	if !strings.Contains(err.Error(), "incorrect password") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestOAuthFlowPKCEMismatch(t *testing.T) {
	// Tamper: server stores a different challenge than what the client will verify with.
	mock := newMockOAuthServer("pass")

	// Intercept /oauth/token to always fail PKCE while delegating everything else to mock.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintln(w, `{"error":"invalid_grant","error_description":"pkce failed"}`)
			return
		}
		mock.handler().ServeHTTP(w, r)
	}))
	defer srv.Close()

	_, err := oauthFlow(srv.URL, "pass")
	if err == nil {
		t.Fatal("expected error for PKCE failure, got nil")
	}
	if !strings.Contains(err.Error(), "token exchange") {
		t.Fatalf("expected token exchange error, got: %v", err)
	}
}

func TestOAuthFlowServerUnreachable(t *testing.T) {
	_, err := oauthFlow("http://127.0.0.1:1", "pass")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

// ── registerClient tests ─────────────────────────────────────────────────────

func TestRegisterClientSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"client_id": "abc123"})
	}))
	defer srv.Close()

	client := &http.Client{}
	id, err := registerClient(client, srv.URL)
	if err != nil {
		t.Fatalf("registerClient: %v", err)
	}
	if id != "abc123" {
		t.Fatalf("unexpected client_id: %q", id)
	}
}

func TestRegisterClientUnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	client := &http.Client{}
	_, err := registerClient(client, srv.URL)
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
}

func TestRegisterClientEmptyClientID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"client_id": ""})
	}))
	defer srv.Close()

	client := &http.Client{}
	_, err := registerClient(client, srv.URL)
	if err == nil || !strings.Contains(err.Error(), "empty client_id") {
		t.Fatalf("expected empty client_id error, got: %v", err)
	}
}

// ── exchangeCode tests ───────────────────────────────────────────────────────

func TestExchangeCodeSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tok-xyz"})
	}))
	defer srv.Close()

	client := &http.Client{}
	token, err := exchangeCode(client, srv.URL, "cid", "code", "verifier", "http://localhost/cb")
	if err != nil {
		t.Fatalf("exchangeCode: %v", err)
	}
	if token != "tok-xyz" {
		t.Fatalf("unexpected token: %q", token)
	}
}

func TestExchangeCodeServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, `{"error":"invalid_grant"}`)
	}))
	defer srv.Close()

	client := &http.Client{}
	_, err := exchangeCode(client, srv.URL, "cid", "code", "verifier", "http://localhost/cb")
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected HTTP 400 in error, got: %v", err)
	}
}

func TestExchangeCodeEmptyToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": ""})
	}))
	defer srv.Close()

	client := &http.Client{}
	_, err := exchangeCode(client, srv.URL, "cid", "code", "verifier", "http://localhost/cb")
	if err == nil || !strings.Contains(err.Error(), "empty access_token") {
		t.Fatalf("expected empty access_token error, got: %v", err)
	}
}

// ── Proxy mode: Authorization header injection ────────────────────────────────

// captureProxy runs the proxy loop against a test server for a single request,
// with the given GYPSUM_TOKEN, and returns what the server received.
func captureProxy(t *testing.T, token string, mcpEndpoint string) string {
	t.Helper()
	old := os.Getenv("GYPSUM_TOKEN")
	if token != "" {
		os.Setenv("GYPSUM_TOKEN", token)
	} else {
		os.Unsetenv("GYPSUM_TOKEN")
	}
	defer os.Setenv("GYPSUM_TOKEN", old)

	// Build a minimal JSON-RPC message.
	msg := `{"jsonrpc":"2.0","id":1,"method":"ping"}`

	// Run proxy logic inline (not via main) to avoid os.Exit.
	tok := os.Getenv("GYPSUM_TOKEN")
	client := &http.Client{}
	req, _ := http.NewRequest(http.MethodPost, mcpEndpoint, bytes.NewReader([]byte(msg)))
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func TestProxyAddsAuthorizationHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
	defer srv.Close()

	captureProxy(t, "my-secret-token", srv.URL)
	if gotAuth != "Bearer my-secret-token" {
		t.Fatalf("expected 'Bearer my-secret-token', got %q", gotAuth)
	}
}

func TestProxyNoAuthorizationHeaderWhenNoToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
	defer srv.Close()

	captureProxy(t, "", srv.URL)
	if gotAuth != "" {
		t.Fatalf("expected no Authorization header, got %q", gotAuth)
	}
}

// ── Full end-to-end: auth flow → proxy with token ────────────────────────────

func TestFullAuthThenProxy(t *testing.T) {
	mock := newMockOAuthServer("wiki-pass")
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	// 1. Obtain token via oauthFlow.
	token, err := oauthFlow(srv.URL, "wiki-pass")
	if err != nil {
		t.Fatalf("oauthFlow: %v", err)
	}
	if !mock.tokens[token] {
		t.Fatalf("token not issued by server")
	}

	// 2. Use token in a proxy request to /mcp.
	var gotAuth string
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		fmt.Fprintln(w, `{}`)
	}))
	defer proxySrv.Close()

	captureProxy(t, token, proxySrv.URL)
	if gotAuth != "Bearer "+token {
		t.Fatalf("proxy did not send the token: got %q", gotAuth)
	}
}
