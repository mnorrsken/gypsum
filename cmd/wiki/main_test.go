package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSeedPagesIfEmptySeedsDefaults(t *testing.T) {
	pagesDir := t.TempDir()

	if err := seedPagesIfEmpty(pagesDir); err != nil {
		t.Fatalf("seedPagesIfEmpty failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(pagesDir, "Home.md")); err != nil {
		t.Fatalf("expected Home.md to be seeded: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pagesDir, "Scratch_Pad.md")); err != nil {
		t.Fatalf("expected Scratch_Pad.md to be seeded: %v", err)
	}
}

func TestSeedPagesIfEmptyDoesNothingWhenPagesExist(t *testing.T) {
	pagesDir := t.TempDir()
	existing := filepath.Join(pagesDir, "Existing.md")
	if err := os.WriteFile(existing, []byte("# Existing"), 0o644); err != nil {
		t.Fatalf("failed writing existing page: %v", err)
	}

	if err := seedPagesIfEmpty(pagesDir); err != nil {
		t.Fatalf("seedPagesIfEmpty failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(pagesDir, "Home.md")); !os.IsNotExist(err) {
		t.Fatalf("expected Home.md to not be seeded when pages already exist")
	}
}

func TestSecurityHeaders(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	securityHeaders(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "SAMEORIGIN",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}
