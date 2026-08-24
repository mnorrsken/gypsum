package wiki

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// SecretColorCount is the number of distinct mnemonic-tile colors. A secret's
// color is derived by hashing its title, so a secret keeps a stable tile color
// as long as its title is unchanged. The browser mirrors this in secrets.js.
const SecretColorCount = 12

// SecretRevealSeconds is how long a revealed secret stays on screen before the
// vault re-masks it, matching the {{secure_aes2}} reveal timeout on wiki pages.
const SecretRevealSeconds = 60

// secretIDTimeLayout is the timestamp format encoded in a secret ID.
const secretIDTimeLayout = "20060102-150405"

// secretIDPattern matches a secret ID: a creation timestamp (yyyymmdd-hhmmss)
// with an optional numeric collision suffix. Used as a path-traversal guard on
// every secret request.
var secretIDPattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}(-[0-9]+)?$`)

// secretHeaderRe matches a "key: value" header line in a secret file.
var secretHeaderRe = regexp.MustCompile(`^([a-z][a-z0-9_-]*):[ \t]*(.*)$`)

// secretBodyRe matches the encrypted body of a secret file: a single
// {{secure_aes:...}} or {{secure_aes2:...}} macro. Group 1 is the variant
// ("" for the legacy SHA-256 KDF, "2" for PBKDF2), group 2 the ciphertext.
var secretBodyRe = regexp.MustCompile(`^\{\{secure_aes(2?):([\w+/=]+)\}\}$`)

// SecretEntry is one entry in the secrets vault. The secret value itself is
// only ever held as ciphertext: the browser encrypts before POSTing and
// decrypts on reveal, so the server never sees the plaintext.
//
// Mnemonic and Color are presentational — both are derived from Title and
// never persisted, so renaming a secret restyles its tile.
type SecretEntry struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	URL         string    `json:"url,omitempty"`
	Image       string    `json:"image,omitempty"` // filename under images/, "" = none
	Ciphertext  string    `json:"-"`               // base64 payload of the secure macro
	Variant     string    `json:"-"`               // "" = secure_aes, "2" = secure_aes2
	Encrypted   bool      `json:"-"`               // false when the stored body is not a secure macro
	Mnemonic    string    `json:"mnemonic"`
	Color       int       `json:"color"`
	Created     time.Time `json:"created"`
	Updated     time.Time `json:"updated"`
}

// NewSecretID returns a secret ID derived from t (yyyymmdd-hhmmss).
func NewSecretID(t time.Time) string {
	return t.Format(secretIDTimeLayout)
}

// ValidSecretID reports whether id is a well-formed secret ID.
func ValidSecretID(id string) bool {
	return secretIDPattern.MatchString(id)
}

// SecretCreatedFromID parses the creation time encoded in a secret ID. Any
// "-N" collision suffix is ignored. Returns the zero time if id is malformed.
func SecretCreatedFromID(id string) time.Time {
	if !ValidSecretID(id) {
		return time.Time{}
	}
	base := id
	if len(base) > len(secretIDTimeLayout) {
		base = base[:len(secretIDTimeLayout)] // drop the collision suffix
	}
	t, err := time.ParseInLocation(secretIDTimeLayout, base, time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

// SecretMnemonic derives the two-letter tile label from a title: the initials
// of the first two words, or the first two letters of a single word. Digits
// count as word characters so "1Password" yields "1P". Returns "?" when the
// title holds no usable characters. secrets.js mirrors this exactly.
func SecretMnemonic(title string) string {
	words := strings.FieldsFunc(title, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	switch {
	case len(words) == 0:
		return "?"
	case len(words) >= 2:
		return strings.ToUpper(firstRunes(words[0], 1) + firstRunes(words[1], 1))
	default:
		return strings.ToUpper(firstRunes(words[0], 2))
	}
}

// firstRunes returns the first n runes of s (fewer if s is shorter).
func firstRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) > n {
		runes = runes[:n]
	}
	return string(runes)
}

// SecretColor returns a stable color index in [0, SecretColorCount) for a
// secret title. The same title always yields the same color on both the server
// and the browser: secrets.js hashes the identical FNV-1a formula over the same
// UTF-8 bytes and normalization (trim, lowercase).
func SecretColor(title string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(title))))
	return int(h.Sum32() % SecretColorCount)
}

// ── Secret file format ─────────────────────────────────────────────────
//
// A secret is stored as a small markdown file: "key: value" header lines, a
// blank line, then the encrypted body. Keeping it plain text means secrets
// diff and merge like every other file in the wiki repo:
//
//	title: Big Secret thing
//	url: https://example.com/login
//	image: secret-20260824-153042-a1b2c3d4.png
//	description: Prod admin credentials
//
//	{{secure_aes2:BASE64...}}

// ParseSecret builds a SecretEntry from a stored secret file. Unknown header
// keys are ignored. A body that is not a single secure macro leaves Encrypted
// false and Ciphertext empty, so a hand-edited or corrupted file can never leak
// its contents to the browser as if it were a decryptable secret.
func ParseSecret(id, raw string) SecretEntry {
	s := SecretEntry{ID: id, Created: SecretCreatedFromID(id)}
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	i := 0
	for ; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			i++ // consume the blank separator line
			break
		}
		m := secretHeaderRe.FindStringSubmatch(line)
		if m == nil {
			break // not a header line — treat the rest as body
		}
		value := strings.TrimSpace(m[2])
		switch m[1] {
		case "title":
			s.Title = value
		case "description":
			s.Description = value
		case "url":
			s.URL = value
		case "image":
			s.Image = value
		}
	}
	body := strings.TrimSpace(strings.Join(lines[min(i, len(lines)):], "\n"))
	if m := secretBodyRe.FindStringSubmatch(body); m != nil {
		s.Variant, s.Ciphertext, s.Encrypted = m[1], m[2], true
	}
	if s.Title == "" {
		s.Title = "Untitled"
	}
	s.Mnemonic = SecretMnemonic(s.Title)
	s.Color = SecretColor(s.Title)
	return s
}

// Marshal renders a secret back to its stored file representation.
func (s SecretEntry) Marshal(body string) string {
	var b strings.Builder
	writeField := func(key, value string) {
		value = sanitizeSecretField(value)
		if value == "" {
			return
		}
		fmt.Fprintf(&b, "%s: %s\n", key, value)
	}
	writeField("title", s.Title)
	writeField("url", s.URL)
	writeField("image", s.Image)
	writeField("description", s.Description)
	b.WriteString("\n")
	b.WriteString(strings.TrimSpace(body))
	b.WriteString("\n")
	return b.String()
}

// SecureMacro returns the stored body form of a secret's ciphertext.
func (s SecretEntry) SecureMacro() string {
	if !s.Encrypted {
		return ""
	}
	return fmt.Sprintf("{{secure_aes%s:%s}}", s.Variant, s.Ciphertext)
}

// sanitizeSecretField collapses any newline or tab in a header value so a
// single field can never break out into extra header lines or into the body.
func sanitizeSecretField(v string) string {
	v = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, v)
	return strings.TrimSpace(v)
}

// ValidSecretBody reports whether body is exactly one secure macro. The vault
// only ever accepts ciphertext from the browser; rejecting anything else keeps
// a plaintext secret from being written to disk by a malformed request.
func ValidSecretBody(body string) bool {
	return secretBodyRe.MatchString(strings.TrimSpace(body))
}

// ── Secret store operations ────────────────────────────────────────────

// SecretsDir returns the directory holding secret files.
func (s *PageStore) SecretsDir() string { return s.secretsDir }

// secretPath returns the filesystem path for a secret, or "" if absent.
func (s *PageStore) secretPath(id string) string {
	p := filepath.Join(s.secretsDir, MarkdownFilename(id))
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// writeSecret writes a secret file, creating the secrets directory if needed.
func (s *PageStore) writeSecret(id, content string) error {
	if err := os.MkdirAll(s.secretsDir, 0o755); err != nil {
		return err
	}
	// 0600: the file holds ciphertext, but there is no reason for anything
	// outside the wiki process to read it.
	return os.WriteFile(filepath.Join(s.secretsDir, MarkdownFilename(id)), []byte(content), 0o600)
}

// CreateSecret allocates a fresh secret ID and writes the entry, returning the
// new ID. A numeric suffix is appended on second-resolution collisions.
func (s *PageStore) CreateSecret(entry SecretEntry, body string) (string, error) {
	base := NewSecretID(time.Now())
	id := base
	for i := 2; ; i++ {
		if s.secretPath(id) == "" {
			break
		}
		id = fmt.Sprintf("%s-%d", base, i)
	}
	if err := s.writeSecret(id, entry.Marshal(body)); err != nil {
		return "", err
	}
	return id, nil
}

// LoadSecret returns a single secret by id.
func (s *PageStore) LoadSecret(id string) (*SecretEntry, error) {
	if !ValidSecretID(id) {
		return nil, ErrPageNotFound
	}
	path := s.secretPath(id)
	if path == "" {
		return nil, ErrPageNotFound
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	entry := ParseSecret(id, string(raw))
	entry.Updated = entry.Created
	if info, err := os.Stat(path); err == nil {
		entry.Updated = info.ModTime()
	}
	return &entry, nil
}

// SaveSecret overwrites an existing secret.
func (s *PageStore) SaveSecret(id string, entry SecretEntry, body string) error {
	if !ValidSecretID(id) || s.secretPath(id) == "" {
		return ErrPageNotFound
	}
	return s.writeSecret(id, entry.Marshal(body))
}

// ListSecrets returns every secret sorted by title (case-insensitive), which
// keeps the vault stable as entries are revealed and edited.
func (s *PageStore) ListSecrets() ([]SecretEntry, error) {
	entries, err := os.ReadDir(s.secretsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	secrets := make([]SecretEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		id := SlugFromFilename(entry.Name())
		if !ValidSecretID(id) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.secretsDir, entry.Name()))
		if err != nil {
			continue
		}
		parsed := ParseSecret(id, string(raw))
		parsed.Updated = parsed.Created
		if info, err := entry.Info(); err == nil {
			parsed.Updated = info.ModTime()
		}
		secrets = append(secrets, parsed)
	}
	sort.Slice(secrets, func(i, j int) bool {
		ti, tj := strings.ToLower(secrets[i].Title), strings.ToLower(secrets[j].Title)
		if ti == tj {
			return secrets[i].ID < secrets[j].ID
		}
		return ti < tj
	})
	return secrets, nil
}

// DeleteSecret permanently removes a secret.
func (s *PageStore) DeleteSecret(id string) error {
	if !ValidSecretID(id) {
		return ErrPageNotFound
	}
	path := s.secretPath(id)
	if path == "" {
		return ErrPageNotFound
	}
	return os.Remove(path)
}
