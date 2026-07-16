package wiki

import (
	"net/http"
	"strings"
)

// handleNotesBoard renders the quick-notes board. The archived board is served
// at /notes/archived and reuses the same template with read-only cards.
func (h *Handler) handleNotesBoard(w http.ResponseWriter, r *http.Request) {
	archived := strings.HasSuffix(r.URL.Path, "/archived")
	notes, err := h.store.ListNotes(archived)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	title := "Quick Notes"
	if archived {
		title = "Archived Notes"
	}
	h.render(w, "notes", TemplateData{
		Title:         title,
		Notes:         notes,
		NotesArchived: archived,
	})
}

// handleNoteCreate creates a note from posted content and returns its id and
// derived color as JSON. Used by the board's autosave when a new card is first typed.
func (h *Handler) handleNoteCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	content := r.FormValue("content")
	if strings.TrimSpace(content) == "" {
		http.Error(w, "empty note", http.StatusBadRequest)
		return
	}
	id, err := h.store.CreateNote(content)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.autoCommit.CommitNoteSave(id, false, UsernameFromRequest(r))
	h.writeJSON(w, http.StatusCreated, map[string]any{
		"id":    id,
		"color": NoteColor(NoteTitle(content)),
	})
}

// handleNoteSave overwrites an existing note's content (autosave).
func (h *Handler) handleNoteSave(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !ValidNoteID(id) {
		http.Error(w, "invalid note id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	note, err := h.store.LoadNote(id)
	if err != nil {
		http.Error(w, "note not found", http.StatusNotFound)
		return
	}
	if err := h.store.SaveNote(id, r.FormValue("content")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.autoCommit.CommitNoteSave(id, note.Archived, UsernameFromRequest(r))
	w.WriteHeader(http.StatusNoContent)
}

// handleNoteArchive moves a note off the board into the archive.
func (h *Handler) handleNoteArchive(w http.ResponseWriter, r *http.Request) {
	h.noteLifecycle(w, r, true)
}

// handleNoteRestore moves an archived note back onto the board.
func (h *Handler) handleNoteRestore(w http.ResponseWriter, r *http.Request) {
	h.noteLifecycle(w, r, false)
}

func (h *Handler) noteLifecycle(w http.ResponseWriter, r *http.Request, toArchive bool) {
	id := r.PathValue("id")
	if !ValidNoteID(id) {
		http.Error(w, "invalid note id", http.StatusBadRequest)
		return
	}
	if _, err := h.store.LoadNote(id); err != nil {
		http.Error(w, "note not found", http.StatusNotFound)
		return
	}
	var err error
	if toArchive {
		err = h.store.ArchiveNote(id)
	} else {
		err = h.store.RestoreNote(id)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.autoCommit.CommitNoteMove(id, toArchive, UsernameFromRequest(r))
	w.WriteHeader(http.StatusNoContent)
}

// handleNoteDelete permanently removes a note.
func (h *Handler) handleNoteDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !ValidNoteID(id) {
		http.Error(w, "invalid note id", http.StatusBadRequest)
		return
	}
	note, err := h.store.LoadNote(id)
	if err != nil {
		http.Error(w, "note not found", http.StatusNotFound)
		return
	}
	if err := h.store.DeleteNote(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.autoCommit.CommitNoteDelete(id, note.Archived, UsernameFromRequest(r))
	w.WriteHeader(http.StatusNoContent)
}
