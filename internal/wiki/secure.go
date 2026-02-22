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
		encoded, err := sc.Encrypt(captures[1])
		if err != nil {
			encryptErr = err
			return match
		}
		return fmt.Sprintf("{{secure:%s}}", encoded)
	})
	return result, encryptErr
}
