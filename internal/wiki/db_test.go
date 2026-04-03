package wiki

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// openTestDB creates a temporary database for testing.
func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := OpenDB(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// --- Share tests ---

func TestCreateAndGetShare(t *testing.T) {
	db := openTestDB(t)

	share, err := db.CreateShare("My_Page")
	if err != nil {
		t.Fatalf("CreateShare: %v", err)
	}
	if share.Slug != "My_Page" {
		t.Fatalf("slug = %q, want My_Page", share.Slug)
	}
	if len(share.Token) != 64 { // 32 bytes = 64 hex chars
		t.Fatalf("token length = %d, want 64", len(share.Token))
	}

	got, err := db.GetShare("My_Page")
	if err != nil {
		t.Fatalf("GetShare: %v", err)
	}
	if got == nil {
		t.Fatal("GetShare returned nil for existing share")
	}
	if got.Token != share.Token {
		t.Fatalf("token mismatch: got %q, want %q", got.Token, share.Token)
	}
}

func TestGetShareByToken(t *testing.T) {
	db := openTestDB(t)

	share, err := db.CreateShare("Test_Page")
	if err != nil {
		t.Fatalf("CreateShare: %v", err)
	}

	got, err := db.GetShareByToken(share.Token)
	if err != nil {
		t.Fatalf("GetShareByToken: %v", err)
	}
	if got == nil {
		t.Fatal("GetShareByToken returned nil for existing token")
	}
	if got.Slug != "Test_Page" {
		t.Fatalf("slug = %q, want Test_Page", got.Slug)
	}
}

func TestGetShareByTokenNotFound(t *testing.T) {
	db := openTestDB(t)

	got, err := db.GetShareByToken("nonexistent-token")
	if err != nil {
		t.Fatalf("GetShareByToken: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for nonexistent token")
	}
}

func TestGetShareNotFound(t *testing.T) {
	db := openTestDB(t)

	got, err := db.GetShare("Nonexistent")
	if err != nil {
		t.Fatalf("GetShare: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for nonexistent slug")
	}
}

func TestDeleteShare(t *testing.T) {
	db := openTestDB(t)

	if _, err := db.CreateShare("Delete_Me"); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}

	if err := db.DeleteShare("Delete_Me"); err != nil {
		t.Fatalf("DeleteShare: %v", err)
	}

	got, err := db.GetShare("Delete_Me")
	if err != nil {
		t.Fatalf("GetShare: %v", err)
	}
	if got != nil {
		t.Fatal("share should be nil after delete")
	}
}

func TestDeleteShareNonexistent(t *testing.T) {
	db := openTestDB(t)

	// Should not error even if no row exists.
	if err := db.DeleteShare("Never_Created"); err != nil {
		t.Fatalf("DeleteShare: %v", err)
	}
}

func TestCreateShareReplacesExisting(t *testing.T) {
	db := openTestDB(t)

	first, err := db.CreateShare("Page")
	if err != nil {
		t.Fatalf("CreateShare (1st): %v", err)
	}

	second, err := db.CreateShare("Page")
	if err != nil {
		t.Fatalf("CreateShare (2nd): %v", err)
	}

	if first.Token == second.Token {
		t.Fatal("regenerated share should have a new token")
	}

	// Old token should no longer resolve.
	got, err := db.GetShareByToken(first.Token)
	if err != nil {
		t.Fatalf("GetShareByToken (old): %v", err)
	}
	if got != nil {
		t.Fatal("old token should not resolve after replacement")
	}

	// New token should resolve.
	got, err = db.GetShareByToken(second.Token)
	if err != nil {
		t.Fatalf("GetShareByToken (new): %v", err)
	}
	if got == nil || got.Token != second.Token {
		t.Fatal("new token should resolve")
	}
}

func TestMultipleSharesDifferentPages(t *testing.T) {
	db := openTestDB(t)

	s1, err := db.CreateShare("Page_A")
	if err != nil {
		t.Fatalf("CreateShare A: %v", err)
	}
	s2, err := db.CreateShare("Page_B")
	if err != nil {
		t.Fatalf("CreateShare B: %v", err)
	}

	if s1.Token == s2.Token {
		t.Fatal("different pages should get different tokens")
	}

	gotA, _ := db.GetShare("Page_A")
	gotB, _ := db.GetShare("Page_B")
	if gotA == nil || gotB == nil {
		t.Fatal("both shares should exist")
	}
	if gotA.Token != s1.Token || gotB.Token != s2.Token {
		t.Fatal("token mismatch on lookup")
	}
}

// --- OAuth token tests ---

func TestSaveAndValidateOAuthToken(t *testing.T) {
	db := openTestDB(t)

	token := "test-token-abc123"
	if err := db.SaveOAuthToken(token, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("SaveOAuthToken: %v", err)
	}

	if !db.ValidateOAuthToken(token) {
		t.Fatal("expected token to be valid")
	}
}

func TestValidateOAuthTokenNotFound(t *testing.T) {
	db := openTestDB(t)

	if db.ValidateOAuthToken("nonexistent") {
		t.Fatal("expected false for nonexistent token")
	}
}

func TestValidateOAuthTokenExpired(t *testing.T) {
	db := openTestDB(t)

	token := "expired-token"
	if err := db.SaveOAuthToken(token, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("SaveOAuthToken: %v", err)
	}

	if db.ValidateOAuthToken(token) {
		t.Fatal("expected false for expired token")
	}

	// Should also have been cleaned up — a second call should also return false.
	if db.ValidateOAuthToken(token) {
		t.Fatal("expired token should have been deleted on first validate")
	}
}

func TestPurgeExpiredOAuthTokens(t *testing.T) {
	db := openTestDB(t)

	// Insert 2 expired and 1 valid token.
	db.SaveOAuthToken("expired-1", time.Now().Add(-time.Hour))
	db.SaveOAuthToken("expired-2", time.Now().Add(-2*time.Hour))
	db.SaveOAuthToken("valid-1", time.Now().Add(time.Hour))

	n, err := db.PurgeExpiredOAuthTokens()
	if err != nil {
		t.Fatalf("PurgeExpiredOAuthTokens: %v", err)
	}
	if n != 2 {
		t.Fatalf("purged %d tokens, want 2", n)
	}

	if !db.ValidateOAuthToken("valid-1") {
		t.Fatal("valid token should survive purge")
	}
	if db.ValidateOAuthToken("expired-1") {
		t.Fatal("expired-1 should be gone")
	}
}

func TestSaveOAuthTokenOverwrite(t *testing.T) {
	db := openTestDB(t)

	token := "overwrite-me"
	db.SaveOAuthToken(token, time.Now().Add(-time.Hour))
	if db.ValidateOAuthToken(token) {
		t.Fatal("should be expired initially")
	}

	// Overwrite with a future expiry.
	db.SaveOAuthToken(token, time.Now().Add(time.Hour))
	if !db.ValidateOAuthToken(token) {
		t.Fatal("should be valid after overwrite")
	}
}

// --- Migration tests ---

func TestMigrateOAuthTokensFromJSON(t *testing.T) {
	dir := t.TempDir()

	validExpiry := time.Now().Add(time.Hour).Truncate(time.Second)
	expiredExpiry := time.Now().Add(-time.Hour).Truncate(time.Second)

	entries := []struct {
		Token  string    `json:"token"`
		Expiry time.Time `json:"expiry"`
	}{
		{"valid-token", validExpiry},
		{"expired-token", expiredExpiry},
	}
	data, _ := json.Marshal(entries)
	jsonPath := filepath.Join(dir, "oauth_tokens.json")
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		t.Fatalf("write JSON: %v", err)
	}

	db, err := OpenDB(dir)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	// Valid token should have been migrated.
	if !db.ValidateOAuthToken("valid-token") {
		t.Fatal("valid-token should have been migrated")
	}

	// Expired token should not be present.
	if db.ValidateOAuthToken("expired-token") {
		t.Fatal("expired-token should not have been migrated")
	}

	// JSON file should be removed.
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Fatal("oauth_tokens.json should be removed after migration")
	}
}

func TestMigrateNoJSONFile(t *testing.T) {
	// OpenDB should not fail when there is no oauth_tokens.json.
	db, err := OpenDB(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	db.Close()
}

func TestOpenDBCreatesFile(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDB(dir)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	db.Close()

	if _, err := os.Stat(filepath.Join(dir, "gypsum.db")); err != nil {
		t.Fatalf("database file should exist: %v", err)
	}
}

// --- FTS5 tests ---

func TestFTSIndexAndSearch(t *testing.T) {
	db := openTestDB(t)

	if err := db.IndexPage("Go_Guide", "Go Guide", "A comprehensive guide to Go programming"); err != nil {
		t.Fatalf("IndexPage: %v", err)
	}
	if err := db.IndexPage("Rust_Notes", "Rust Notes", "Notes about the Rust programming language"); err != nil {
		t.Fatalf("IndexPage: %v", err)
	}

	results, err := db.SearchFTS("go programming")
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	// "Go Guide" should match because both terms appear.
	found := false
	for _, r := range results {
		if r.Slug == "Go_Guide" {
			found = true
			if len(r.Snippets) == 0 {
				t.Fatal("expected at least one snippet")
			}
		}
	}
	if !found {
		t.Fatal("expected Go_Guide in results")
	}
}

func TestFTSPrefixMatch(t *testing.T) {
	db := openTestDB(t)

	db.IndexPage("Kubernetes_Guide", "Kubernetes Guide", "deploying containers with kubectl")

	results, err := db.SearchFTS("kube")
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for prefix match, got %d", len(results))
	}
	if results[0].Slug != "Kubernetes_Guide" {
		t.Fatalf("unexpected slug: %s", results[0].Slug)
	}
}

func TestFTSEmptyQuery(t *testing.T) {
	db := openTestDB(t)

	db.IndexPage("Page", "Page", "content")

	results, err := db.SearchFTS("")
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("empty query should return 0 results, got %d", len(results))
	}
}

func TestFTSNoResults(t *testing.T) {
	db := openTestDB(t)

	db.IndexPage("Page", "Page", "content here")

	results, err := db.SearchFTS("nonexistent")
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestFTSRemovePage(t *testing.T) {
	db := openTestDB(t)

	db.IndexPage("Temp", "Temp", "temporary page content")

	results, _ := db.SearchFTS("temporary")
	if len(results) != 1 {
		t.Fatalf("expected 1 result before delete, got %d", len(results))
	}

	if err := db.RemovePage("Temp"); err != nil {
		t.Fatalf("RemovePage: %v", err)
	}

	results, _ = db.SearchFTS("temporary")
	if len(results) != 0 {
		t.Fatalf("expected 0 results after delete, got %d", len(results))
	}
}

func TestFTSReindexPages(t *testing.T) {
	db := openTestDB(t)

	db.IndexPage("Old", "Old", "this should be removed on reindex")

	pages := []Page{
		{Slug: "New_A", Title: "New A", Content: "alpha content"},
		{Slug: "New_B", Title: "New B", Content: "beta content"},
	}
	if err := db.ReindexPages(pages); err != nil {
		t.Fatalf("ReindexPages: %v", err)
	}

	// Old page should be gone.
	results, _ := db.SearchFTS("removed")
	if len(results) != 0 {
		t.Fatalf("expected old page gone, got %d results", len(results))
	}

	// New pages should be findable.
	results, _ = db.SearchFTS("alpha")
	if len(results) != 1 || results[0].Slug != "New_A" {
		t.Fatalf("expected New_A, got %v", results)
	}
}

func TestFTSUpdatePage(t *testing.T) {
	db := openTestDB(t)

	db.IndexPage("Page", "Page", "original content with apples")

	results, _ := db.SearchFTS("apples")
	if len(results) != 1 {
		t.Fatalf("expected 1 result for original content, got %d", len(results))
	}

	// Update the page content.
	db.IndexPage("Page", "Page", "updated content with oranges")

	results, _ = db.SearchFTS("apples")
	if len(results) != 0 {
		t.Fatalf("expected 0 results for old content, got %d", len(results))
	}
	results, _ = db.SearchFTS("oranges")
	if len(results) != 1 {
		t.Fatalf("expected 1 result for updated content, got %d", len(results))
	}
}

func TestBuildFTSQuery(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"go", `"go"*`},
		{"go programming", `"go"* AND "programming"*`},
		{"tokens & lösen", `"tokens"* AND "lösen"*`},
		{"Go go GO", `"go"*`}, // dedup + lowercase
	}
	for _, tt := range tests {
		got := buildFTSQuery(tt.input)
		if got != tt.want {
			t.Errorf("buildFTSQuery(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestPageStoreSearchWithFTS(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t)
	store := NewPageStore(dir)

	// Save pages before wiring DB (simulating pre-existing data).
	store.Save("Zoo", "Animals in the wild")
	store.Save("Alpha", "Contains keyword: gypsum rocks")

	// Wire DB — triggers reindex of existing pages.
	store.SetDB(db)

	results, err := store.Search("gypsum")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Slug != "Alpha" {
		t.Fatalf("unexpected slug: %s", results[0].Slug)
	}
	if len(results[0].Snippets) == 0 {
		t.Fatal("expected FTS snippets")
	}
}

func TestPageStoreDeleteRemovesFromIndex(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t)
	store := NewPageStore(dir)
	store.SetDB(db)

	store.Save("Temp_Page", "deletable content here")

	results, _ := store.Search("deletable")
	if len(results) != 1 {
		t.Fatalf("expected 1 result before delete, got %d", len(results))
	}

	if err := store.Delete("Temp_Page"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	results, _ = store.Search("deletable")
	if len(results) != 0 {
		t.Fatalf("expected 0 results after delete, got %d", len(results))
	}
}

func TestPageStoreSaveUpdatesIndex(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t)
	store := NewPageStore(dir)
	store.SetDB(db)

	store.Save("Notes", "first version about cats")

	results, _ := store.Search("cats")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// Update the page.
	store.Save("Notes", "second version about dogs")

	results, _ = store.Search("cats")
	if len(results) != 0 {
		t.Fatalf("expected 0 results for old content, got %d", len(results))
	}
	results, _ = store.Search("dogs")
	if len(results) != 1 {
		t.Fatalf("expected 1 result for updated content, got %d", len(results))
	}
}
