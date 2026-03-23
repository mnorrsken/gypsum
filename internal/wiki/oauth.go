package wiki

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"net/http"
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
//	GYPSUM_OAUTH_CLIENT_ID  — expected client_id (default: "claude")
//	GYPSUM_OAUTH_TOKEN_TTL  — access token lifetime as Go duration (default: "24h")
type OAuthServer struct {
	clientID    string
	password    string
	tokenTTL    time.Duration
	externalURL string // no trailing slash

	mu      sync.Mutex
	codes   map[string]pendingCode
	tokens  map[string]time.Time
	clients map[string]bool // dynamically registered client_ids
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
}

// NewOAuthServer creates an OAuthServer. externalURL must not have a trailing slash.
// It starts a background goroutine to periodically purge expired codes and tokens.
func NewOAuthServer(clientID, password, externalURL string, tokenTTL time.Duration) *OAuthServer {
	o := &OAuthServer{
		clientID:    clientID,
		password:    password,
		tokenTTL:    tokenTTL,
		externalURL: strings.TrimRight(externalURL, "/"),
		codes:       make(map[string]pendingCode),
		tokens:      make(map[string]time.Time),
		clients:     make(map[string]bool),
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
		for token, expiry := range o.tokens {
			if now.After(expiry) {
				delete(o.tokens, token)
			}
		}
		o.mu.Unlock()
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
	o.mu.Lock()
	defer o.mu.Unlock()
	expiry, ok := o.tokens[token]
	if !ok {
		return false
	}
	if time.Now().After(expiry) {
		delete(o.tokens, token)
		return false
	}
	return true
}

// HandleProtectedResource serves GET /.well-known/oauth-protected-resource.
// This tells clients which authorization server protects the resource.
func (o *OAuthServer) HandleProtectedResource(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"resource":              o.externalURL + "/mcp/external",
		"authorization_servers": []string{o.externalURL},
	})
}

// HandleAuthServerMeta serves GET /.well-known/oauth-authorization-server.
// This is the OAuth 2.0 Authorization Server Metadata (RFC 8414).
func (o *OAuthServer) HandleAuthServerMeta(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer":                           o.externalURL,
		"authorization_endpoint":           o.externalURL + "/oauth/authorize",
		"token_endpoint":                   o.externalURL + "/oauth/token",
		"registration_endpoint":            o.externalURL + "/oauth/register",
		"response_types_supported":         []string{"code"},
		"grant_types_supported":            []string{"authorization_code"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"code_challenge_methods_supported": []string{"S256"},
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":             "invalid_client_metadata",
			"error_description": "cannot parse request body",
		})
		return
	}

	clientID := "gypsum-" + oauthGenerateToken()[:16]

	o.mu.Lock()
	o.clients[clientID] = true
	o.mu.Unlock()

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

// validClientID returns true if cid matches the static client_id or a dynamically registered one.
func (o *OAuthServer) validClientID(cid string) bool {
	if cid == o.clientID {
		return true
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.clients[cid]
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
		if !o.validClientID(q.Get("client_id")) {
			http.Error(w, "unknown client_id", http.StatusBadRequest)
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
		o.serveLoginForm(w, loginFormData{
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
	clientID := r.FormValue("client_id")
	redirectURI := r.FormValue("redirect_uri")

	if code == "" || verifier == "" {
		oauthTokenError(w, "invalid_request", "missing code or code_verifier")
		return
	}
	if clientID != "" && !o.validClientID(clientID) {
		oauthTokenError(w, "invalid_client", "unknown client_id")
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
	o.mu.Lock()
	o.tokens[token] = time.Now().Add(o.tokenTTL)
	o.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int(o.tokenTTL.Seconds()),
	})
}

func (o *OAuthServer) serveLoginForm(w http.ResponseWriter, data loginFormData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if data.Error != "" {
		w.WriteHeader(http.StatusUnauthorized)
	}
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

	if !o.validClientID(clientID) {
		http.Error(w, "unknown client_id", http.StatusBadRequest)
		return
	}
	if codeChallenge == "" {
		http.Error(w, "missing code_challenge", http.StatusBadRequest)
		return
	}

	if subtle.ConstantTimeCompare([]byte(password), []byte(o.password)) != 1 {
		o.serveLoginForm(w, loginFormData{
			ClientID:            clientID,
			RedirectURI:         redirectURI,
			CodeChallenge:       codeChallenge,
			CodeChallengeMethod: codeChallengeMethod,
			State:               state,
			Error:               "Incorrect password.",
		})
		return
	}

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
