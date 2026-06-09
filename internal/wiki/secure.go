package wiki

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// SecurePBKDF2Iterations is the PBKDF2-HMAC-SHA256 work factor for {{secure_aes2:...}}
// blocks. It is served to the browser so client and server agree on the KDF.
const SecurePBKDF2Iterations = 600000

const securePBKDF2KeyLen = 32

// secureAesMacroRe matches the legacy {{secure_aes:PAYLOAD}} (SHA-256 KDF).
var secureAesMacroRe = regexp.MustCompile(`\{\{secure_aes:([\w+/=]+)\}\}`)

// secureAes2MacroRe matches {{secure_aes2:PAYLOAD}} (PBKDF2 KDF + per-deploy salt).
var secureAes2MacroRe = regexp.MustCompile(`\{\{secure_aes2:([\w+/=]+)\}\}`)

// secureMacroRe matches {{secure:CONTENT}} used in the editor for decrypted blocks.
// An optional leading backslash is captured so \{{secure:...}} can be skipped.
var secureMacroRe = regexp.MustCompile(`(?s)(\\?)\{\{secure:(.*?)\}\}`)

// ServerCrypto provides AES-256-GCM encryption using a single server key. The
// CLI re-encrypt tool is the only runtime user — during normal edit/save all
// crypto happens in the browser and the server never holds a key.
type ServerCrypto struct {
	key [32]byte
	// macro is the wire-format name this instance reads and writes
	// ("secure_aes" for the legacy SHA-256 KDF, "secure_aes2" for PBKDF2).
	macro string
	// decryptRe matches blocks of the configured macro.
	decryptRe *regexp.Regexp
}

// NewServerCrypto derives a 256-bit key from the passphrase using the legacy
// unsalted SHA-256 KDF. It reads and writes {{secure_aes:...}} blocks.
func NewServerCrypto(passphrase string) *ServerCrypto {
	return &ServerCrypto{
		key:       sha256.Sum256([]byte(passphrase)),
		macro:     "secure_aes",
		decryptRe: secureAesMacroRe,
	}
}

const secureSaltConfigKey = "secure_salt"

// ResolveSecureSalt returns the base64-encoded per-deployment PBKDF2 salt.
// Priority: the GYPSUM_SECURE_SALT value (envSalt) if non-empty, otherwise a
// value persisted in the database, otherwise a freshly generated 16-byte salt
// which is stored so it stays stable across restarts. The salt is not secret —
// it only ensures two deployments derive different keys from the same passphrase.
func ResolveSecureSalt(envSalt string, db *DB) (string, error) {
	if envSalt != "" {
		if _, err := base64.StdEncoding.DecodeString(envSalt); err != nil {
			return "", fmt.Errorf("GYPSUM_SECURE_SALT is not valid base64: %w", err)
		}
		return envSalt, nil
	}
	if db != nil {
		if v, ok, err := db.GetConfig(secureSaltConfigKey); err != nil {
			return "", err
		} else if ok && v != "" {
			return v, nil
		}
	}
	raw := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	salt := base64.StdEncoding.EncodeToString(raw)
	if db != nil {
		if err := db.SetConfig(secureSaltConfigKey, salt); err != nil {
			return "", err
		}
	}
	return salt, nil
}

// NewServerCryptoPBKDF2 derives a 256-bit key with PBKDF2-HMAC-SHA256 over the
// passphrase and per-deployment salt. It reads and writes {{secure_aes2:...}}.
func NewServerCryptoPBKDF2(passphrase string, salt []byte, iterations int) (*ServerCrypto, error) {
	dk, err := pbkdf2.Key(sha256.New, passphrase, salt, iterations, securePBKDF2KeyLen)
	if err != nil {
		return nil, err
	}
	sc := &ServerCrypto{macro: "secure_aes2", decryptRe: secureAes2MacroRe}
	copy(sc.key[:], dk)
	return sc, nil
}

// Encrypt encrypts plaintext and returns a base64 string (nonce + ciphertext).
func (sc *ServerCrypto) Encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(sc.key[:])
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	sealed := aead.Seal(nonce, nonce, []byte(plaintext), nil) // prepend nonce
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt decodes a base64 string (nonce + ciphertext) and returns plaintext.
func (sc *ServerCrypto) Decrypt(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("invalid base64: %w", err)
	}

	block, err := aes.NewCipher(sc.key[:])
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := aead.NonceSize()
	if len(raw) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := raw[:nonceSize], raw[nonceSize:]
	plain, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed: %w", err)
	}
	return string(plain), nil
}

// DecryptForEdit replaces every block of this instance's macro
// ({{secure_aes:...}} or {{secure_aes2:...}}) in markdown with {{secure:DECRYPTED}}
// so the editor shows cleartext. Blocks that fail to decrypt are left as-is.
func (sc *ServerCrypto) DecryptForEdit(markdown string) string {
	return sc.decryptRe.ReplaceAllStringFunc(markdown, func(match string) string {
		captures := sc.decryptRe.FindStringSubmatch(match)
		if len(captures) < 2 || captures[1] == "" {
			return match
		}
		plain, err := sc.Decrypt(captures[1])
		if err != nil {
			return match // leave as-is if decryption fails
		}
		// If content contains newlines, format as multiline block.
		if strings.Contains(plain, "\n") {
			return fmt.Sprintf("{{secure:\n%s\n}}", plain)
		}
		return fmt.Sprintf("{{secure:%s}}", plain)
	})
}

// EncryptForSave replaces every {{secure:CONTENT}} in markdown with
// {{secure_aes:CIPHERTEXT}} for storage.
func (sc *ServerCrypto) EncryptForSave(markdown string) (string, error) {
	return sc.encryptForSave(markdown, nil)
}

// EncryptForSavePreserving works like EncryptForSave but reuses the original
// ciphertext for any {{secure:CONTENT}} block whose plaintext matches an
// existing {{secure_aes:...}} block in oldMarkdown. This avoids spurious diffs
// caused by AES-GCM's random nonce.
func (sc *ServerCrypto) EncryptForSavePreserving(markdown, oldMarkdown string) (string, error) {
	// Build plaintext → original ciphertext map from old content.
	preserve := make(map[string]string) // plaintext → original macro
	for _, m := range sc.decryptRe.FindAllStringSubmatch(oldMarkdown, -1) {
		if len(m) < 2 {
			continue
		}
		plain, err := sc.Decrypt(m[1])
		if err != nil {
			continue
		}
		preserve[plain] = m[0] // keep the full macro string
	}
	return sc.encryptForSave(markdown, preserve)
}

func (sc *ServerCrypto) encryptForSave(markdown string, preserve map[string]string) (string, error) {
	var encryptErr error
	result := secureMacroRe.ReplaceAllStringFunc(markdown, func(match string) string {
		captures := secureMacroRe.FindStringSubmatch(match)
		if len(captures) < 3 {
			return match
		}
		if captures[1] == `\` {
			// Escaped: \{{secure:...}} — preserve the original text including the
			// backslash so subsequent saves don't encrypt it.
			return match
		}
		content := captures[2]
		// For multiline blocks, strip the leading and trailing linebreaks
		// so {{secure:\nxxx\nyyy\n}} stores only "xxx\nyyy".
		if strings.HasPrefix(content, "\n") && strings.HasSuffix(content, "\n") {
			content = strings.TrimPrefix(content, "\n")
			content = strings.TrimSuffix(content, "\n")
		}
		// Reuse original ciphertext when the plaintext is unchanged.
		if preserve != nil {
			if original, ok := preserve[content]; ok {
				return original
			}
		}
		encoded, err := sc.Encrypt(content)
		if err != nil {
			encryptErr = err
			return match
		}
		return fmt.Sprintf("{{%s:%s}}", sc.macro, encoded)
	})
	return result, encryptErr
}

var anyDoubleBraceOpen = regexp.MustCompile(`\{\{`)

// ValidateContent checks the content for malformed custom tags.
// Returns a user-friendly error message or empty string if valid.
func ValidateContent(content string) string {
	lines := strings.Split(content, "\n")

	// Check for unknown {{tag:...}} patterns
	for i, line := range lines {
		lineNum := i + 1
		for _, loc := range anyDoubleBraceOpen.FindAllStringIndex(line, -1) {
			rest := line[loc[0]:]
			if strings.HasPrefix(rest, "{{secure:") || strings.HasPrefix(rest, "{{secure_aes:") || strings.HasPrefix(rest, "{{secure_aes2:") {
				continue
			}
			if len(rest) > 2 && rest[2] != '{' && rest[2] != ' ' && rest[2] != '\n' {
				colonIdx := strings.Index(rest, ":")
				closeIdx := strings.Index(rest, "}}")
				if colonIdx > 2 && (closeIdx < 0 || colonIdx < closeIdx) {
					tag := rest[2:colonIdx]
					if !strings.ContainsAny(tag, " \t\n") {
						return fmt.Sprintf("Line %d: unknown tag {{%s:...}}. Only {{secure:...}} is supported.", lineNum, tag)
					}
				}
			}
		}
	}

	// Track structure
	inBlock := false
	blockStartLine := 0

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)

		if !inBlock {
			// Look for {{secure: on this line
			idx := strings.Index(line, "{{secure:")
			if idx < 0 {
				continue
			}

			// Check if it closes on the same line (single-line block)
			after := line[idx+9:] // after "{{secure:"
			closeIdx := strings.Index(after, "}}")
			if closeIdx >= 0 {
				// Single-line: {{secure:content}} — valid
				continue
			}

			// Multiline block: {{secure: must be the entire trimmed content of this line
			if trimmed != "{{secure:" {
				return fmt.Sprintf("Line %d: multiline {{secure: must be on its own line with no other text.", lineNum)
			}

			inBlock = true
			blockStartLine = lineNum
		} else {
			// Inside a multiline block — look for closing }}
			if strings.Contains(line, "}}") {
				if trimmed != "}}" {
					return fmt.Sprintf("Line %d: closing }} of a multiline secure block must be on its own line.", lineNum)
				}
				inBlock = false
			}
		}
	}

	if inBlock {
		return fmt.Sprintf("Line %d: unclosed {{secure: block — missing closing }}.", blockStartLine)
	}

	return ""
}
