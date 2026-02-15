package wiki

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/scrypt"
)

var ErrWrongPassword = errors.New("wrong password or corrupted secure block")

type encryptedBlock struct {
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type SecureStore struct {
	secureDir string
}

func NewSecureStore(secureDir string) *SecureStore {
	return &SecureStore{secureDir: secureDir}
}

func (s *SecureStore) Exists(pageSlug, blockID string) bool {
	_, err := os.Stat(s.pathFor(pageSlug, blockID))
	return err == nil
}

func (s *SecureStore) Load(pageSlug, blockID, password string) (string, error) {
	path := s.pathFor(pageSlug, blockID)
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}

	var block encryptedBlock
	if err := json.Unmarshal(body, &block); err != nil {
		return "", err
	}

	salt, err := base64.StdEncoding.DecodeString(block.Salt)
	if err != nil {
		return "", err
	}
	nonce, err := base64.StdEncoding.DecodeString(block.Nonce)
	if err != nil {
		return "", err
	}
	cipherBytes, err := base64.StdEncoding.DecodeString(block.Ciphertext)
	if err != nil {
		return "", err
	}

	key, err := deriveKey(password, salt)
	if err != nil {
		return "", err
	}

	plain, err := decrypt(key, nonce, cipherBytes)
	if err != nil {
		return "", ErrWrongPassword
	}

	return string(plain), nil
}

func (s *SecureStore) Save(pageSlug, blockID, password, content string) error {
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return err
	}
	key, err := deriveKey(password, salt)
	if err != nil {
		return err
	}
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}

	encrypted, err := encrypt(key, nonce, []byte(content))
	if err != nil {
		return err
	}

	body, err := json.MarshalIndent(encryptedBlock{
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(encrypted),
	}, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.pathFor(pageSlug, blockID), body, 0o600)
}

func (s *SecureStore) pathFor(pageSlug, blockID string) string {
	filename := fmt.Sprintf("%s__%s.json", pageSlug, blockID)
	return filepath.Join(s.secureDir, filename)
}

func deriveKey(password string, salt []byte) ([]byte, error) {
	return scrypt.Key([]byte(password), salt, 32768, 8, 1, 32)
}

func encrypt(key, nonce, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return aead.Seal(nil, nonce, plaintext, nil), nil
}

func decrypt(key, nonce, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, nonce, ciphertext, nil)
}
