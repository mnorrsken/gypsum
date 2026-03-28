package wiki

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps a SQLite database used for share links and OAuth token storage.
type DB struct {
	db *sql.DB
}

// OpenDB opens (or creates) the SQLite database in dataDir/gypsum.db.
func OpenDB(dataDir string) (*DB, error) {
	dbPath := filepath.Join(dataDir, "gypsum.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS shares (
			slug       TEXT PRIMARY KEY,
			token      TEXT NOT NULL UNIQUE,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS oauth_tokens (
			token      TEXT PRIMARY KEY,
			expires_at DATETIME NOT NULL
		);
	`); err != nil {
		db.Close()
		return nil, err
	}

	d := &DB{db: db}
	d.migrateOAuthTokens(dataDir)
	return d, nil
}

// Close closes the underlying database connection.
func (d *DB) Close() error { return d.db.Close() }

// ---------- Share operations ----------

// Share represents a public share link for a wiki page.
type Share struct {
	Slug      string
	Token     string
	CreatedAt time.Time
}

// GetShare returns the share for a page slug, or nil if none exists.
func (d *DB) GetShare(slug string) (*Share, error) {
	var s Share
	err := d.db.QueryRow(
		"SELECT slug, token, created_at FROM shares WHERE slug = ?", slug,
	).Scan(&s.Slug, &s.Token, &s.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &s, err
}

// GetShareByToken returns the share for a given token, or nil if not found.
func (d *DB) GetShareByToken(token string) (*Share, error) {
	var s Share
	err := d.db.QueryRow(
		"SELECT slug, token, created_at FROM shares WHERE token = ?", token,
	).Scan(&s.Slug, &s.Token, &s.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &s, err
}

// CreateShare creates (or replaces) a share link for the given page slug.
func (d *DB) CreateShare(slug string) (*Share, error) {
	token := generateShareToken()
	now := time.Now()
	_, err := d.db.Exec(
		"INSERT OR REPLACE INTO shares (slug, token, created_at) VALUES (?, ?, ?)",
		slug, token, now,
	)
	if err != nil {
		return nil, err
	}
	return &Share{Slug: slug, Token: token, CreatedAt: now}, nil
}

// DeleteShare removes the share link for a page slug.
func (d *DB) DeleteShare(slug string) error {
	_, err := d.db.Exec("DELETE FROM shares WHERE slug = ?", slug)
	return err
}

func generateShareToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---------- OAuth token operations ----------

// SaveOAuthToken stores an access token with its expiry time.
func (d *DB) SaveOAuthToken(token string, expiresAt time.Time) error {
	_, err := d.db.Exec(
		"INSERT OR REPLACE INTO oauth_tokens (token, expires_at) VALUES (?, ?)",
		token, expiresAt,
	)
	return err
}

// ValidateOAuthToken returns true if the token exists and has not expired.
func (d *DB) ValidateOAuthToken(token string) bool {
	var expiresAt time.Time
	err := d.db.QueryRow(
		"SELECT expires_at FROM oauth_tokens WHERE token = ?", token,
	).Scan(&expiresAt)
	if err != nil {
		return false
	}
	if time.Now().After(expiresAt) {
		d.db.Exec("DELETE FROM oauth_tokens WHERE token = ?", token)
		return false
	}
	return true
}

// PurgeExpiredOAuthTokens removes all expired tokens and returns the count.
func (d *DB) PurgeExpiredOAuthTokens() (int64, error) {
	res, err := d.db.Exec("DELETE FROM oauth_tokens WHERE expires_at < ?", time.Now())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// migrateOAuthTokens imports tokens from the legacy oauth_tokens.json file
// into SQLite and removes the JSON file afterwards.
func (d *DB) migrateOAuthTokens(dataDir string) {
	jsonPath := filepath.Join(dataDir, "oauth_tokens.json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return
	}

	var entries []struct {
		Token  string    `json:"token"`
		Expiry time.Time `json:"expiry"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		log.Printf("db: failed to parse %s for migration: %v", jsonPath, err)
		return
	}

	now := time.Now()
	migrated := 0
	for _, e := range entries {
		if now.Before(e.Expiry) {
			if err := d.SaveOAuthToken(e.Token, e.Expiry); err == nil {
				migrated++
			}
		}
	}

	if err := os.Remove(jsonPath); err != nil {
		log.Printf("db: failed to remove %s after migration: %v", jsonPath, err)
	} else if migrated > 0 {
		log.Printf("db: migrated %d OAuth token(s) from JSON to SQLite", migrated)
	}
}
