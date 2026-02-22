package wiki

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestInlineSecureUnlock(t *testing.T) {
	crypto := NewServerCrypto("test-key")
	handler := NewHandler(
		NewPageStore(t.TempDir()),
		crypto,
		NewMarkdownRenderer(),
		t.TempDir(),
		nil,
	).Routes()

	// Encrypt some content
	ciphertext, err := crypto.Encrypt("sensitive plaintext")
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	unlockForm := url.Values{
		"ciphertext": {ciphertext},
	}
	unlockReq := httptest.NewRequest(http.MethodPost, "/secure-inline/unlock", strings.NewReader(unlockForm.Encode()))
	unlockReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	unlockRec := httptest.NewRecorder()
	handler.ServeHTTP(unlockRec, unlockReq)
	if unlockRec.Code != http.StatusOK {
		t.Fatalf("unlock status = %d, want %d; body: %s", unlockRec.Code, http.StatusOK, unlockRec.Body.String())
	}

	var unlockPayload map[string]any
	if err := json.Unmarshal(unlockRec.Body.Bytes(), &unlockPayload); err != nil {
		t.Fatalf("failed to decode unlock payload: %v", err)
	}
	if got, _ := unlockPayload["content"].(string); got != "sensitive plaintext" {
		t.Fatalf("unexpected unlocked content: %q", got)
	}
}

func TestInlineSecureUnlockBadCiphertext(t *testing.T) {
	handler := NewHandler(
		NewPageStore(t.TempDir()),
		NewServerCrypto("test-key"),
		NewMarkdownRenderer(),
		t.TempDir(),
		nil,
	).Routes()

	unlockForm := url.Values{
		"ciphertext": {"not-valid-base64-ciphertext"},
	}
	unlockReq := httptest.NewRequest(http.MethodPost, "/secure-inline/unlock", strings.NewReader(unlockForm.Encode()))
	unlockReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	unlockRec := httptest.NewRecorder()
	handler.ServeHTTP(unlockRec, unlockReq)
	if unlockRec.Code != http.StatusBadRequest {
		t.Fatalf("unlock status = %d, want %d", unlockRec.Code, http.StatusBadRequest)
	}

	var unlockPayload map[string]any
	if err := json.Unmarshal(unlockRec.Body.Bytes(), &unlockPayload); err != nil {
		t.Fatalf("failed to decode unlock payload: %v", err)
	}
	if ok, _ := unlockPayload["ok"].(bool); ok {
		t.Fatalf("expected payload ok=false, got %#v", unlockPayload)
	}
}
