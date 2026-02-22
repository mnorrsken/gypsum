package wiki

import (
	"strings"
	"testing"
)

func TestServerCryptoEncryptDecrypt(t *testing.T) {
	sc := NewServerCrypto("test-passphrase")
	plaintext := "top secret text"

	encoded, err := sc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	if encoded == "" {
		t.Fatal("expected non-empty ciphertext")
	}

	got, err := sc.Decrypt(encoded)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if got != plaintext {
		t.Fatalf("unexpected plaintext: got %q want %q", got, plaintext)
	}
}

func TestServerCryptoDecryptWrongKey(t *testing.T) {
	sc1 := NewServerCrypto("key-one")
	sc2 := NewServerCrypto("key-two")

	encoded, err := sc1.Encrypt("secret")
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	_, err = sc2.Decrypt(encoded)
	if err == nil {
		t.Fatal("expected error decrypting with wrong key")
	}
}

func TestDecryptForEditAndEncryptForSave(t *testing.T) {
	sc := NewServerCrypto("roundtrip-key")

	// Start with plaintext macros and encrypt
	original := "Hello {{plain:secret value}} world"
	encrypted, err := sc.EncryptForSave(original)
	if err != nil {
		t.Fatalf("EncryptForSave failed: %v", err)
	}

	if strings.Contains(encrypted, "secret value") {
		t.Fatal("encrypted markdown should not contain plaintext")
	}
	if !strings.Contains(encrypted, "{{secure:") {
		t.Fatal("encrypted markdown should contain {{secure:...}}")
	}

	// Now decrypt for editing
	decrypted := sc.DecryptForEdit(encrypted)
	if !strings.Contains(decrypted, "{{plain:secret value}}") {
		t.Fatalf("decrypted markdown should contain {{plain:secret value}}, got: %s", decrypted)
	}
	if !strings.Contains(decrypted, "Hello ") {
		t.Fatal("surrounding text should be preserved")
	}
}
