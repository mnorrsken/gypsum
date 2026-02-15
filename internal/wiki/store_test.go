package wiki

import (
	"errors"
	"testing"
)

func TestPageStoreSaveLoadAndMissing(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	const slug = "Home"
	const content = "# Welcome\nHello"
	if err := store.Save(slug, content); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	page, err := store.Load(slug)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if page.Slug != slug || page.Content != content {
		t.Fatalf("loaded page mismatch: %#v", page)
	}

	_, err = store.Load("Does_Not_Exist")
	if !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("expected ErrPageNotFound, got %v", err)
	}
}

func TestPageStoreListAndSearch(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir)

	if err := store.Save("Zoo", "Animals"); err != nil {
		t.Fatalf("Save Zoo failed: %v", err)
	}
	if err := store.Save("Alpha", "Contains keyword: gypsum"); err != nil {
		t.Fatalf("Save Alpha failed: %v", err)
	}

	links, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %d", len(links))
	}
	if links[0].Slug != "Alpha" || links[1].Slug != "Zoo" {
		t.Fatalf("links not sorted as expected: %#v", links)
	}

	results, err := store.Search("GyPsuM")
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
