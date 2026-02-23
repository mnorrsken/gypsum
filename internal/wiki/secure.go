package wiki

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// secureMacroRe matches {{secure:PAYLOAD}} where PAYLOAD is base64 ciphertext.
var secureMacroRe = regexp.MustCompile(`\{\{secure:([\w+/=]+)\}\}`)

// plainMacroRe matches {{plain:CONTENT}} used in the editor for decrypted blocks.
var plainMacroRe = regexp.MustCompile(`(?s)\{\{plain:(.*?)\}\}`)

// ServerCrypto provides AES-256-GCM encryption using a single server key.
type ServerCrypto struct {
	key [32]byte
}

// NewServerCrypto derives a 256-bit key from the supplied passphrase.
func NewServerCrypto(passphrase string) *ServerCrypto {
	sc := &ServerCrypto{}
	sc.key = sha256.Sum256([]byte(passphrase))
	return sc
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

// DecryptForEdit replaces every {{secure:CIPHERTEXT}} in markdown with
// {{plain:DECRYPTED}} so the editor shows cleartext.
func (sc *ServerCrypto) DecryptForEdit(markdown string) string {
	return secureMacroRe.ReplaceAllStringFunc(markdown, func(match string) string {
		captures := secureMacroRe.FindStringSubmatch(match)
		if len(captures) < 2 || captures[1] == "" {
			return match
		}
		plain, err := sc.Decrypt(captures[1])
		if err != nil {
			return match // leave as-is if decryption fails
		}
		// If content contains newlines, format as multiline block.
		if strings.Contains(plain, "\n") {
			return fmt.Sprintf("{{plain:\n%s\n}}", plain)
		}
		return fmt.Sprintf("{{plain:%s}}", plain)
	})
}

// EncryptForSave replaces every {{plain:CONTENT}} in markdown with
// {{secure:CIPHERTEXT}} for storage.
func (sc *ServerCrypto) EncryptForSave(markdown string) (string, error) {
	var encryptErr error
	result := plainMacroRe.ReplaceAllStringFunc(markdown, func(match string) string {
		captures := plainMacroRe.FindStringSubmatch(match)
		if len(captures) < 2 {
			return match
		}
		content := captures[1]
		// For multiline blocks, strip the leading and trailing linebreaks
		// so {{plain:\nxxx\nyyy\n}} stores only "xxx\nyyy".
		if strings.HasPrefix(content, "\n") && strings.HasSuffix(content, "\n") {
			content = strings.TrimPrefix(content, "\n")
			content = strings.TrimSuffix(content, "\n")
		}
		encoded, err := sc.Encrypt(content)
		if err != nil {
			encryptErr = err
			return match
		}
		return fmt.Sprintf("{{secure:%s}}", encoded)
	})
	return result, encryptErr
}

// unknownTagRe matches any {{word:...}} or {{word that isn't a known tag.
var unknownTagRe = regexp.MustCompile(`\{\{(?:plain|secure)\b`)
var anyDoubleBraceOpen = regexp.MustCompile(`\{\{`)
var anyDoubleBraceClose = regexp.MustCompile(`\}\}`)

// ValidateContent checks the content for malformed custom tags.
// Returns a user-friendly error message or empty string if valid.
func ValidateContent(content string) string {
	lines := strings.Split(content, "\n")

	// Check for unknown {{tag:...}} patterns
	for i, line := range lines {
		lineNum := i + 1
		for _, loc := range anyDoubleBraceOpen.FindAllStringIndex(line, -1) {
			rest := line[loc[0]:]
			if strings.HasPrefix(rest, "{{plain:") || strings.HasPrefix(rest, "{{secure:") {
				continue
			}
			if len(rest) > 2 && rest[2] != '{' && rest[2] != ' ' && rest[2] != '\n' {
				colonIdx := strings.Index(rest, ":")
				closeIdx := strings.Index(rest, "}}")
				if colonIdx > 2 && (closeIdx < 0 || colonIdx < closeIdx) {
					tag := rest[2:colonIdx]
					if !strings.ContainsAny(tag, " \t\n") {
						return fmt.Sprintf("Line %d: unknown tag {{%s:...}}. Only {{plain:...}} is supported.", lineNum, tag)
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
			// Look for {{plain: on this line
			idx := strings.Index(line, "{{plain:")
			if idx < 0 {
				// Check for stray }}
				if strings.Contains(line, "}}") && !strings.Contains(line, "{{secure:") {
					// Only flag if it's not part of something else (template syntax etc)
					// We allow }} in normal text, only flag in context of plain blocks
				}
				continue
			}

			// Check if it closes on the same line (single-line block)
			after := line[idx+8:] // after "{{plain:"
			closeIdx := strings.Index(after, "}}")
			if closeIdx >= 0 {
				// Single-line: {{plain:content}} — valid
				continue
			}

			// Multiline block: {{plain: must be the entire trimmed content of this line
			if trimmed != "{{plain:" {
				return fmt.Sprintf("Line %d: multiline {{plain: must be on its own line with no other text.", lineNum)
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
		return fmt.Sprintf("Line %d: unclosed {{plain: block — missing closing }}.", blockStartLine)
	}

	return ""
}
