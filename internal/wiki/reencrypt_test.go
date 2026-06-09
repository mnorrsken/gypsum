package wiki

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReencryptMigratesLegacyAndAes2 verifies that a directory mixing legacy
// {{secure_aes:...}} (SHA-256) and {{secure_aes2:...}} (PBKDF2, old salt) blocks
// is fully migrated to {{secure_aes2:...}} under the new key + salt, and the
// result decrypts with the new credentials.
func TestReencryptMigratesLegacyAndAes2(t *testing.T) {
	dir := t.TempDir()
	oldSalt := []byte("old-salt-16bytes")
	newSalt := []byte("new-salt-16bytes")
	const iters = 1000

	legacy := NewServerCrypto("old-pass")
	oldAes2 := mustPBKDF2(t, "old-pass", oldSalt, iters)

	legacyField, err := legacy.EncryptForSave("{{secure:legacy-secret}}")
	if err != nil {
		t.Fatalf("legacy EncryptForSave: %v", err)
	}
	aes2Field, err := oldAes2.EncryptForSave("{{secure:aes2-secret}}")
	if err != nil {
		t.Fatalf("aes2 EncryptForSave: %v", err)
	}
	page := "# Page\n\n" + legacyField + "\n\n" + aes2Field + "\n"
	pagePath := filepath.Join(dir, "Page.md")
	if err := os.WriteFile(pagePath, []byte(page), 0o644); err != nil {
		t.Fatalf("write page: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := reencryptDir(dir, "old-pass", "new-pass", oldSalt, newSalt, iters, false, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("reencryptDir exit %d, stderr: %s", code, stderr.String())
	}

	out, err := os.ReadFile(pagePath)
	if err != nil {
		t.Fatalf("read page: %v", err)
	}
	migrated := string(out)
	if strings.Contains(migrated, "{{secure_aes:") {
		t.Fatalf("legacy blocks should be upgraded, got: %s", migrated)
	}
	if c := strings.Count(migrated, "{{secure_aes2:"); c != 2 {
		t.Fatalf("expected 2 secure_aes2 blocks, got %d: %s", c, migrated)
	}

	// Both secrets must decrypt with the new key + salt.
	newAes2 := mustPBKDF2(t, "new-pass", newSalt, iters)
	plain := newAes2.DecryptForEdit(migrated)
	if !strings.Contains(plain, "{{secure:legacy-secret}}") || !strings.Contains(plain, "{{secure:aes2-secret}}") {
		t.Fatalf("migrated content did not decrypt with new credentials: %s", plain)
	}
}

// TestReencryptWarnsOnUndecryptableAes2 verifies that secure_aes2 blocks are
// reported (and left intact) when no -old-salt is supplied to decrypt them.
func TestReencryptWarnsOnUndecryptableAes2(t *testing.T) {
	dir := t.TempDir()
	field, err := mustPBKDF2(t, "p", []byte("some-salt-16byte"), 1000).EncryptForSave("{{secure:x}}")
	if err != nil {
		t.Fatalf("EncryptForSave: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "P.md"), []byte(field), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var stdout, stderr bytes.Buffer
	// No old salt → the secure_aes2 block cannot be decrypted.
	code := reencryptDir(dir, "p", "p", nil, []byte("new-salt-16bytes"), 1000, false, &stdout, &stderr)
	if code == 0 {
		t.Fatal("expected non-zero exit for undecryptable field")
	}
	if !strings.Contains(stderr.String(), "old-salt") {
		t.Fatalf("expected hint to pass -old-salt, got: %s", stderr.String())
	}
}

// TestRunReencryptRequiresNewSalt verifies the CLI rejects a missing -new-salt.
func TestRunReencryptRequiresNewSalt(t *testing.T) {
	if code := RunReencrypt([]string{"-dir", t.TempDir(), "-old-key", "a", "-new-key", "b"}); code == 0 {
		t.Fatal("expected non-zero exit when -new-salt is missing")
	}
}

func TestRunReencryptRejectsBadSalt(t *testing.T) {
	args := []string{"-dir", t.TempDir(), "-old-key", "a", "-new-key", "b", "-new-salt", "not!base64"}
	if code := RunReencrypt(args); code == 0 {
		t.Fatal("expected non-zero exit for invalid -new-salt base64")
	}
}
