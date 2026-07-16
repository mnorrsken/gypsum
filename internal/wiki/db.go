package wiki

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

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
		CREATE TABLE IF NOT EXISTS app_config (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
		CREATE VIRTUAL TABLE IF NOT EXISTS fts_pages USING fts5(
			slug UNINDEXED,
			title,
			content,
			tokenize='unicode61'
		);
		CREATE VIRTUAL TABLE IF NOT EXISTS fts_skills USING fts5(
			slug UNINDEXED,
			title,
			tags,
			content,
			tokenize='unicode61'
		);
		CREATE VIRTUAL TABLE IF NOT EXISTS fts_notes USING fts5(
			slug UNINDEXED,
			title,
			content,
			archived UNINDEXED,
			tokenize='unicode61'
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

// ---------- App config ----------

// GetConfig returns the value for key and whether it was found.
func (d *DB) GetConfig(key string) (string, bool, error) {
	var value string
	err := d.db.QueryRow("SELECT value FROM app_config WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// SetConfig stores (or replaces) the value for key.
func (d *DB) SetConfig(key, value string) error {
	_, err := d.db.Exec(
		"INSERT INTO app_config(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, value,
	)
	return err
}

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

// ---------- Full-text search (FTS5) operations ----------

// FTSSearchResult is a single search hit from the FTS index.
type FTSSearchResult struct {
	Slug     string
	Title    string
	Snippets []string // context snippets around matched terms
}

// IndexPage inserts or replaces a page in the FTS index.
func (d *DB) IndexPage(slug, title, content string) error {
	// FTS5 does not support INSERT OR REPLACE, so delete first.
	d.db.Exec("DELETE FROM fts_pages WHERE slug = ?", slug)
	_, err := d.db.Exec(
		"INSERT INTO fts_pages (slug, title, content) VALUES (?, ?, ?)",
		slug, title, content,
	)
	return err
}

// RemovePage removes a page from the FTS index.
func (d *DB) RemovePage(slug string) error {
	_, err := d.db.Exec("DELETE FROM fts_pages WHERE slug = ?", slug)
	return err
}

// ReindexPages replaces the entire FTS index with the given pages.
func (d *DB) ReindexPages(pages []Page) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM fts_pages"); err != nil {
		return err
	}
	stmt, err := tx.Prepare("INSERT INTO fts_pages (slug, title, content) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, p := range pages {
		if _, err := stmt.Exec(p.Slug, p.Title, p.Content); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SearchFTS performs a full-text search and returns results with context snippets.
// Each result includes up to 3 snippets showing where terms matched in the content.
func (d *DB) SearchFTS(query string) ([]FTSSearchResult, error) {
	// Build an FTS5 query: each term gets a prefix match (*) so "kube" matches "kubernetes".
	ftsQuery := buildFTSQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}

	rows, err := d.db.Query(`
		SELECT
			slug,
			title,
			snippet(fts_pages, 2, '<<', '>>', '…', 48) AS snip1,
			snippet(fts_pages, 1, '<<', '>>', '…', 48) AS title_snip
		FROM fts_pages
		WHERE fts_pages MATCH ?
		ORDER BY rank
		LIMIT 50
	`, ftsQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []FTSSearchResult
	for rows.Next() {
		var slug, title, contentSnip, titleSnip string
		if err := rows.Scan(&slug, &title, &contentSnip, &titleSnip); err != nil {
			return nil, err
		}
		var snippets []string
		if contentSnip != "" {
			snippets = append(snippets, contentSnip)
		}
		results = append(results, FTSSearchResult{
			Slug:     slug,
			Title:    title,
			Snippets: snippets,
		})
	}
	return results, rows.Err()
}

// ---------- Skill FTS5 operations ----------

// FTSSkillEntry holds the data needed to index a skill.
type FTSSkillEntry struct {
	Slug    string
	Title   string
	Tags    string
	Content string
}

// IndexSkill inserts or replaces a skill in the FTS index.
func (d *DB) IndexSkill(slug, title, tags, content string) error {
	d.db.Exec("DELETE FROM fts_skills WHERE slug = ?", slug)
	_, err := d.db.Exec(
		"INSERT INTO fts_skills (slug, title, tags, content) VALUES (?, ?, ?, ?)",
		slug, title, tags, content,
	)
	return err
}

// RemoveSkill removes a skill from the FTS index.
func (d *DB) RemoveSkill(slug string) error {
	_, err := d.db.Exec("DELETE FROM fts_skills WHERE slug = ?", slug)
	return err
}

// ReindexSkills replaces the entire skill FTS index.
func (d *DB) ReindexSkills(skills []FTSSkillEntry) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM fts_skills"); err != nil {
		return err
	}
	stmt, err := tx.Prepare("INSERT INTO fts_skills (slug, title, tags, content) VALUES (?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, s := range skills {
		if _, err := stmt.Exec(s.Slug, s.Title, s.Tags, s.Content); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SearchFTSSkills performs a full-text search across skills with tag boosting.
// BM25 weights: slug=0 (unindexed), title=10, tags=15, content=1.
func (d *DB) SearchFTSSkills(query string) ([]FTSSearchResult, error) {
	ftsQuery := buildFTSQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}

	rows, err := d.db.Query(`
		SELECT
			slug,
			title,
			snippet(fts_skills, 3, '<<', '>>', '…', 48) AS snip1,
			snippet(fts_skills, 2, '<<', '>>', '…', 48) AS tag_snip
		FROM fts_skills
		WHERE fts_skills MATCH ?
		ORDER BY bm25(fts_skills, 0, 10.0, 15.0, 1.0)
		LIMIT 50
	`, ftsQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []FTSSearchResult
	for rows.Next() {
		var slug, title, contentSnip, tagSnip string
		if err := rows.Scan(&slug, &title, &contentSnip, &tagSnip); err != nil {
			return nil, err
		}
		var snippets []string
		if contentSnip != "" {
			snippets = append(snippets, contentSnip)
		}
		results = append(results, FTSSearchResult{
			Slug:     slug,
			Title:    title,
			Snippets: snippets,
		})
	}
	return results, rows.Err()
}

// ---------- Note FTS5 operations ----------

// FTSNoteEntry holds the data needed to index a note.
type FTSNoteEntry struct {
	ID       string
	Title    string
	Content  string
	Archived bool
}

// FTSNoteResult is a single search hit from the notes FTS index.
type FTSNoteResult struct {
	ID       string
	Title    string
	Snippets []string
	Archived bool
}

// archivedFlag maps a bool to the text value stored in the UNINDEXED column.
func archivedFlag(archived bool) string {
	if archived {
		return "1"
	}
	return "0"
}

// IndexNote inserts or replaces a note in the FTS index.
func (d *DB) IndexNote(id, title, content string, archived bool) error {
	d.db.Exec("DELETE FROM fts_notes WHERE slug = ?", id)
	_, err := d.db.Exec(
		"INSERT INTO fts_notes (slug, title, content, archived) VALUES (?, ?, ?, ?)",
		id, title, content, archivedFlag(archived),
	)
	return err
}

// RemoveNote removes a note from the FTS index.
func (d *DB) RemoveNote(id string) error {
	_, err := d.db.Exec("DELETE FROM fts_notes WHERE slug = ?", id)
	return err
}

// ReindexNotes replaces the entire note FTS index.
func (d *DB) ReindexNotes(notes []FTSNoteEntry) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM fts_notes"); err != nil {
		return err
	}
	stmt, err := tx.Prepare("INSERT INTO fts_notes (slug, title, content, archived) VALUES (?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, n := range notes {
		if _, err := stmt.Exec(n.ID, n.Title, n.Content, archivedFlag(n.Archived)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SearchFTSNotes performs a full-text search across notes. Archived notes are
// excluded unless includeArchived is true.
func (d *DB) SearchFTSNotes(query string, includeArchived bool) ([]FTSNoteResult, error) {
	ftsQuery := buildFTSQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}

	sqlStr := `
		SELECT
			slug,
			title,
			archived,
			snippet(fts_notes, 2, '<<', '>>', '…', 48) AS snip
		FROM fts_notes
		WHERE fts_notes MATCH ?`
	if !includeArchived {
		sqlStr += " AND archived = '0'"
	}
	sqlStr += " ORDER BY rank LIMIT 50"

	rows, err := d.db.Query(sqlStr, ftsQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []FTSNoteResult
	for rows.Next() {
		var slug, title, archived, contentSnip string
		if err := rows.Scan(&slug, &title, &archived, &contentSnip); err != nil {
			return nil, err
		}
		var snippets []string
		if contentSnip != "" {
			snippets = append(snippets, contentSnip)
		}
		results = append(results, FTSNoteResult{
			ID:       slug,
			Title:    title,
			Snippets: snippets,
			Archived: archived == "1",
		})
	}
	return results, rows.Err()
}

// buildFTSQuery converts a user query into an FTS5 MATCH expression.
// "tokens lösen" → "tokens* AND lösen*" (prefix matching on each term).
func buildFTSQuery(query string) string {
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, query)
	words := strings.Fields(strings.TrimSpace(cleaned))
	if len(words) == 0 {
		return ""
	}
	// Deduplicate.
	seen := make(map[string]bool)
	var terms []string
	for _, w := range words {
		low := strings.ToLower(w)
		if !seen[low] {
			seen[low] = true
			terms = append(terms, low)
		}
	}
	// Each term becomes a prefix query joined with AND.
	for i, t := range terms {
		// Quote the term and append * for prefix matching.
		terms[i] = `"` + t + `"` + "*"
	}
	return strings.Join(terms, " AND ")
}
