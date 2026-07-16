package wiki

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mnorrsken/gypsum/web"
)

// postForm sends an application/x-www-form-urlencoded POST and returns the recorder.
func postForm(t *testing.T, handler http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestNoteHTTPRoundTrip(t *testing.T) {
	h, handler := newTestHandler(t)

	// Create.
	rec := postForm(t, handler, "/notes/create", url.Values{"content": {"HTTP note\nbody"}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID    string `json:"id"`
		Color int    `json:"color"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if !ValidNoteID(created.ID) {
		t.Fatalf("create returned invalid id %q", created.ID)
	}
	if created.Color != NoteColor("HTTP note") {
		t.Fatalf("create color = %d, want %d", created.Color, NoteColor("HTTP note"))
	}

	// Save.
	if rec := postForm(t, handler, "/notes/save/"+created.ID, url.Values{"content": {"Edited\nbody"}}); rec.Code != http.StatusNoContent {
		t.Fatalf("save status = %d", rec.Code)
	}
	if note, _ := h.store.LoadNote(created.ID); note.Title != "Edited" {
		t.Fatalf("after save title = %q, want Edited", note.Title)
	}

	// Archive → note leaves the active board.
	if rec := postForm(t, handler, "/notes/archive/"+created.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("archive status = %d", rec.Code)
	}
	if active, _ := h.store.ListNotes(false); len(active) != 0 {
		t.Fatalf("archived note still active: %d", len(active))
	}

	// Restore.
	if rec := postForm(t, handler, "/notes/restore/"+created.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("restore status = %d", rec.Code)
	}
	if active, _ := h.store.ListNotes(false); len(active) != 1 {
		t.Fatalf("after restore active = %d, want 1", len(active))
	}

	// Delete.
	if rec := postForm(t, handler, "/notes/delete/"+created.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rec.Code)
	}
	if _, err := h.store.LoadNote(created.ID); err == nil {
		t.Fatal("note still present after delete")
	}
}

func TestNoteHTTPValidation(t *testing.T) {
	_, handler := newTestHandler(t)

	// Invalid id → 400.
	if rec := postForm(t, handler, "/notes/save/not-an-id", url.Values{"content": {"x"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid id save status = %d, want 400", rec.Code)
	}
	// Unknown (well-formed) id → 404.
	if rec := postForm(t, handler, "/notes/save/20200101-000000", url.Values{"content": {"x"}}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id save status = %d, want 404", rec.Code)
	}
	// Empty content on create → 400.
	if rec := postForm(t, handler, "/notes/create", url.Values{"content": {"   "}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty create status = %d, want 400", rec.Code)
	}
	// A GET to a POST-only note endpoint falls through to the "/" catch-all
	// (handleIndex), which 404s any non-root path.
	req := httptest.NewRequest(http.MethodGet, "/notes/create", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET on create status = %d, want 404", rec.Code)
	}
}

func TestNoteBoardRenders(t *testing.T) {
	store := NewPageStore(t.TempDir() + "/pages")
	h := NewHandler(store, NewMarkdownRenderer(), web.Templates(), nil, nil, nil, AllMCPSections)
	handler := h.Routes()

	id, _ := store.CreateNote("Rendered note\nbody")

	req := httptest.NewRequest(http.MethodGet, "/notes", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("board status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Quick Notes") {
		t.Error("board missing 'Quick Notes' heading")
	}
	if !strings.Contains(body, `data-id="`+id+`"`) {
		t.Error("board missing the created note card")
	}
	if !strings.Contains(body, "/static/notes.js") {
		t.Error("board missing notes.js script")
	}
}
