package wiki

import (
	"errors"
	"testing"
)

func TestSecureStoreSaveLoadAndWrongPassword(t *testing.T) {
	dir := t.TempDir()
	store := NewSecureStore(dir)

	pageSlug := "Home"
	blockID := "ops_notes"
	password := "correct horse battery staple"
	plaintext := "top secret text"

	if err := store.Save(pageSlug, blockID, password, plaintext); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if !store.Exists(pageSlug, blockID) {
		t.Fatalf("expected secure block to exist")
	}

	got, err := store.Load(pageSlug, blockID, password)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if got != plaintext {
		t.Fatalf("unexpected plaintext: got %q want %q", got, plaintext)
	}

	_, err = store.Load(pageSlug, blockID, "wrong password")
	if !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("expected ErrWrongPassword, got %v", err)
	}
}

func TestSecureStoreMissingBlockReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	store := NewSecureStore(dir)

	got, err := store.Load("Home", "missing", "pw")
	if err != nil {
		t.Fatalf("unexpected error loading missing block: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty content for missing block, got %q", got)
	}
}
