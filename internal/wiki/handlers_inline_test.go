package wiki

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestInlineSecureSaveThenUnlock(t *testing.T) {
	handler := NewHandler(
		NewPageStore(t.TempDir()),
		NewSecureStore(t.TempDir()),
		NewMarkdownRenderer(),
		t.TempDir(),
	).Routes()

	saveForm := url.Values{
		"page_slug": {"Home"},
		"block_id":  {"ops_notes"},
		"password":  {"secret-pass"},
		"content":   {"sensitive plaintext"},
	}
	saveReq := httptest.NewRequest(http.MethodPost, "/secure-inline/save", strings.NewReader(saveForm.Encode()))
	saveReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	saveRec := httptest.NewRecorder()
	handler.ServeHTTP(saveRec, saveReq)
	if saveRec.Code != http.StatusOK {
		t.Fatalf("save status = %d, want %d", saveRec.Code, http.StatusOK)
	}

	var savePayload map[string]any
	if err := json.Unmarshal(saveRec.Body.Bytes(), &savePayload); err != nil {
		t.Fatalf("failed to decode save payload: %v", err)
	}
	if ok, _ := savePayload["ok"].(bool); !ok {
		t.Fatalf("save payload not ok: %#v", savePayload)
	}

	unlockForm := url.Values{
		"page_slug": {"Home"},
		"block_id":  {"ops_notes"},
		"password":  {"secret-pass"},
	}
	unlockReq := httptest.NewRequest(http.MethodPost, "/secure-inline/unlock", strings.NewReader(unlockForm.Encode()))
	unlockReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	unlockRec := httptest.NewRecorder()
	handler.ServeHTTP(unlockRec, unlockReq)
	if unlockRec.Code != http.StatusOK {
		t.Fatalf("unlock status = %d, want %d", unlockRec.Code, http.StatusOK)
	}

	var unlockPayload map[string]any
	if err := json.Unmarshal(unlockRec.Body.Bytes(), &unlockPayload); err != nil {
		t.Fatalf("failed to decode unlock payload: %v", err)
	}
	if got, _ := unlockPayload["content"].(string); got != "sensitive plaintext" {
		t.Fatalf("unexpected unlocked content: %q", got)
	}
}

func TestInlineSecureUnlockWrongPassword(t *testing.T) {
	handler := NewHandler(
		NewPageStore(t.TempDir()),
		NewSecureStore(t.TempDir()),
		NewMarkdownRenderer(),
		t.TempDir(),
	).Routes()

	saveForm := url.Values{
		"page_slug": {"Home"},
		"block_id":  {"ops_notes"},
		"password":  {"correct"},
		"content":   {"secret"},
	}
	saveReq := httptest.NewRequest(http.MethodPost, "/secure-inline/save", strings.NewReader(saveForm.Encode()))
	saveReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	saveRec := httptest.NewRecorder()
	handler.ServeHTTP(saveRec, saveReq)
	if saveRec.Code != http.StatusOK {
		t.Fatalf("save status = %d, want %d", saveRec.Code, http.StatusOK)
	}

	unlockForm := url.Values{
		"page_slug": {"Home"},
		"block_id":  {"ops_notes"},
		"password":  {"wrong"},
	}
	unlockReq := httptest.NewRequest(http.MethodPost, "/secure-inline/unlock", strings.NewReader(unlockForm.Encode()))
	unlockReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	unlockRec := httptest.NewRecorder()
	handler.ServeHTTP(unlockRec, unlockReq)
	if unlockRec.Code != http.StatusUnauthorized {
		t.Fatalf("unlock status = %d, want %d", unlockRec.Code, http.StatusUnauthorized)
	}

	var unlockPayload map[string]any
	if err := json.Unmarshal(unlockRec.Body.Bytes(), &unlockPayload); err != nil {
		t.Fatalf("failed to decode unlock payload: %v", err)
	}
	if ok, _ := unlockPayload["ok"].(bool); ok {
		t.Fatalf("expected payload ok=false, got %#v", unlockPayload)
	}
}
