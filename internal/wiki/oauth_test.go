package wiki

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// newTestOAuthServer creates an OAuthServer backed by a temp SQLite DB.
func newTestOAuthServer(t *testing.T, password string) *OAuthServer {
	t.Helper()
	db := openTestDB(t)
	return NewOAuthServer(password, "https://wiki.example.com", time.Hour, db)
}

// --- ValidateBearer ---

func TestValidateBearerValid(t *testing.T) {
	db := openTestDB(t)
	o := NewOAuthServer("pass", "https://wiki.example.com", time.Hour, db)

	db.SaveOAuthToken("good-token", time.Now().Add(time.Hour))

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer good-token")
	if !o.ValidateBearer(r) {
		t.Fatal("expected valid bearer")
	}
}

func TestValidateBearerExpired(t *testing.T) {
	db := openTestDB(t)
	o := NewOAuthServer("pass", "https://wiki.example.com", time.Hour, db)

	db.SaveOAuthToken("old-token", time.Now().Add(-time.Hour))

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer old-token")
	if o.ValidateBearer(r) {
		t.Fatal("expected expired bearer to be invalid")
	}
}

func TestValidateBearerMissing(t *testing.T) {
	o := newTestOAuthServer(t, "pass")

	r := httptest.NewRequest("GET", "/", nil)
	if o.ValidateBearer(r) {
		t.Fatal("expected false for missing header")
	}
}

func TestValidateBearerEmptyToken(t *testing.T) {
	o := newTestOAuthServer(t, "pass")

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer ")
	if o.ValidateBearer(r) {
		t.Fatal("expected false for empty token")
	}
}

func TestValidateBearerWrongScheme(t *testing.T) {
	o := newTestOAuthServer(t, "pass")

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	if o.ValidateBearer(r) {
		t.Fatal("expected false for non-Bearer scheme")
	}
}

// --- HandleProtectedResource ---

func TestHandleProtectedResource(t *testing.T) {
	o := newTestOAuthServer(t, "pass")

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/.well-known/oauth-protected-resource", nil)
	o.HandleProtectedResource(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["resource"] != "https://wiki.example.com" {
		t.Fatalf("unexpected resource: %v", body["resource"])
	}
}

// --- HandleAuthServerMeta ---

func TestHandleAuthServerMeta(t *testing.T) {
	o := newTestOAuthServer(t, "pass")

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/.well-known/oauth-authorization-server", nil)
	o.HandleAuthServerMeta(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["issuer"] != "https://wiki.example.com" {
		t.Fatalf("unexpected issuer: %v", body["issuer"])
	}
	if body["authorization_endpoint"] != "https://wiki.example.com/oauth/authorize" {
		t.Fatalf("unexpected authorization_endpoint: %v", body["authorization_endpoint"])
	}
}

// --- HandleRegister ---

func TestHandleRegister(t *testing.T) {
	o := newTestOAuthServer(t, "pass")

	reqBody := `{"redirect_uris":["http://localhost/callback"],"client_name":"test-client"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/oauth/register", strings.NewReader(reqBody))
	r.Header.Set("Content-Type", "application/json")
	o.HandleRegister(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	clientID, ok := body["client_id"].(string)
	if !ok || !strings.HasPrefix(clientID, "gypsum-") {
		t.Fatalf("unexpected client_id: %v", body["client_id"])
	}
	if body["client_name"] != "test-client" {
		t.Fatalf("unexpected client_name: %v", body["client_name"])
	}
}

func TestHandleRegisterInvalidBody(t *testing.T) {
	o := newTestOAuthServer(t, "pass")

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/oauth/register", strings.NewReader("not json"))
	r.Header.Set("Content-Type", "application/json")
	o.HandleRegister(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// --- HandleAuthorize ---

func TestHandleAuthorizeGETRendersForm(t *testing.T) {
	o := newTestOAuthServer(t, "pass")

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/oauth/authorize?response_type=code&code_challenge=abc&code_challenge_method=S256&client_id=test&redirect_uri=http://localhost/cb&state=xyz", nil)
	o.HandleAuthorize(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Wiki password") {
		t.Fatal("expected login form in response body")
	}
}

func TestHandleAuthorizeGETMissingChallenge(t *testing.T) {
	o := newTestOAuthServer(t, "pass")

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/oauth/authorize?response_type=code&code_challenge_method=S256", nil)
	o.HandleAuthorize(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleAuthorizeGETBadResponseType(t *testing.T) {
	o := newTestOAuthServer(t, "pass")

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/oauth/authorize?response_type=token&code_challenge=abc&code_challenge_method=S256", nil)
	o.HandleAuthorize(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleAuthorizeGETBadChallengeMethod(t *testing.T) {
	o := newTestOAuthServer(t, "pass")

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/oauth/authorize?response_type=code&code_challenge=abc&code_challenge_method=plain", nil)
	o.HandleAuthorize(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleAuthorizePOSTWrongPassword(t *testing.T) {
	o := newTestOAuthServer(t, "correct-password")

	form := url.Values{
		"client_id":             {"test"},
		"redirect_uri":         {"http://localhost/cb"},
		"code_challenge":       {"abc"},
		"code_challenge_method": {"S256"},
		"state":                {"xyz"},
		"password":             {"wrong-password"},
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/oauth/authorize", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	o.HandleAuthorize(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Incorrect password") {
		t.Fatal("expected error message in response")
	}
}

func TestHandleAuthorizePOSTCorrectPassword(t *testing.T) {
	o := newTestOAuthServer(t, "correct-password")

	form := url.Values{
		"client_id":             {"test"},
		"redirect_uri":         {"http://localhost/cb"},
		"code_challenge":       {"abc"},
		"code_challenge_method": {"S256"},
		"state":                {"xyz"},
		"password":             {"correct-password"},
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/oauth/authorize", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	o.HandleAuthorize(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	loc := w.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	if u.Query().Get("code") == "" {
		t.Fatal("expected code in redirect URL")
	}
	if u.Query().Get("state") != "xyz" {
		t.Fatalf("state = %q, want xyz", u.Query().Get("state"))
	}
}

func TestHandleAuthorizeMethodNotAllowed(t *testing.T) {
	o := newTestOAuthServer(t, "pass")

	w := httptest.NewRecorder()
	r := httptest.NewRequest("DELETE", "/oauth/authorize", nil)
	o.HandleAuthorize(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}

// --- HandleToken ---

// pkceChallenge computes the S256 PKCE challenge from a verifier string.
func pkceChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func TestHandleTokenFullFlow(t *testing.T) {
	o := newTestOAuthServer(t, "pass")

	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challenge := pkceChallenge(verifier)

	// Step 1: Login to get an authorization code.
	form := url.Values{
		"client_id":             {"test"},
		"redirect_uri":         {"http://localhost/cb"},
		"code_challenge":       {challenge},
		"code_challenge_method": {"S256"},
		"password":             {"pass"},
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/oauth/authorize", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	o.HandleAuthorize(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("authorize: status = %d, want 302", w.Code)
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatal("authorize: no code in redirect")
	}

	// Step 2: Exchange code for token.
	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {"http://localhost/cb"},
	}
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(tokenForm.Encode()))
	r2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	o.HandleToken(w2, r2)

	if w2.Code != http.StatusOK {
		t.Fatalf("token: status = %d, want 200, body: %s", w2.Code, w2.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w2.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	accessToken, ok := body["access_token"].(string)
	if !ok || accessToken == "" {
		t.Fatal("expected access_token in response")
	}
	if body["token_type"] != "Bearer" {
		t.Fatalf("token_type = %v, want Bearer", body["token_type"])
	}

	// Step 3: Validate the issued token.
	r3 := httptest.NewRequest("GET", "/", nil)
	r3.Header.Set("Authorization", "Bearer "+accessToken)
	if !o.ValidateBearer(r3) {
		t.Fatal("issued token should be valid")
	}
}

func TestHandleTokenCodeIsOneTime(t *testing.T) {
	o := newTestOAuthServer(t, "pass")

	verifier := "test-verifier-string"
	challenge := pkceChallenge(verifier)

	// Get a code.
	form := url.Values{
		"client_id":             {"test"},
		"redirect_uri":         {"http://localhost/cb"},
		"code_challenge":       {challenge},
		"code_challenge_method": {"S256"},
		"password":             {"pass"},
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/oauth/authorize", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	o.HandleAuthorize(w, r)

	loc, _ := url.Parse(w.Header().Get("Location"))
	code := loc.Query().Get("code")

	// Exchange successfully.
	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
	}
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(tokenForm.Encode()))
	r2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	o.HandleToken(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("first exchange: status = %d", w2.Code)
	}

	// Second use of the same code should fail.
	w3 := httptest.NewRecorder()
	r3 := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(tokenForm.Encode()))
	r3.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	o.HandleToken(w3, r3)
	if w3.Code != http.StatusBadRequest {
		t.Fatalf("second exchange: status = %d, want 400", w3.Code)
	}
}

func TestHandleTokenBadPKCE(t *testing.T) {
	o := newTestOAuthServer(t, "pass")

	verifier := "correct-verifier"
	challenge := pkceChallenge(verifier)

	form := url.Values{
		"client_id":             {"test"},
		"redirect_uri":         {"http://localhost/cb"},
		"code_challenge":       {challenge},
		"code_challenge_method": {"S256"},
		"password":             {"pass"},
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/oauth/authorize", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	o.HandleAuthorize(w, r)

	loc, _ := url.Parse(w.Header().Get("Location"))
	code := loc.Query().Get("code")

	// Try to exchange with the wrong verifier.
	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {"wrong-verifier"},
	}
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(tokenForm.Encode()))
	r2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	o.HandleToken(w2, r2)

	if w2.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w2.Code)
	}
	var body map[string]string
	json.NewDecoder(w2.Body).Decode(&body)
	if body["error"] != "invalid_grant" {
		t.Fatalf("error = %q, want invalid_grant", body["error"])
	}
}

func TestHandleTokenBadGrantType(t *testing.T) {
	o := newTestOAuthServer(t, "pass")

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"code":          {"anything"},
		"code_verifier": {"anything"},
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	o.HandleToken(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	var body map[string]string
	json.NewDecoder(w.Body).Decode(&body)
	if body["error"] != "unsupported_grant_type" {
		t.Fatalf("error = %q, want unsupported_grant_type", body["error"])
	}
}

func TestHandleTokenMissingCode(t *testing.T) {
	o := newTestOAuthServer(t, "pass")

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code_verifier": {"something"},
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	o.HandleToken(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleTokenRedirectURIMismatch(t *testing.T) {
	o := newTestOAuthServer(t, "pass")

	verifier := "test-verifier"
	challenge := pkceChallenge(verifier)

	form := url.Values{
		"client_id":             {"test"},
		"redirect_uri":         {"http://localhost/cb"},
		"code_challenge":       {challenge},
		"code_challenge_method": {"S256"},
		"password":             {"pass"},
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/oauth/authorize", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	o.HandleAuthorize(w, r)

	loc, _ := url.Parse(w.Header().Get("Location"))
	code := loc.Query().Get("code")

	// Exchange with a different redirect_uri.
	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {"http://evil.example.com/cb"},
	}
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(tokenForm.Encode()))
	r2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	o.HandleToken(w2, r2)

	if w2.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w2.Code)
	}
}

// --- verifyPKCE ---

func TestVerifyPKCE(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challenge := pkceChallenge(verifier)

	if !verifyPKCE(verifier, challenge) {
		t.Fatal("expected valid PKCE")
	}
	if verifyPKCE("wrong-verifier", challenge) {
		t.Fatal("expected invalid PKCE for wrong verifier")
	}
}
