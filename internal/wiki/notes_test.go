package wiki

import (
	"path/filepath"
	"testing"
	"time"
)

func TestNewNoteIDFormat(t *testing.T) {
	id := NewNoteID(time.Date(2026, 7, 16, 15, 30, 42, 0, time.Local))
	if id != "20260716-153042" {
		t.Fatalf("NewNoteID = %q, want 20260716-153042", id)
	}
	if !ValidNoteID(id) {
		t.Fatalf("ValidNoteID(%q) = false", id)
	}
	if !ValidNoteID(id + "-2") {
		t.Fatalf("collision-suffixed id should be valid")
	}
	for _, bad := range []string{"", "notaid", "../etc/passwd", "2026-07-16", "20260716_153042"} {
		if ValidNoteID(bad) {
			t.Errorf("ValidNoteID(%q) = true, want false", bad)
		}
	}
}

func TestNoteCreatedFromID(t *testing.T) {
	got := NoteCreatedFromID("20260716-153042")
	want := time.Date(2026, 7, 16, 15, 30, 42, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("NoteCreatedFromID = %v, want %v", got, want)
	}
	// Collision suffix is ignored.
	if got := NoteCreatedFromID("20260716-153042-3"); !got.Equal(want) {
		t.Fatalf("NoteCreatedFromID with suffix = %v, want %v", got, want)
	}
	if !NoteCreatedFromID("garbage").IsZero() {
		t.Fatalf("malformed id should yield zero time")
	}
}

func TestNoteTitle(t *testing.T) {
	cases := map[string]string{
		"Buy milk\nand eggs":      "Buy milk",
		"# Heading title\n\nbody": "Heading title",
		"\n\n  ## Spaced  \nrest": "Spaced",
		"":                        "Untitled",
		"\n\n\n":                  "Untitled",
		"###":                     "Untitled",
	}
	for content, want := range cases {
		if got := NoteTitle(content); got != want {
			t.Errorf("NoteTitle(%q) = %q, want %q", content, got, want)
		}
	}
}

// TestNoteColorContract pins the exact FNV-1a color values. These are the
// contract with the browser mirror in web/static/notes.js — if this test
// changes, that file must change to match (and vice versa).
func TestNoteColorContract(t *testing.T) {
	cases := map[string]int{
		"Buy DNS renewal":     0,
		"Groceries":           4,
		"Ideas":               3,
		"# Refactor auth pkg": 5,
		"hello":               3,
		"Untitled":            0,
		"Möbius":              0,
	}
	for title, want := range cases {
		if got := NoteColor(title); got != want {
			t.Errorf("NoteColor(%q) = %d, want %d", title, got, want)
		}
	}
	// Every color is in range and stable across calls.
	for _, s := range []string{"a", "bbb", "long title here", "12345"} {
		c := NoteColor(s)
		if c < 0 || c >= NoteColorCount {
			t.Errorf("NoteColor(%q) = %d out of range", s, c)
		}
		if NoteColor(s) != c {
			t.Errorf("NoteColor(%q) not stable", s)
		}
	}
}

// newNoteStore returns a store rooted at a temp dir with note directories created.
func newNoteStore(t *testing.T) *PageStore {
	t.Helper()
	return NewPageStore(filepath.Join(t.TempDir(), "pages"))
}

func TestNoteStoreRoundTrip(t *testing.T) {
	s := newNoteStore(t)

	id, err := s.CreateNote("First note\nsome body")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if !ValidNoteID(id) {
		t.Fatalf("CreateNote returned invalid id %q", id)
	}

	note, err := s.LoadNote(id)
	if err != nil {
		t.Fatalf("LoadNote: %v", err)
	}
	if note.Title != "First note" {
		t.Fatalf("title = %q, want First note", note.Title)
	}
	if note.Archived {
		t.Fatal("new note should not be archived")
	}
	if note.Color != NoteColor("First note") {
		t.Fatalf("color = %d, want %d", note.Color, NoteColor("First note"))
	}

	// Update content.
	if err := s.SaveNote(id, "Renamed\nbody"); err != nil {
		t.Fatalf("SaveNote: %v", err)
	}
	note, _ = s.LoadNote(id)
	if note.Title != "Renamed" {
		t.Fatalf("after save title = %q, want Renamed", note.Title)
	}

	active, err := s.ListNotes(false)
	if err != nil || len(active) != 1 {
		t.Fatalf("ListNotes(false) = %d notes, err=%v", len(active), err)
	}
}

func TestNoteArchiveLifecycle(t *testing.T) {
	s := newNoteStore(t)
	id, _ := s.CreateNote("Archive me")

	if err := s.ArchiveNote(id); err != nil {
		t.Fatalf("ArchiveNote: %v", err)
	}

	active, _ := s.ListNotes(false)
	if len(active) != 0 {
		t.Fatalf("archived note still on active board: %d", len(active))
	}
	archived, _ := s.ListNotes(true)
	if len(archived) != 1 {
		t.Fatalf("archived board = %d, want 1", len(archived))
	}
	if !archived[0].Archived {
		t.Fatal("listed archived note missing Archived flag")
	}

	// LoadNote finds archived notes.
	note, err := s.LoadNote(id)
	if err != nil || !note.Archived {
		t.Fatalf("LoadNote archived: note=%v err=%v", note, err)
	}

	// Restore.
	if err := s.RestoreNote(id); err != nil {
		t.Fatalf("RestoreNote: %v", err)
	}
	active, _ = s.ListNotes(false)
	if len(active) != 1 {
		t.Fatalf("after restore active = %d, want 1", len(active))
	}
}

func TestNoteDelete(t *testing.T) {
	s := newNoteStore(t)
	id, _ := s.CreateNote("Delete me")
	if err := s.DeleteNote(id); err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}
	if _, err := s.LoadNote(id); err == nil {
		t.Fatal("note still loadable after delete")
	}

	// Delete works on archived notes too.
	id2, _ := s.CreateNote("Delete archived")
	_ = s.ArchiveNote(id2)
	if err := s.DeleteNote(id2); err != nil {
		t.Fatalf("DeleteNote archived: %v", err)
	}
	if _, err := s.LoadNote(id2); err == nil {
		t.Fatal("archived note still loadable after delete")
	}
}

func TestNoteSearchWithDB(t *testing.T) {
	s := newNoteStore(t)
	db, err := OpenDB(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()
	s.SetDB(db)

	active, _ := s.CreateNote("Deploy pipeline\nrun the release")
	arch, _ := s.CreateNote("Archived deploy\nold notes")
	_ = s.ArchiveNote(arch)

	// Active-only search excludes the archived match.
	got, err := s.SearchNotes("deploy", false)
	if err != nil {
		t.Fatalf("SearchNotes: %v", err)
	}
	if len(got) != 1 || got[0].ID != active {
		t.Fatalf("active search = %+v, want only %s", got, active)
	}

	// include_archived surfaces both.
	got, _ = s.SearchNotes("deploy", true)
	if len(got) != 2 {
		t.Fatalf("archived-inclusive search = %d, want 2", len(got))
	}

	// Direct FTS query carries the archived flag.
	res, err := db.SearchFTSNotes("archived", true)
	if err != nil {
		t.Fatalf("SearchFTSNotes: %v", err)
	}
	if len(res) != 1 || !res[0].Archived {
		t.Fatalf("SearchFTSNotes archived flag wrong: %+v", res)
	}
}

func TestNoteInvalidID(t *testing.T) {
	s := newNoteStore(t)
	if _, err := s.LoadNote("../../etc/passwd"); err == nil {
		t.Fatal("LoadNote accepted a traversal id")
	}
	if err := s.SaveNote("bad id", "x"); err == nil {
		t.Fatal("SaveNote accepted an invalid id")
	}
}
