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

	// Start with secure macros and encrypt
	original := "Hello {{secure:secret value}} world"
	encrypted, err := sc.EncryptForSave(original)
	if err != nil {
		t.Fatalf("EncryptForSave failed: %v", err)
	}

	if strings.Contains(encrypted, "secret value") {
		t.Fatal("encrypted markdown should not contain plaintext")
	}
	if !strings.Contains(encrypted, "{{secure_aes:") {
		t.Fatal("encrypted markdown should contain {{secure_aes:...}}")
	}

	// Now decrypt for editing
	decrypted := sc.DecryptForEdit(encrypted)
	if !strings.Contains(decrypted, "{{secure:secret value}}") {
		t.Fatalf("decrypted markdown should contain {{secure:secret value}}, got: %s", decrypted)
	}
	if !strings.Contains(decrypted, "Hello ") {
		t.Fatal("surrounding text should be preserved")
	}
}

func TestMultilineSecureMacroStripsLinebreaks(t *testing.T) {
	sc := NewServerCrypto("multiline-key")

	// Multiline block with leading/trailing newlines around content.
	original := "before\n{{secure:\nline1\nline2\n}}\nafter"
	encrypted, err := sc.EncryptForSave(original)
	if err != nil {
		t.Fatalf("EncryptForSave failed: %v", err)
	}

	if strings.Contains(encrypted, "line1") {
		t.Fatal("encrypted markdown should not contain plaintext")
	}

	// Decrypt the ciphertext directly to verify stored content has no
	// leading/trailing linebreaks.
	captures := secureAesMacroRe.FindStringSubmatch(encrypted)
	if len(captures) < 2 {
		t.Fatalf("expected {{secure_aes:...}} in %q", encrypted)
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
	expected := "before\n{{secure:\nline1\nline2\n}}\nafter"
	if decrypted != expected {
		t.Fatalf("round-trip mismatch:\n got: %q\nwant: %q", decrypted, expected)
	}
}

func TestMultilineSecureMacroCRLF(t *testing.T) {
	sc := NewServerCrypto("crlf-key")

	// Simulate browser textarea submission with \r\n line endings.
	// After handler normalisation (\r\n → \n) the content should
	// round-trip without accumulating extra newlines.
	browser := "before\r\n{{secure:\r\nline1\r\nline2\r\n}}\r\nafter"
	normalised := strings.ReplaceAll(browser, "\r\n", "\n")

	encrypted, err := sc.EncryptForSave(normalised)
	if err != nil {
		t.Fatalf("EncryptForSave failed: %v", err)
	}

	decrypted := sc.DecryptForEdit(encrypted)
	if decrypted != normalised {
		t.Fatalf("CRLF round-trip mismatch:\n got: %q\nwant: %q", decrypted, normalised)
	}

	// A second round-trip must be stable (no extra \n added).
	encrypted2, err := sc.EncryptForSave(decrypted)
	if err != nil {
		t.Fatalf("EncryptForSave (2nd) failed: %v", err)
	}
	decrypted2 := sc.DecryptForEdit(encrypted2)
	if decrypted2 != normalised {
		t.Fatalf("CRLF second round-trip mismatch:\n got: %q\nwant: %q", decrypted2, normalised)
	}
}

func TestEncryptForSavePreservingUnchanged(t *testing.T) {
	sc := NewServerCrypto("preserve-key")

	// Start with some encrypted content on disk.
	original := "Hello {{secure:password123}} and {{secure:other secret}}"
	encrypted, err := sc.EncryptForSave(original)
	if err != nil {
		t.Fatalf("EncryptForSave failed: %v", err)
	}

	// Simulate edit round-trip: decrypt for editor, then re-encrypt preserving.
	editorContent := sc.DecryptForEdit(encrypted)
	reEncrypted, err := sc.EncryptForSavePreserving(editorContent, encrypted)
	if err != nil {
		t.Fatalf("EncryptForSavePreserving failed: %v", err)
	}

	// Unchanged blocks must keep the exact same ciphertext.
	if reEncrypted != encrypted {
		t.Fatalf("expected identical ciphertext for unchanged content:\n got: %q\nwant: %q", reEncrypted, encrypted)
	}
}

func TestValidateContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "valid content no macros",
			content: "Hello world\nNo macros here.",
			wantErr: "",
		},
		{
			name:    "valid single-line secure",
			content: "Hello {{secure:password123}} world",
			wantErr: "",
		},
		{
			name:    "valid multiline secure",
			content: "before\n{{secure:\nline1\nline2\n}}\nafter",
			wantErr: "",
		},
		{
			name:    "valid secure_aes",
			content: "Hello {{secure_aes:abc123def456}} world",
			wantErr: "",
		},
		{
			name:    "unknown tag",
			content: "Hello {{custom:value}} world",
			wantErr: "Line 1: unknown tag {{custom:...}}",
		},
		{
			name:    "unclosed multiline secure block",
			content: "before\n{{secure:\nline1\nline2",
			wantErr: "Line 2: unclosed {{secure: block",
		},
		{
			name:    "multiline secure with text on same line",
			content: "before\n{{secure: some text here\nline2\n}}\nafter",
			wantErr: "Line 2: multiline {{secure: must be on its own line",
		},
		{
			name:    "closing braces not on own line",
			content: "before\n{{secure:\nline1\nline2 }}\nafter",
			wantErr: "Line 4: closing }} of a multiline secure block must be on its own line",
		},
		{
			name:    "double braces in code block style is ok",
			content: "some text {{ not a tag",
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateContent(tt.content)
			if tt.wantErr == "" {
				if got != "" {
					t.Fatalf("expected no error, got: %q", got)
				}
			} else {
				if got == "" {
					t.Fatalf("expected error containing %q, got empty", tt.wantErr)
				}
				if !strings.Contains(got, tt.wantErr) {
					t.Fatalf("expected error containing %q, got: %q", tt.wantErr, got)
				}
			}
		})
	}
}

// TestSecureMacroEncryptedInMarkdownContexts verifies that {{secure:...}} is
// encrypted in all common Markdown contexts — none of them should accidentally
// suppress EncryptForSave.
func TestSecureMacroEncryptedInMarkdownContexts(t *testing.T) {
	sc := NewServerCrypto("ctx-key")
	macro := "{{secure:s3cr3t}}"

	cases := []struct {
		name   string
		source string
	}{
		{"plain", macro},
		{"surrounded by text", "before " + macro + " after"},
		{"blockquote", "> " + macro},
		{"unordered list", "- " + macro},
		{"ordered list", "1. " + macro},
		{"bold", "**" + macro + "**"},
		{"italic", "*" + macro + "*"},
		{"h1 heading", "# " + macro},
		{"table cell", "| col |\n|-----|\n| " + macro + " |"},
		{"nested blockquote", "> > " + macro},
		{"indented (not code-block)", "  " + macro},
		{"after horizontal rule", "---\n" + macro},
		{"multiple macros", macro + " and " + macro},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := sc.EncryptForSave(tc.source)
			if err != nil {
				t.Fatalf("EncryptForSave failed: %v", err)
			}
			if strings.Contains(result, "s3cr3t") {
				t.Errorf("plaintext should be encrypted in %q context, got: %s", tc.name, result)
			}
			if !strings.Contains(result, "{{secure_aes:") {
				t.Errorf("expected {{secure_aes:...}} in %q context, got: %s", tc.name, result)
			}
		})
	}
}

// TestOnlyBackslashEscapesEncryption verifies that ONLY a leading backslash
// prevents {{secure:...}} from being encrypted on save. All other Markdown
// constructs must not accidentally suppress encryption.
func TestOnlyBackslashEscapesEncryption(t *testing.T) {
	sc := NewServerCrypto("escape-ctx-key")
	macro := "{{secure:s3cr3t}}"

	// Only backslash suppresses encryption.
	t.Run("backslash suppresses", func(t *testing.T) {
		result, err := sc.EncryptForSave(`\` + macro)
		if err != nil {
			t.Fatalf("EncryptForSave failed: %v", err)
		}
		if strings.Contains(result, "{{secure_aes:") {
			t.Errorf("backslash-escaped macro must not be encrypted, got: %s", result)
		}
		if strings.Contains(result, "s3cr3t") {
			// After escaping, {{secure:s3cr3t}} is stored literally — plaintext visible is expected.
			// (The user chose to display this literally, not encrypt it.)
		}
		if !strings.Contains(result, "{{secure:s3cr3t}}") {
			t.Errorf("escaped macro content should be preserved literally, got: %s", result)
		}
	})

	// None of these should suppress encryption.
	notSuppressed := []struct {
		name   string
		source string
	}{
		{"blockquote", "> " + macro},
		{"list item", "- " + macro},
		{"bold", "**" + macro + "**"},
		{"italic", "*" + macro + "*"},
		{"table cell", "| h |\n|---|\n| " + macro + " |"},
		{"nested blockquote", "> > " + macro},
		{"indented (not code-block)", "  " + macro},
		{"after horizontal rule", "---\n" + macro},
		// Note: backtick code spans and fenced blocks intentionally suppress
		// rendering (issue #25) but EncryptForSave operates on raw markdown
		// before rendering, so encryption still happens.
		{"inline code span", "`" + macro + "`"},
		{"fenced code block", "```\n" + macro + "\n```\n"},
	}
	for _, tc := range notSuppressed {
		t.Run(tc.name, func(t *testing.T) {
			result, err := sc.EncryptForSave(tc.source)
			if err != nil {
				t.Fatalf("EncryptForSave failed: %v", err)
			}
			if !strings.Contains(result, "{{secure_aes:") {
				t.Errorf("%q must NOT suppress encryption, got: %s", tc.name, result)
			}
		})
	}
}

func TestBackslashEscapedSecureMacroNotEncrypted(t *testing.T) {
	sc := NewServerCrypto("escape-key")

	// \{{secure:plaintext}} should NOT be encrypted — it is treated as literal text.
	input := `before \{{secure:plaintext}} after`
	result, err := sc.EncryptForSave(input)
	if err != nil {
		t.Fatalf("EncryptForSave failed: %v", err)
	}
	if strings.Contains(result, "{{secure_aes:") {
		t.Errorf("escaped secure macro should not be encrypted, got: %s", result)
	}
	if !strings.Contains(result, `\{{secure:plaintext}}`) {
		t.Errorf("backslash must be preserved in saved content so re-saves don't encrypt it, got: %s", result)
	}
}

// TestBackslashEscapedSecureMacroIdempotent verifies that saving a page with
// \{{secure:...}} multiple times never encrypts the content. This was a bug
// where the backslash was stripped on first save, causing encryption on save 2.
func TestBackslashEscapedSecureMacroIdempotent(t *testing.T) {
	sc := NewServerCrypto("escape-key")

	stored := `API key: \{{secure:my-api-key}}`

	for i := range 3 {
		result, err := sc.EncryptForSave(stored)
		if err != nil {
			t.Fatalf("save %d: EncryptForSave failed: %v", i+1, err)
		}
		if strings.Contains(result, "{{secure_aes:") {
			t.Fatalf("save %d: escaped macro was encrypted, got: %s", i+1, result)
		}
		stored = result // simulate re-loading and re-saving
	}
}

func TestEncryptForSavePreservingChanged(t *testing.T) {
	sc := NewServerCrypto("preserve-key")

	// Encrypt two blocks.
	original := "Hello {{secure:password123}} and {{secure:other secret}}"
	encrypted, err := sc.EncryptForSave(original)
	if err != nil {
		t.Fatalf("EncryptForSave failed: %v", err)
	}

	// Modify only the first block in the editor.
	editorContent := sc.DecryptForEdit(encrypted)
	editorContent = strings.Replace(editorContent, "{{secure:password123}}", "{{secure:newpassword}}", 1)

	reEncrypted, err := sc.EncryptForSavePreserving(editorContent, encrypted)
	if err != nil {
		t.Fatalf("EncryptForSavePreserving failed: %v", err)
	}

	// The first block should have new ciphertext.
	if reEncrypted == encrypted {
		t.Fatal("expected different ciphertext when content changed")
	}

	// But the second block's ciphertext should be preserved.
	// Extract the second {{secure_aes:...}} from both.
	oldMatches := secureAesMacroRe.FindAllString(encrypted, -1)
	newMatches := secureAesMacroRe.FindAllString(reEncrypted, -1)
	if len(oldMatches) != 2 || len(newMatches) != 2 {
		t.Fatalf("expected 2 secure blocks in each, got old=%d new=%d", len(oldMatches), len(newMatches))
	}
	if oldMatches[1] != newMatches[1] {
		t.Fatalf("unchanged second block should be preserved:\n got: %q\nwant: %q", newMatches[1], oldMatches[1])
	}
	// First block must differ.
	if oldMatches[0] == newMatches[0] {
		t.Fatal("changed first block should have different ciphertext")
	}
}

func TestServerCryptoPBKDF2RoundTrip(t *testing.T) {
	salt := []byte("0123456789abcdef")
	sc, err := NewServerCryptoPBKDF2("pbkdf2-pass", salt, 1000)
	if err != nil {
		t.Fatalf("NewServerCryptoPBKDF2: %v", err)
	}
	enc, err := sc.Encrypt("top secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := sc.Decrypt(enc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != "top secret" {
		t.Fatalf("round-trip mismatch: got %q", got)
	}
}

func TestServerCryptoPBKDF2WrongSaltFails(t *testing.T) {
	enc, err := mustPBKDF2(t, "pass", []byte("salt-aaaaaaaaaaa"), 1000).Encrypt("secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// Same passphrase, different salt → different key → decryption must fail.
	if _, err := mustPBKDF2(t, "pass", []byte("salt-bbbbbbbbbbb"), 1000).Decrypt(enc); err == nil {
		t.Fatal("expected decryption with wrong salt to fail")
	}
}

func TestPBKDF2CryptoEmitsSecureAes2(t *testing.T) {
	sc := mustPBKDF2(t, "pass", []byte("saltsaltsaltsalt"), 1000)
	out, err := sc.EncryptForSave("login: {{secure:hunter2}}")
	if err != nil {
		t.Fatalf("EncryptForSave: %v", err)
	}
	if !strings.Contains(out, "{{secure_aes2:") {
		t.Fatalf("expected {{secure_aes2:...}}, got %q", out)
	}
	if strings.Contains(out, "{{secure_aes:") {
		t.Fatalf("PBKDF2 crypto must not emit legacy secure_aes, got %q", out)
	}
	back := sc.DecryptForEdit(out)
	if !strings.Contains(back, "{{secure:hunter2}}") {
		t.Fatalf("DecryptForEdit round-trip failed: %q", back)
	}
}

func TestValidateContentAllowsSecureAes2(t *testing.T) {
	if msg := ValidateContent("Hello {{secure_aes2:YWJjZGVm}} world"); msg != "" {
		t.Fatalf("secure_aes2 should be valid, got: %q", msg)
	}
}

func TestResolveSecureSaltEnvWins(t *testing.T) {
	db := openTestDB(t)
	salt, err := ResolveSecureSalt("ZW52LXNhbHQ=", db)
	if err != nil {
		t.Fatalf("ResolveSecureSalt: %v", err)
	}
	if salt != "ZW52LXNhbHQ=" {
		t.Fatalf("env salt should win, got %q", salt)
	}
	// Env value must not be persisted to the DB.
	if _, ok, _ := db.GetConfig("secure_salt"); ok {
		t.Fatal("env salt should not be persisted")
	}
}

func TestResolveSecureSaltRejectsBadBase64(t *testing.T) {
	if _, err := ResolveSecureSalt("not!base64", nil); err == nil {
		t.Fatal("expected error for invalid base64 env salt")
	}
}

func TestResolveSecureSaltGeneratesAndPersists(t *testing.T) {
	db := openTestDB(t)
	first, err := ResolveSecureSalt("", db)
	if err != nil {
		t.Fatalf("ResolveSecureSalt: %v", err)
	}
	if first == "" {
		t.Fatal("expected a generated salt")
	}
	// Second call returns the same persisted salt (stable across restarts).
	second, err := ResolveSecureSalt("", db)
	if err != nil {
		t.Fatalf("ResolveSecureSalt (2nd): %v", err)
	}
	if first != second {
		t.Fatalf("salt not stable: %q vs %q", first, second)
	}
}

func mustPBKDF2(t *testing.T, pass string, salt []byte, iter int) *ServerCrypto {
	t.Helper()
	sc, err := NewServerCryptoPBKDF2(pass, salt, iter)
	if err != nil {
		t.Fatalf("NewServerCryptoPBKDF2: %v", err)
	}
	return sc
}
