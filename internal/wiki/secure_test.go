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

func TestMultilineSecureMacroStripsLinebreaks(t *testing.T) {
	sc := NewServerCrypto("multiline-key")

	// Multiline block with leading/trailing newlines around content.
	original := "before\n{{plain:\nline1\nline2\n}}\nafter"
	encrypted, err := sc.EncryptForSave(original)
	if err != nil {
		t.Fatalf("EncryptForSave failed: %v", err)
	}

	if strings.Contains(encrypted, "line1") {
		t.Fatal("encrypted markdown should not contain plaintext")
	}

	// Decrypt the ciphertext directly to verify stored content has no
	// leading/trailing linebreaks.
	captures := secureMacroRe.FindStringSubmatch(encrypted)
	if len(captures) < 2 {
		t.Fatalf("expected {{secure:...}} in %q", encrypted)
	}
	raw, err := sc.Decrypt(captures[1])
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if raw != "line1\nline2" {
		t.Fatalf("expected stored plaintext %q, got %q", "line1\nline2", raw)
	}

	// DecryptForEdit should re-format as a multiline block.
	decrypted := sc.DecryptForEdit(encrypted)
	expected := "before\n{{plain:\nline1\nline2\n}}\nafter"
	if decrypted != expected {
		t.Fatalf("round-trip mismatch:\n got: %q\nwant: %q", decrypted, expected)
	}
}
