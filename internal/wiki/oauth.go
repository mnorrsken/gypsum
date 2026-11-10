package wiki

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OAuthServer implements a minimal OAuth 2.0 Authorization Server for the
// /mcp/external endpoint. It supports the Authorization Code grant with PKCE
// (S256), as required by the MCP 2025-03-26 spec.
//
// Configuration via env vars (read in main.go):
//
//	GYPSUM_OAUTH_ENABLED    — set to "true" to enable
//	GYPSUM_OAUTH_PASSWORD   — single-user wiki password (required)
//	GYPSUM_EXTERNAL_URL     — public base URL, e.g. https://wiki.example.com (required)
//	GYPSUM_OAUTH_TOKEN_TTL  — access token lifetime as Go duration, e.g. "2160h" for 90 days (default: "24h"; valid units: s, m, h — "d" is not supported)
type OAuthServer struct {
	password    string
	tokenTTL    time.Duration
	externalURL string // no trailing slash
	db          *DB    // SQLite database for token persistence

	mu    sync.Mutex
	codes map[string]pendingCode

	proxies ProxyTrust  // reverse proxies whose X-Forwarded-For is believed
	logins  *loginGuard // locks out an address after repeated wrong passwords
}

type pendingCode struct {
	redirectURI   string
	codeChallenge string
	expiry        time.Time
}

// loginFormData holds the OAuth parameters threaded through the login form.
type loginFormData struct {
	ClientID            string
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	State               string
	Error               string
	RedirectHost        string // where the browser is sent after login, shown to the user
}

// NewOAuthServer creates an OAuthServer. externalURL must not have a trailing slash.
// db is the SQLite database used for token persistence.
// It starts a background purge goroutine for expired codes and tokens.
func NewOAuthServer(password, externalURL string, tokenTTL time.Duration, db *DB) *OAuthServer {
	o := &OAuthServer{
		password:    password,
		tokenTTL:    tokenTTL,
		externalURL: strings.TrimRight(externalURL, "/"),
		db:          db,
		codes:       make(map[string]pendingCode),
		logins:      newLoginGuard(),
	}
	go o.purgeExpiredLoop()
	return o
}

// purgeExpiredLoop removes expired authorization codes and tokens every 10 minutes.
func (o *OAuthServer) purgeExpiredLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		o.mu.Lock()
		for code, pending := range o.codes {
			if now.After(pending.expiry) {
				delete(o.codes, code)
			}
		}
		o.mu.Unlock()
		if n, err := o.db.PurgeExpiredOAuthTokens(); err != nil {
			log.Printf("oauth: purge error: %v", err)
		} else if n > 0 {
			log.Printf("oauth: purged %d expired token(s)", n)
		}
	}
}

// ValidateBearer returns true if the Authorization header carries a valid, non-expired Bearer token.
func (o *OAuthServer) ValidateBearer(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	if token == "" {
		return false
	}
	return o.db.ValidateOAuthToken(token)
}

// HandleProtectedResource serves GET /.well-known/oauth-protected-resource.
// This tells clients which authorization server protects the resource.
func (o *OAuthServer) HandleProtectedResource(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"resource":              o.externalURL,
		"authorization_servers": []string{o.externalURL},
	})
}

// HandleAuthServerMeta serves GET /.well-known/oauth-authorization-server.
// This is the OAuth 2.0 Authorization Server Metadata (RFC 8414).
func (o *OAuthServer) HandleAuthServerMeta(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer":                                o.externalURL,
		"authorization_endpoint":                o.externalURL + "/oauth/authorize",
		"token_endpoint":                        o.externalURL + "/oauth/token",
		"registration_endpoint":                 o.externalURL + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"code_challenge_methods_supported":      []string{"S256"},
	})
}

// HandleRegister serves POST /oauth/register — Dynamic Client Registration (RFC 7591).
// Any client can register; the returned client_id is accepted by the authorize and token endpoints.
func (o *OAuthServer) HandleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RedirectURIs            []string `json:"redirect_uris"`
		ClientName              string   `json:"client_name"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		oauthRegisterError(w, "invalid_client_metadata", "cannot parse request body")
		return
	}
	if err := validateRedirectURIs(req.RedirectURIs); err != nil {
		oauthRegisterError(w, "invalid_redirect_uri", err.Error())
		return
	}

	clientID := "gypsum-" + oauthGenerateToken()[:16]
	if err := o.db.SaveOAuthClient(clientID, req.RedirectURIs); err != nil {
		log.Printf("oauth: failed to store client: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "server_error"})
		return
	}

	if len(req.GrantTypes) == 0 {
		req.GrantTypes = []string{"authorization_code"}
	}
	if len(req.ResponseTypes) == 0 {
		req.ResponseTypes = []string{"code"}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"client_id":                  clientID,
		"client_name":               req.ClientName,
		"redirect_uris":             req.RedirectURIs,
		"grant_types":               req.GrantTypes,
		"response_types":            req.ResponseTypes,
		"token_endpoint_auth_method": "none",
	})
}

func oauthRegisterError(w http.ResponseWriter, errCode, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             errCode,
		"error_description": description,
	})
}

// maxRedirectURIs and maxRedirectURILen bound what one client may register.
const (
	maxRedirectURIs   = 10
	maxRedirectURILen = 2048
)

// validateRedirectURIs checks the redirect URIs of a client registration:
// absolute URLs without a fragment, and plain http only towards loopback.
func validateRedirectURIs(uris []string) error {
	if len(uris) == 0 {
		return errors.New("redirect_uris is required")
	}
	if len(uris) > maxRedirectURIs {
		return fmt.Errorf("at most %d redirect_uris are allowed", maxRedirectURIs)
	}
	for _, raw := range uris {
		u, err := url.Parse(raw)
		if err != nil || len(raw) > maxRedirectURILen || u.Scheme == "" || u.Fragment != "" {
			return fmt.Errorf("invalid redirect uri %q", raw)
		}
		switch strings.ToLower(u.Scheme) {
		case "javascript", "data", "vbscript", "file", "blob":
			return fmt.Errorf("redirect uri scheme %q is not allowed", u.Scheme)
		case "http":
			if !isLoopbackHost(u.Hostname()) {
				return fmt.Errorf("redirect uri %q must use https", raw)
			}
		}
	}
	return nil
}

// isLoopbackHost reports whether host is localhost or a loopback address.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// redirectAllowed reports whether redirectURI is one the client registered.
// The match is exact, except that the port of a loopback http URI may differ:
// native apps listen on a new port for every login (RFC 8252, section 7.3).
func (o *OAuthServer) redirectAllowed(clientID, redirectURI string) bool {
	registered, ok := o.db.OAuthClientRedirectURIs(clientID)
	if !ok || redirectURI == "" {
		return false
	}
	u, err := url.Parse(redirectURI)
	if err != nil {
		return false
	}
	for _, reg := range registered {
		if reg == redirectURI {
			return true
		}
		ru, err := url.Parse(reg)
		if err != nil || ru.Scheme != "http" || u.Scheme != "http" || !isLoopbackHost(ru.Hostname()) {
			continue
		}
		if strings.EqualFold(ru.Hostname(), u.Hostname()) && ru.Path == u.Path &&
			ru.RawQuery == u.RawQuery && u.Fragment == "" && u.User == nil {
			return true
		}
	}
	return false
}

// redirectRefusal is shown instead of the login form when the client or its
// redirect URI is not registered. The browser is never sent to that URI.
const redirectRefusal = "This application is not registered with this wiki, or it asked to send you to an address it did not register. " +
	"Remove the wiki connection in the application and add it again."

// redirectHost is the part of a redirect URI the login form shows the user.
func redirectHost(redirectURI string) string {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return redirectURI
	}
	if u.Host == "" {
		return u.Scheme + ":"
	}
	return u.Host
}

// HandleAuthorize serves GET and POST /oauth/authorize.
// GET renders the login form; POST validates the password and issues an auth code.
func (o *OAuthServer) HandleAuthorize(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		if q.Get("response_type") != "code" {
			http.Error(w, "unsupported response_type", http.StatusBadRequest)
			return
		}
		if q.Get("code_challenge_method") != "S256" {
			http.Error(w, "only S256 code_challenge_method is supported", http.StatusBadRequest)
			return
		}
		if q.Get("code_challenge") == "" {
			http.Error(w, "missing code_challenge", http.StatusBadRequest)
			return
		}
		if !o.redirectAllowed(q.Get("client_id"), q.Get("redirect_uri")) {
			http.Error(w, redirectRefusal, http.StatusBadRequest)
			return
		}
		o.serveLoginForm(w, http.StatusOK, loginFormData{
			ClientID:            q.Get("client_id"),
			RedirectURI:         q.Get("redirect_uri"),
			CodeChallenge:       q.Get("code_challenge"),
			CodeChallengeMethod: q.Get("code_challenge_method"),
			State:               q.Get("state"),
		})
	case http.MethodPost:
		o.processLogin(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// HandleToken serves POST /oauth/token — exchanges an authorization code for an access token.
func (o *OAuthServer) HandleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthTokenError(w, "invalid_request", "cannot parse form body")
		return
	}

	if r.FormValue("grant_type") != "authorization_code" {
		oauthTokenError(w, "unsupported_grant_type", "only authorization_code is supported")
		return
	}

	code := r.FormValue("code")
	verifier := r.FormValue("code_verifier")
	redirectURI := r.FormValue("redirect_uri")

	if code == "" || verifier == "" {
		oauthTokenError(w, "invalid_request", "missing code or code_verifier")
		return
	}
	o.mu.Lock()
	pending, ok := o.codes[code]
	if ok {
		delete(o.codes, code) // single-use
	}
	o.mu.Unlock()

	if !ok || time.Now().After(pending.expiry) {
		oauthTokenError(w, "invalid_grant", "code not found or expired")
		return
	}
	if redirectURI != "" && redirectURI != pending.redirectURI {
		oauthTokenError(w, "invalid_grant", "redirect_uri mismatch")
		return
	}
	if !verifyPKCE(verifier, pending.codeChallenge) {
		oauthTokenError(w, "invalid_grant", "PKCE verification failed")
		return
	}

	token := oauthGenerateToken()
	if err := o.db.SaveOAuthToken(token, time.Now().Add(o.tokenTTL)); err != nil {
		log.Printf("oauth: failed to persist token: %v", err)
		oauthTokenError(w, "server_error", "failed to issue token")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int(o.tokenTTL.Seconds()),
	})
}

func (o *OAuthServer) serveLoginForm(w http.ResponseWriter, status int, data loginFormData) {
	data.RedirectHost = redirectHost(data.RedirectURI)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = oauthLoginTmpl.Execute(w, data)
}

func (o *OAuthServer) processLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form body", http.StatusBadRequest)
		return
	}

	clientID := r.FormValue("client_id")
	redirectURI := r.FormValue("redirect_uri")
	codeChallenge := r.FormValue("code_challenge")
	codeChallengeMethod := r.FormValue("code_challenge_method")
	state := r.FormValue("state")
	password := r.FormValue("password")

	if codeChallenge == "" {
		http.Error(w, "missing code_challenge", http.StatusBadRequest)
		return
	}

	if !o.redirectAllowed(clientID, redirectURI) {
		http.Error(w, redirectRefusal, http.StatusBadRequest)
		return
	}

	form := loginFormData{
		ClientID:            clientID,
		RedirectURI:         redirectURI,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
		State:               state,
	}
	addr := o.proxies.ClientKey(r)
	if wait := o.logins.Locked(addr); wait > 0 {
		form.Error = fmt.Sprintf("Too many wrong passwords. Try again in %d minutes.", int(wait.Minutes())+1)
		o.serveLoginForm(w, http.StatusTooManyRequests, form)
		return
	}
	if subtle.ConstantTimeCompare([]byte(password), []byte(o.password)) != 1 {
		if o.logins.Fail(addr) {
			log.Printf("oauth: %s locked out after repeated wrong passwords", addr)
		}
		form.Error = "Incorrect password."
		o.serveLoginForm(w, http.StatusUnauthorized, form)
		return
	}
	o.logins.Reset(addr)
	o.db.TouchOAuthClient(clientID)

	// Issue authorization code
	code := oauthGenerateToken()
	o.mu.Lock()
	o.codes[code] = pendingCode{
		redirectURI:   redirectURI,
		codeChallenge: codeChallenge,
		expiry:        time.Now().Add(10 * time.Minute),
	}
	o.mu.Unlock()

	// Redirect to redirect_uri with code (and state) properly encoded.
	u, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	q := u.Query()
	q.Set("code", code)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// verifyPKCE checks that SHA256(verifier) == challenge (base64url, no padding).
func verifyPKCE(verifier, challenge string) bool {
	h := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(h[:])
	return computed == challenge
}

// oauthGenerateToken generates a cryptographically random 32-byte hex token.
func oauthGenerateToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func oauthTokenError(w http.ResponseWriter, errCode, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             errCode,
		"error_description": description,
	})
}

var oauthLoginTmpl = template.Must(template.New("oauth-login").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Gypsum — Authorize</title>
  <style>
    :root { color-scheme: dark light; }
    body {
      font-family: system-ui, -apple-system, sans-serif;
      max-width: 380px;
      margin: 80px auto;
      padding: 0 1.5rem;
    }
    h1 { font-size: 1.3rem; margin-bottom: 0.25rem; }
    .sub { color: #888; margin-top: 0; font-size: 0.9rem; }
    .error {
      background: #fee;
      color: #c0392b;
      border: 1px solid #f5c6cb;
      border-radius: 4px;
      padding: 0.5rem 0.75rem;
      margin-bottom: 1rem;
      font-size: 0.9rem;
    }
    label { display: block; margin-bottom: 0.4rem; font-weight: 500; font-size: 0.95rem; }
    input[type=password] {
      width: 100%;
      box-sizing: border-box;
      padding: 0.55rem 0.75rem;
      font-size: 1rem;
      border: 1px solid #ccc;
      border-radius: 4px;
    }
    button {
      margin-top: 1rem;
      width: 100%;
      padding: 0.6rem;
      font-size: 1rem;
      cursor: pointer;
      border-radius: 4px;
    }
  </style>
</head>
<body>
  <h1>Authorize Gypsum</h1>
  <p class="sub">An application ({{.ClientID}}) is requesting access to your wiki.</p>
  <p class="sub">After you sign in you are sent to <strong>{{.RedirectHost}}</strong>. Continue only if you started this yourself and know that address.</p>
  {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
  <form method="POST">
    <input type="hidden" name="client_id"             value="{{.ClientID}}">
    <input type="hidden" name="redirect_uri"          value="{{.RedirectURI}}">
    <input type="hidden" name="code_challenge"        value="{{.CodeChallenge}}">
    <input type="hidden" name="code_challenge_method" value="{{.CodeChallengeMethod}}">
    <input type="hidden" name="state"                 value="{{.State}}">
    <label for="password">Wiki password</label>
    <input type="password" id="password" name="password" autofocus required placeholder="Enter your wiki password">
    <button type="submit">Authorize</button>
  </form>
</body>
</html>`))
