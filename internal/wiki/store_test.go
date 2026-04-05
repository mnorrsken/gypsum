package wiki

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPageStoreSaveLoadAndMissing(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	const slug = "Home"
	const content = "# Welcome\nHello"
	if err := store.Save(KindPage,slug, content); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	page, err := store.Load(KindPage,slug)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if page.Slug != slug || page.Content != content {
		t.Fatalf("loaded page mismatch: %#v", page)
	}

	_, err = store.Load(KindPage,"Does_Not_Exist")
	if !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("expected ErrPageNotFound, got %v", err)
	}
}

func TestPageStoreListAndSearch(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	if err := store.Save(KindPage,"Zoo", "Animals"); err != nil {
		t.Fatalf("Save Zoo failed: %v", err)
	}
	if err := store.Save(KindPage,"Alpha", "Contains keyword: gypsum"); err != nil {
		t.Fatalf("Save Alpha failed: %v", err)
	}

	links, err := store.List(KindPage)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %d", len(links))
	}
	if links[0].Slug != "Alpha" || links[1].Slug != "Zoo" {
		t.Fatalf("links not sorted as expected: %#v", links)
	}

	results, err := store.Search(KindPage,"GyPsuM")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Slug != "Alpha" {
		t.Fatalf("unexpected search result: %#v", results[0])
	}
	if results[0].Excerpt == "" {
		t.Fatalf("expected non-empty excerpt")
	}
}

func TestPageStoreListSkipsSpecialFiles(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	_ = store.Save(KindPage,"Normal", "content")
	_ = store.Save(KindPage,"_favorites", "[[Normal]]")

	links, err := store.List(KindPage)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("expected 1 link (skipping _favorites), got %d", len(links))
	}
	if links[0].Slug != "Normal" {
		t.Fatalf("unexpected slug: %s", links[0].Slug)
	}
}

func TestPageStoreRecentPages(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	_ = store.Save(KindPage,"First", "first page")
	_ = store.Save(KindPage,"Second", "second page")
	_ = store.Save(KindPage,"Third", "third page")

	recent, err := store.RecentPages(2)
	if err != nil {
		t.Fatalf("RecentPages failed: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("expected 2 recent pages, got %d", len(recent))
	}
	// All three were saved nearly simultaneously, so just verify count is capped
}

func TestPageStoreRecentPagesSkipsSpecial(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	_ = store.Save(KindPage,"Page", "content")
	_ = store.Save(KindPage,"_favorites", "[[Page]]")

	recent, err := store.RecentPages(10)
	if err != nil {
		t.Fatalf("RecentPages failed: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("expected 1 (skipping _favorites), got %d", len(recent))
	}
}

func TestPageStoreLoadFavorites(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	_ = store.Save(KindPage,"_favorites", "[[Home]]\n- [[Notes]]\n[[Scratch Pad]]")

	favs, err := store.LoadFavorites()
	if err != nil {
		t.Fatalf("LoadFavorites failed: %v", err)
	}
	if len(favs) != 3 {
		t.Fatalf("expected 3 favorites, got %d", len(favs))
	}
	if favs[0].Title != "Home" || favs[0].Slug != "Home" {
		t.Fatalf("unexpected first fav: %#v", favs[0])
	}
	if favs[1].Title != "Notes" {
		t.Fatalf("unexpected second fav: %#v", favs[1])
	}
	if favs[2].Title != "Scratch Pad" || favs[2].Slug != "Scratch_Pad" {
		t.Fatalf("unexpected third fav: %#v", favs[2])
	}
}

func TestPageStoreLoadFavoritesMissing(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	favs, err := store.LoadFavorites()
	if err != nil {
		t.Fatalf("LoadFavorites should not error on missing file: %v", err)
	}
	if len(favs) != 0 {
		t.Fatalf("expected 0 favorites, got %d", len(favs))
	}
}

func TestPageStoreSearchEmptyQuery(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	_ = store.Save(KindPage,"Page", "content")

	results, err := store.Search(KindPage,"")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("empty query should return 0 results, got %d", len(results))
	}
}

func TestPageStoreSearchMatchesTitle(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	_ = store.Save(KindPage,"Kubernetes_Guide", "some content about pods")

	results, err := store.Search(KindPage,"kubernetes")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result matching title, got %d", len(results))
	}
}

func TestPageStoreSearchPrefixAndRelaxed(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	// Slug with double underscore (& stripped during creation), simulates "Tokens & Lösenord"
	_ = store.Save(KindPage,"Tokens__Lösenord", "page about tokens and passwords")
	_ = store.Save(KindPage,"Other_Page", "unrelated content")

	// "tokens & lösen" should match: & is stripped, "lösen" prefix-matches "lösenord"
	results, err := store.Search(KindPage,"tokens & lösen")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Slug != "Tokens__Lösenord" {
		t.Fatalf("unexpected result slug: %s", results[0].Slug)
	}
}

func TestPageStoreSearchRelevanceOrder(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	_ = store.Save(KindPage,"Go_Programming", "a guide to go programming")
	_ = store.Save(KindPage,"Notes", "contains the word go somewhere in content")

	results, err := store.Search(KindPage,"go programming")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) < 1 {
		t.Fatal("expected at least 1 result")
	}
	// Title match with both terms should rank first
	if results[0].Slug != "Go_Programming" {
		t.Fatalf("expected Go_Programming first, got %s", results[0].Slug)
	}
}

func TestPageStoreSearchPartialTerm(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	_ = store.Save(KindPage,"Kubernetes_Guide", "deploying containers")

	// "kube" should prefix-match "kubernetes"
	results, err := store.Search(KindPage,"kube")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for prefix match, got %d", len(results))
	}
}

func TestPageStorePagePath(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	got := store.PagePath("My_Page")
	want := filepath.Join(dir, "My_Page.md")
	if got != want {
		t.Fatalf("PagePath = %q, want %q", got, want)
	}
}

func TestPageStoreImagePath(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	got := store.ImagePath("photo.png")
	imagesDir := filepath.Join(filepath.Dir(dir), "images")
	want := filepath.Join(imagesDir, "photo.png")
	if got != want {
		t.Fatalf("ImagePath = %q, want %q", got, want)
	}
}

func TestPageStoreListImagesEmpty(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	images, err := store.ListImages()
	if err != nil {
		t.Fatalf("ListImages failed: %v", err)
	}
	if len(images) != 0 {
		t.Fatalf("expected 0 images, got %d", len(images))
	}
}

func TestPageStoreListImagesWithFiles(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	// Create test images in the images directory
	imagesDir := store.ImagesDir()
	_ = os.WriteFile(filepath.Join(imagesDir, "test.png"), []byte("fake png"), 0o644)
	_ = os.WriteFile(filepath.Join(imagesDir, "photo.jpg"), []byte("fake jpg"), 0o644)
	_ = os.WriteFile(filepath.Join(imagesDir, "readme.txt"), []byte("not an image"), 0o644)

	images, err := store.ListImages()
	if err != nil {
		t.Fatalf("ListImages failed: %v", err)
	}
	// Should only include image files, not .txt
	if len(images) != 2 {
		t.Fatalf("expected 2 images (excluding .txt), got %d", len(images))
	}
}

func TestPageStoreListImagesTracksUsage(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	imagesDir := store.ImagesDir()
	_ = os.WriteFile(filepath.Join(imagesDir, "used.png"), []byte("img"), 0o644)
	_ = os.WriteFile(filepath.Join(imagesDir, "unused.png"), []byte("img"), 0o644)

	// Create a page that references one image
	_ = store.Save(KindPage,"MyPage", "Some text ![alt](/images/used.png) more text")

	images, err := store.ListImages()
	if err != nil {
		t.Fatalf("ListImages failed: %v", err)
	}

	for _, img := range images {
		if img.Name == "used.png" {
			if len(img.UsedBy) != 1 || img.UsedBy[0] != "MyPage" {
				t.Fatalf("expected used.png UsedBy=[MyPage], got %v", img.UsedBy)
			}
		}
		if img.Name == "unused.png" {
			if len(img.UsedBy) != 0 {
				t.Fatalf("expected unused.png UsedBy=[], got %v", img.UsedBy)
			}
		}
	}
}

func TestExcerptForTermsLongContent(t *testing.T) {
	content := strings.Repeat("x", 200) + "KEYWORD" + strings.Repeat("y", 200)
	excerpt := excerptForTerms(content, []string{"keyword"})
	if !strings.Contains(strings.ToLower(excerpt), "keyword") {
		t.Fatalf("excerpt should contain keyword: %s", excerpt)
	}
	if !strings.HasPrefix(excerpt, "...") {
		t.Fatal("excerpt should start with ... for mid-content match")
	}
	if !strings.HasSuffix(excerpt, "...") {
		t.Fatal("excerpt should end with ... for mid-content match")
	}
}

func TestExcerptForTermsNoMatch(t *testing.T) {
	content := strings.Repeat("abc ", 100)
	excerpt := excerptForTerms(content, []string{"zzz"})
	if len(excerpt) > 184 { // 180 + "..."
		t.Fatalf("excerpt too long for no-match case: len=%d", len(excerpt))
	}
}

func TestExtractWikiLinks(t *testing.T) {
	content := "See [[Home]] and [[My Page]] for details. Also [[Home]] again."
	links := ExtractWikiLinks(content)
	if len(links) != 2 {
		t.Fatalf("expected 2 unique links, got %d: %v", len(links), links)
	}
	if links[0] != "Home" || links[1] != "My_Page" {
		t.Fatalf("unexpected links: %v", links)
	}
}

func TestExtractWikiLinksEmpty(t *testing.T) {
	links := ExtractWikiLinks("No links here, just text.")
	if len(links) != 0 {
		t.Fatalf("expected 0 links, got %d", len(links))
	}
}

func TestLinkGraph(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)
	_ = store.Save(KindPage,"Home", "Welcome! See [[About]] and [[Contact]]")
	_ = store.Save(KindPage,"About", "About us. Back to [[Home]]")
	_ = store.Save(KindPage,"Contact", "Contact page.")

	graph, err := store.LinkGraph()
	if err != nil {
		t.Fatalf("LinkGraph failed: %v", err)
	}
	if len(graph["Home"]) != 2 {
		t.Fatalf("expected Home to have 2 links, got %d", len(graph["Home"]))
	}
	if len(graph["About"]) != 1 || graph["About"][0] != "Home" {
		t.Fatalf("unexpected About links: %v", graph["About"])
	}
	if len(graph["Contact"]) != 0 {
		t.Fatalf("expected Contact to have 0 links, got %d", len(graph["Contact"]))
	}
}

func TestPageStoreDeletePage(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	_ = store.Save(KindPage, "ToDelete", "content")
	if err := store.Delete(KindPage, "ToDelete"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	_, err := store.Load(KindPage, "ToDelete")
	if !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("expected ErrPageNotFound after delete, got %v", err)
	}
}

func TestPageStoreDeleteNonExistent(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	err := store.Delete(KindPage, "Ghost")
	if err == nil {
		t.Fatal("expected error deleting non-existent page")
	}
}

// ── Skill store tests ─────────────────────────────────────────────────────

func TestPageStoreSkillCRUD(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	// Save a skill
	if err := store.Save(KindSkill, "Go_Testing", "# Go Testing\n\nTags: go, testing"); err != nil {
		t.Fatalf("Save skill failed: %v", err)
	}

	// Load the skill
	skill, err := store.Load(KindSkill, "Go_Testing")
	if err != nil {
		t.Fatalf("Load skill failed: %v", err)
	}
	if skill.Slug != "Go_Testing" {
		t.Fatalf("unexpected slug: %s", skill.Slug)
	}
	if skill.Content != "# Go Testing\n\nTags: go, testing" {
		t.Fatalf("unexpected content: %q", skill.Content)
	}

	// List skills
	skills, err := store.List(KindSkill)
	if err != nil {
		t.Fatalf("List skills failed: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}

	// Delete the skill
	if err := store.Delete(KindSkill, "Go_Testing"); err != nil {
		t.Fatalf("Delete skill failed: %v", err)
	}
	_, err = store.Load(KindSkill, "Go_Testing")
	if !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("expected ErrPageNotFound after delete, got %v", err)
	}
}

func TestListSkillEntries(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	_ = store.Save(KindSkill, "Go_Testing", "# Go Testing\n\nTags: go, testing")
	_ = store.Save(KindSkill, "Deploy_K8s", "# Deploy K8s\n\nTags: deploy, kubernetes")
	_ = store.Save(KindSkill, "No_Tags", "# No Tags\n\nA skill without tags.")

	entries, err := store.ListSkillEntries()
	if err != nil {
		t.Fatalf("ListSkillEntries failed: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Should be sorted alphabetically
	if entries[0].Slug != "Deploy_K8s" {
		t.Fatalf("expected Deploy_K8s first, got %s", entries[0].Slug)
	}
	if entries[1].Slug != "Go_Testing" {
		t.Fatalf("expected Go_Testing second, got %s", entries[1].Slug)
	}

	// Check tags
	if len(entries[0].Tags) != 2 || entries[0].Tags[0] != "deploy" {
		t.Fatalf("unexpected tags for Deploy_K8s: %v", entries[0].Tags)
	}
	if len(entries[1].Tags) != 2 || entries[1].Tags[0] != "go" {
		t.Fatalf("unexpected tags for Go_Testing: %v", entries[1].Tags)
	}
	// No tags should return empty slice, not nil
	if len(entries[2].Tags) != 0 {
		t.Fatalf("expected empty tags for No_Tags, got %v", entries[2].Tags)
	}
}

func TestListSkillEntriesEmpty(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	entries, err := store.ListSkillEntries()
	if err != nil {
		t.Fatalf("ListSkillEntries failed: %v", err)
	}
	if entries != nil && len(entries) != 0 {
		t.Fatalf("expected nil or empty, got %v", entries)
	}
}

func TestListSkillEntriesSkipsSpecial(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	_ = store.Save(KindSkill, "Normal_Skill", "# Normal\n\nTags: test")
	_ = store.Save(KindSkill, "_hidden", "should be skipped")

	entries, err := store.ListSkillEntries()
	if err != nil {
		t.Fatalf("ListSkillEntries failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (skipping _hidden), got %d", len(entries))
	}
	if entries[0].Slug != "Normal_Skill" {
		t.Fatalf("unexpected slug: %s", entries[0].Slug)
	}
}

func TestBackLinks(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)
	_ = store.Save(KindPage,"Home", "See [[About]] and [[Contact]]")
	_ = store.Save(KindPage,"About", "Back to [[Home]]")
	_ = store.Save(KindPage,"Contact", "Back to [[Home]]")

	backlinks, err := store.BackLinks("Home")
	if err != nil {
		t.Fatalf("BackLinks failed: %v", err)
	}
	if len(backlinks) != 2 {
		t.Fatalf("expected 2 backlinks to Home, got %d", len(backlinks))
	}

	backlinks, err = store.BackLinks("About")
	if err != nil {
		t.Fatalf("BackLinks failed: %v", err)
	}
	if len(backlinks) != 1 || backlinks[0].Slug != "Home" {
		t.Fatalf("unexpected backlinks to About: %v", backlinks)
	}

	backlinks, err = store.BackLinks("Nonexistent")
	if err != nil {
		t.Fatalf("BackLinks failed: %v", err)
	}
	if len(backlinks) != 0 {
		t.Fatalf("expected 0 backlinks to Nonexistent, got %d", len(backlinks))
	}
}
