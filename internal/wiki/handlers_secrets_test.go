package wiki

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mnorrsken/gypsum/web"
)

// testMacro is a syntactically valid encrypted body. The handlers never
// decrypt, so a well-formed macro is all a test needs.
const testMacro = "{{secure_aes2:QUJDREVGRw==}}"

func TestSecretHTTPRoundTrip(t *testing.T) {
	h, handler := newTestHandler(t)

	rec := postForm(t, handler, "/secrets/create", url.Values{
		"title":       {"Big Secret thing"},
		"secret":      {testMacro},
		"url":         {"https://example.com"},
		"description": {"prod creds"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID       string `json:"id"`
		Mnemonic string `json:"mnemonic"`
		Color    int    `json:"color"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if !ValidSecretID(created.ID) {
		t.Fatalf("create returned invalid id %q", created.ID)
	}
	if created.Mnemonic != "BS" || created.Color != SecretColor("Big Secret thing") {
		t.Errorf("derived fields = %q / %d", created.Mnemonic, created.Color)
	}

	stored, err := h.store.LoadSecret(created.ID)
	if err != nil {
		t.Fatalf("LoadSecret: %v", err)
	}
	if stored.Ciphertext != "QUJDREVGRw==" || stored.Description != "prod creds" {
		t.Fatalf("stored = %+v", stored)
	}

	// Save with an empty secret keeps the stored ciphertext, so metadata can
	// be edited while the vault is locked.
	rec = postForm(t, handler, "/secrets/save/"+created.ID, url.Values{
		"title":  {"Renamed"},
		"secret": {""},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("save status = %d, body=%s", rec.Code, rec.Body.String())
	}
	stored, _ = h.store.LoadSecret(created.ID)
	if stored.Title != "Renamed" {
		t.Errorf("title after save = %q", stored.Title)
	}
	if stored.Ciphertext != "QUJDREVGRw==" {
		t.Errorf("ciphertext changed on metadata-only save: %q", stored.Ciphertext)
	}

	if rec := postForm(t, handler, "/secrets/delete/"+created.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rec.Code)
	}
	if _, err := h.store.LoadSecret(created.ID); err != ErrPageNotFound {
		t.Errorf("secret still present after delete: %v", err)
	}
}

// Plaintext must never reach disk: the browser holds the key, so a body that
// is not an encrypted macro is a bug and is rejected.
func TestSecretCreateRejectsPlaintext(t *testing.T) {
	h, handler := newTestHandler(t)

	for _, body := range []string{"hunter2", "", "{{secure:hunter2}}"} {
		rec := postForm(t, handler, "/secrets/create", url.Values{
			"title": {"Leaky"}, "secret": {body},
		})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("create with body %q status = %d, want 400", body, rec.Code)
		}
	}
	if secrets, _ := h.store.ListSecrets(); len(secrets) != 0 {
		t.Fatalf("rejected secrets were stored: %d", len(secrets))
	}
}

func TestSecretCreateRequiresTitle(t *testing.T) {
	_, handler := newTestHandler(t)
	rec := postForm(t, handler, "/secrets/create", url.Values{"secret": {testMacro}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSecretRejectsBadIDAndImageName(t *testing.T) {
	h, handler := newTestHandler(t)
	id, err := h.store.CreateSecret(SecretEntry{Title: "Existing"}, testMacro)
	if err != nil {
		t.Fatal(err)
	}

	// Path traversal in the id.
	for _, path := range []string{"/secrets/save/..%2F..%2Fetc", "/secrets/delete/nope", "/secrets/image/bad-id"} {
		if rec := postForm(t, handler, path, url.Values{"title": {"x"}, "secret": {testMacro}}); rec.Code < 400 {
			t.Errorf("POST %s status = %d, want an error", path, rec.Code)
		}
	}

	// Path traversal in the image field.
	rec := postForm(t, handler, "/secrets/save/"+id, url.Values{
		"title": {"Existing"}, "secret": {testMacro}, "image": {"../../etc/passwd"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("save with traversal image status = %d, want 400", rec.Code)
	}
}

func TestSecretSaveMissingSecret(t *testing.T) {
	_, handler := newTestHandler(t)
	rec := postForm(t, handler, "/secrets/save/20260824-120000", url.Values{
		"title": {"Ghost"}, "secret": {testMacro},
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestSecretImageFetch(t *testing.T) {
	store := NewPageStore(filepath.Join(t.TempDir(), "pages"))
	h := NewHandler(store, NewMarkdownRenderer(), nil, nil, nil, nil, AllMCPSections)
	handler := h.Routes()

	ts := siteImageServer(t, `<meta property="og:image" content="/og.png">`,
		map[string]string{"/og.png": "image/png"})
	h.SetSiteImageClient(ts.Client())

	id, err := store.CreateSecret(SecretEntry{Title: "Example", URL: ts.URL}, testMacro)
	if err != nil {
		t.Fatal(err)
	}

	rec := postForm(t, handler, "/secrets/image/"+id, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("image status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		OK    bool   `json:"ok"`
		Image string `json:"image"`
		URL   string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.OK || !strings.HasPrefix(resp.Image, "secret-"+id+"-") || !strings.HasSuffix(resp.Image, ".png") {
		t.Fatalf("unexpected response %+v", resp)
	}
	if resp.URL != "/images/"+resp.Image {
		t.Errorf("url = %q", resp.URL)
	}
	if _, err := os.Stat(filepath.Join(store.ImagesDir(), resp.Image)); err != nil {
		t.Errorf("image not written: %v", err)
	}

	// The fetched image is attached to the secret, and its ciphertext survives.
	stored, _ := store.LoadSecret(id)
	if stored.Image != resp.Image {
		t.Errorf("secret image = %q, want %q", stored.Image, resp.Image)
	}
	if stored.Ciphertext == "" {
		t.Error("ciphertext lost when attaching the image")
	}
}

// A site with no usable image is an ordinary outcome: the card keeps its
// mnemonic tile, so the request succeeds with ok:false.
func TestSecretImageFetchNoImage(t *testing.T) {
	store := NewPageStore(filepath.Join(t.TempDir(), "pages"))
	h := NewHandler(store, NewMarkdownRenderer(), nil, nil, nil, nil, AllMCPSections)
	handler := h.Routes()

	ts := siteImageServer(t, "", nil) // no og:image, no favicon
	h.SetSiteImageClient(ts.Client())

	id, _ := store.CreateSecret(SecretEntry{Title: "Bare", URL: ts.URL}, testMacro)
	rec := postForm(t, handler, "/secrets/image/"+id, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp struct {
		OK bool `json:"ok"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.OK {
		t.Error("expected ok:false when no image is available")
	}
	if stored, _ := store.LoadSecret(id); stored.Image != "" {
		t.Errorf("image set despite failure: %q", stored.Image)
	}
}

func TestSecretImageRequiresURL(t *testing.T) {
	h, handler := newTestHandler(t)
	id, _ := h.store.CreateSecret(SecretEntry{Title: "No link"}, testMacro)
	if rec := postForm(t, handler, "/secrets/image/"+id, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSecretsVaultRenders(t *testing.T) {
	store := NewPageStore(filepath.Join(t.TempDir(), "pages"))
	h := NewHandler(store, NewMarkdownRenderer(), web.Templates(), nil, nil, nil, AllMCPSections)
	handler := h.Routes()

	id, _ := store.CreateSecret(SecretEntry{
		Title: "Big Secret thing", URL: "https://example.com", Description: "prod creds",
	}, testMacro)

	req := httptest.NewRequest(http.MethodGet, "/secrets", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("vault status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-id="` + id + `"`,
		`data-ciphertext="QUJDREVGRw=="`,
		"secret-mnemonic",
		">BS<",
		"/static/secrets.js",
		"secret-filter",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("vault page missing %q", want)
		}
	}
	// A vault listing must not be cached.
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}
