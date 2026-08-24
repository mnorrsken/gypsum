package wiki

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSecretMnemonic(t *testing.T) {
	cases := map[string]string{
		"Big Secret thing": "BS",
		"github":           "GI",
		"  spaced   out  ": "SO",
		"1Password vault":  "1V",
		"x":                "X",
		"":                 "?",
		"!!!":              "?",
		"my-api-key":       "MA", // hyphens split words
		"Ålesund kommune":  "ÅK",
		"日本 サイト":           "日サ",
	}
	for title, want := range cases {
		if got := SecretMnemonic(title); got != want {
			t.Errorf("SecretMnemonic(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestSecretColorIsStableAndInRange(t *testing.T) {
	for _, title := range []string{"", "GitHub", "a very long secret title", "Ålesund"} {
		first := SecretColor(title)
		if first < 0 || first >= SecretColorCount {
			t.Fatalf("SecretColor(%q) = %d, out of range", title, first)
		}
		if second := SecretColor(title); second != first {
			t.Errorf("SecretColor(%q) not stable: %d then %d", title, first, second)
		}
	}
	// Case and surrounding whitespace must not change the color, so that
	// secrets.js can hash the same normalized string.
	if SecretColor("GitHub") != SecretColor("  github  ") {
		t.Error("SecretColor is not normalization-stable")
	}
}

func TestParseSecret(t *testing.T) {
	raw := "title: Big Secret thing\n" +
		"url: https://example.com/login\n" +
		"image: secret-20260824-120000-abcd1234.png\n" +
		"description: Prod admin credentials\n" +
		"unknown: ignored\n" +
		"\n" +
		"{{secure_aes2:QUJDREVG}}\n"

	s := ParseSecret("20260824-120000", raw)
	if s.Title != "Big Secret thing" {
		t.Errorf("Title = %q", s.Title)
	}
	if s.URL != "https://example.com/login" {
		t.Errorf("URL = %q", s.URL)
	}
	if s.Image != "secret-20260824-120000-abcd1234.png" {
		t.Errorf("Image = %q", s.Image)
	}
	if s.Description != "Prod admin credentials" {
		t.Errorf("Description = %q", s.Description)
	}
	if !s.Encrypted || s.Variant != "2" || s.Ciphertext != "QUJDREVG" {
		t.Errorf("body parsed as encrypted=%v variant=%q ct=%q", s.Encrypted, s.Variant, s.Ciphertext)
	}
	if s.Mnemonic != "BS" || s.Color != SecretColor(s.Title) {
		t.Errorf("derived presentation wrong: %q / %d", s.Mnemonic, s.Color)
	}
	if want := SecretCreatedFromID("20260824-120000"); !s.Created.Equal(want) {
		t.Errorf("Created = %v, want %v", s.Created, want)
	}
}

func TestParseSecretLegacyMacroAndDefaults(t *testing.T) {
	s := ParseSecret("20260824-120000", "\n{{secure_aes:QUJD}}\n")
	if s.Title != "Untitled" {
		t.Errorf("missing title should default to Untitled, got %q", s.Title)
	}
	if !s.Encrypted || s.Variant != "" || s.Ciphertext != "QUJD" {
		t.Errorf("legacy macro not parsed: %+v", s)
	}
}

// A body that is not a secure macro must never be exposed as a decryptable
// secret — otherwise a hand-edited file could hand plaintext to the browser
// dressed up as ciphertext.
func TestParseSecretRejectsPlaintextBody(t *testing.T) {
	s := ParseSecret("20260824-120000", "title: Oops\n\nhunter2\n")
	if s.Encrypted || s.Ciphertext != "" {
		t.Fatalf("plaintext body treated as encrypted: %+v", s)
	}
	if s.SecureMacro() != "" {
		t.Errorf("SecureMacro on unencrypted entry = %q", s.SecureMacro())
	}
}

func TestSecretMarshalRoundTrip(t *testing.T) {
	entry := SecretEntry{
		Title:       "Big Secret thing",
		URL:         "https://example.com",
		Description: "note",
	}
	raw := entry.Marshal("{{secure_aes2:QUJD}}")
	back := ParseSecret("20260824-120000", raw)
	if back.Title != entry.Title || back.URL != entry.URL || back.Description != entry.Description {
		t.Fatalf("round trip lost fields: %+v", back)
	}
	if back.SecureMacro() != "{{secure_aes2:QUJD}}" {
		t.Errorf("SecureMacro = %q", back.SecureMacro())
	}
	// Empty optional fields are omitted rather than written blank.
	if strings.Contains(raw, "image:") {
		t.Errorf("empty image field was written:\n%s", raw)
	}
}

// A newline smuggled into a header field must not be able to forge extra
// header lines or a fake body.
func TestSecretMarshalSanitizesFields(t *testing.T) {
	entry := SecretEntry{Title: "Evil\nurl: https://attacker.example", Description: "a\tb"}
	raw := entry.Marshal("{{secure_aes2:QUJD}}")
	back := ParseSecret("20260824-120000", raw)
	if back.URL != "" {
		t.Fatalf("injected header line was parsed: URL = %q", back.URL)
	}
	if strings.Contains(back.Title, "\n") || strings.Contains(back.Description, "\t") {
		t.Errorf("field not sanitized: %+v", back)
	}
}

func TestValidSecretBody(t *testing.T) {
	valid := []string{"{{secure_aes2:QUJD}}", "{{secure_aes:QUJD}}", "  {{secure_aes2:QUJD}}  "}
	for _, v := range valid {
		if !ValidSecretBody(v) {
			t.Errorf("ValidSecretBody(%q) = false, want true", v)
		}
	}
	invalid := []string{"", "hunter2", "{{secure:plain}}", "{{secure_aes2:QUJD}} trailing", "{{secure_aes3:QUJD}}"}
	for _, v := range invalid {
		if ValidSecretBody(v) {
			t.Errorf("ValidSecretBody(%q) = true, want false", v)
		}
	}
}

func TestValidSecretID(t *testing.T) {
	valid := []string{"20260824-120000", "20260824-120000-2"}
	for _, id := range valid {
		if !ValidSecretID(id) {
			t.Errorf("ValidSecretID(%q) = false", id)
		}
	}
	invalid := []string{"", "../../etc/passwd", "2026-08-24", "20260824-120000/x", "notanid"}
	for _, id := range invalid {
		if ValidSecretID(id) {
			t.Errorf("ValidSecretID(%q) = true", id)
		}
	}
}

func TestSecretStoreCRUD(t *testing.T) {
	store := NewPageStore(filepath.Join(t.TempDir(), "pages"))

	id, err := store.CreateSecret(SecretEntry{Title: "GitHub token", URL: "https://github.com"}, "{{secure_aes2:QUJD}}")
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	if !ValidSecretID(id) {
		t.Fatalf("CreateSecret returned invalid id %q", id)
	}

	loaded, err := store.LoadSecret(id)
	if err != nil {
		t.Fatalf("LoadSecret: %v", err)
	}
	if loaded.Title != "GitHub token" || loaded.Ciphertext != "QUJD" {
		t.Fatalf("loaded = %+v", loaded)
	}

	// Secret files are owner-only.
	info, err := os.Stat(filepath.Join(store.SecretsDir(), MarkdownFilename(id)))
	if err != nil {
		t.Fatalf("stat secret file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("secret file mode = %v, want 0600", perm)
	}

	loaded.Title = "GitHub PAT"
	if err := store.SaveSecret(id, *loaded, loaded.SecureMacro()); err != nil {
		t.Fatalf("SaveSecret: %v", err)
	}
	if again, _ := store.LoadSecret(id); again.Title != "GitHub PAT" {
		t.Errorf("after save Title = %q", again.Title)
	}

	if err := store.DeleteSecret(id); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}
	if _, err := store.LoadSecret(id); err != ErrPageNotFound {
		t.Errorf("LoadSecret after delete = %v, want ErrPageNotFound", err)
	}
}

func TestSecretStoreRejectsTraversalID(t *testing.T) {
	store := NewPageStore(filepath.Join(t.TempDir(), "pages"))
	for _, id := range []string{"../evil", "..", "sub/dir"} {
		if _, err := store.LoadSecret(id); err != ErrPageNotFound {
			t.Errorf("LoadSecret(%q) = %v, want ErrPageNotFound", id, err)
		}
		if err := store.DeleteSecret(id); err != ErrPageNotFound {
			t.Errorf("DeleteSecret(%q) = %v, want ErrPageNotFound", id, err)
		}
	}
}

func TestListSecretsSortsByTitle(t *testing.T) {
	store := NewPageStore(filepath.Join(t.TempDir(), "pages"))
	for _, title := range []string{"zeta", "Alpha", "middle"} {
		if _, err := store.CreateSecret(SecretEntry{Title: title}, "{{secure_aes2:QUJD}}"); err != nil {
			t.Fatalf("CreateSecret(%q): %v", title, err)
		}
	}
	// Stray files in the directory are ignored.
	if err := os.WriteFile(filepath.Join(store.SecretsDir(), "README.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	secrets, err := store.ListSecrets()
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}
	var got []string
	for _, s := range secrets {
		got = append(got, s.Title)
	}
	want := []string{"Alpha", "middle", "zeta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ListSecrets order = %v, want %v", got, want)
	}
}

func TestCreateSecretIDCollision(t *testing.T) {
	store := NewPageStore(filepath.Join(t.TempDir(), "pages"))
	base := NewSecretID(time.Now())
	// Occupy the ID this second would produce.
	if err := store.writeSecret(base, "title: taken\n\n{{secure_aes2:QUJD}}\n"); err != nil {
		t.Fatal(err)
	}
	id, err := store.CreateSecret(SecretEntry{Title: "next"}, "{{secure_aes2:QUJD}}")
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	if id == base || !ValidSecretID(id) {
		t.Fatalf("collision id = %q (base %q)", id, base)
	}
}

func TestListSecretsMissingDir(t *testing.T) {
	store := NewPageStore(filepath.Join(t.TempDir(), "pages"))
	if err := os.RemoveAll(store.SecretsDir()); err != nil {
		t.Fatal(err)
	}
	secrets, err := store.ListSecrets()
	if err != nil {
		t.Fatalf("ListSecrets on missing dir: %v", err)
	}
	if len(secrets) != 0 {
		t.Errorf("expected no secrets, got %d", len(secrets))
	}
}
