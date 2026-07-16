package wiki

import (
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// NoteColorCount is the number of distinct note-card colors. A note's color is
// derived by hashing its title, so a note keeps a stable color as long as its
// title (first line) is unchanged. The browser mirrors this in notes.js.
const NoteColorCount = 8

// noteIDTimeLayout is the timestamp format encoded in a note ID.
const noteIDTimeLayout = "20060102-150405"

// noteIDPattern matches a note ID: a creation timestamp (yyyymmdd-hhmmss) with
// an optional numeric collision suffix, e.g. "20260716-153042" or
// "20260716-153042-2". Used as a path-traversal guard on every note request.
var noteIDPattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}(-[0-9]+)?$`)

// NoteEntry is a single quick note. Content is omitted from list responses and
// included when a single note is fetched. Color is presentational — it is
// always derived from Title and never persisted.
type NoteEntry struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Content  string    `json:"content,omitempty"`
	Color    int       `json:"-"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	Archived bool      `json:"archived"`
}

// NewNoteID returns a note ID derived from t (yyyymmdd-hhmmss).
func NewNoteID(t time.Time) string {
	return t.Format(noteIDTimeLayout)
}

// ValidNoteID reports whether id is a well-formed note ID.
func ValidNoteID(id string) bool {
	return noteIDPattern.MatchString(id)
}

// NoteCreatedFromID parses the creation time encoded in a note ID. Any "-N"
// collision suffix is ignored. Returns the zero time if id is malformed.
func NoteCreatedFromID(id string) time.Time {
	if !ValidNoteID(id) {
		return time.Time{}
	}
	base := id
	if len(base) > len(noteIDTimeLayout) {
		base = base[:len(noteIDTimeLayout)] // drop the collision suffix
	}
	t, err := time.ParseInLocation(noteIDTimeLayout, base, time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

// NoteTitle extracts the display title from a note's content: the first
// non-empty line with any leading markdown heading markers stripped. Returns
// "Untitled" when the content has no text.
func NoteTitle(content string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		trimmed = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		if trimmed != "" {
			return trimmed
		}
	}
	return "Untitled"
}

// NoteColor returns a stable color index in [0, NoteColorCount) for a note
// title. The same title always yields the same color on both the server and
// the browser: notes.js hashes the identical FNV-1a formula over the same
// UTF-8 bytes and normalization (trim, strip leading '#', trim, lowercase).
func NoteColor(title string) int {
	norm := strings.ToLower(strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(title), "#")))
	h := fnv.New32a()
	_, _ = h.Write([]byte(norm))
	return int(h.Sum32() % NoteColorCount)
}

// ── Note store operations ──────────────────────────────────────────────

// notesArchiveDir is the subdirectory holding archived notes.
func (s *PageStore) notesArchiveDir() string {
	return filepath.Join(s.notesDir, "archive")
}

// NotesDir returns the directory holding active note files.
func (s *PageStore) NotesDir() string { return s.notesDir }

// notePath locates a note by id, returning its filesystem path and whether it
// is archived. The path is empty when the note does not exist.
func (s *PageStore) notePath(id string) (path string, archived bool) {
	active := filepath.Join(s.notesDir, MarkdownFilename(id))
	if _, err := os.Stat(active); err == nil {
		return active, false
	}
	arch := filepath.Join(s.notesArchiveDir(), MarkdownFilename(id))
	if _, err := os.Stat(arch); err == nil {
		return arch, true
	}
	return "", false
}

// indexNote updates the FTS index for a note (no-op without a database).
func (s *PageStore) indexNote(id, content string, archived bool) {
	if s.db != nil {
		_ = s.db.IndexNote(id, NoteTitle(content), content, archived)
	}
}

// writeNote writes a note file to the active or archive directory and indexes it.
func (s *PageStore) writeNote(id, content string, archived bool) error {
	dir := s.notesDir
	if archived {
		dir = s.notesArchiveDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, MarkdownFilename(id)), []byte(content), 0o644); err != nil {
		return err
	}
	s.indexNote(id, content, archived)
	return nil
}

// CreateNote allocates a fresh note ID and writes content to notes/<id>.md,
// returning the new ID. A numeric suffix is appended if the second-resolution
// timestamp collides with an existing note.
func (s *PageStore) CreateNote(content string) (string, error) {
	base := NewNoteID(time.Now())
	id := base
	for i := 2; ; i++ {
		if p, _ := s.notePath(id); p == "" {
			break
		}
		id = fmt.Sprintf("%s-%d", base, i)
	}
	if err := s.writeNote(id, content, false); err != nil {
		return "", err
	}
	return id, nil
}

// LoadNote returns a single note (active or archived) by id.
func (s *PageStore) LoadNote(id string) (*NoteEntry, error) {
	if !ValidNoteID(id) {
		return nil, ErrPageNotFound
	}
	path, archived := s.notePath(id)
	if path == "" {
		return nil, ErrPageNotFound
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	updated := NoteCreatedFromID(id)
	if info, err := os.Stat(path); err == nil {
		updated = info.ModTime()
	}
	title := NoteTitle(string(content))
	return &NoteEntry{
		ID:       id,
		Title:    title,
		Content:  string(content),
		Color:    NoteColor(title),
		Created:  NoteCreatedFromID(id),
		Updated:  updated,
		Archived: archived,
	}, nil
}

// SaveNote overwrites an existing note's content at whichever location (active
// or archived) it currently lives.
func (s *PageStore) SaveNote(id, content string) error {
	if !ValidNoteID(id) {
		return ErrPageNotFound
	}
	path, archived := s.notePath(id)
	if path == "" {
		return ErrPageNotFound
	}
	return s.writeNote(id, content, archived)
}

// ListNotes returns the active (or archived) notes sorted by last-updated
// (most recent first).
func (s *PageStore) ListNotes(archived bool) ([]NoteEntry, error) {
	dir := s.notesDir
	if archived {
		dir = s.notesArchiveDir()
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	notes := make([]NoteEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		id := SlugFromFilename(entry.Name())
		if strings.HasPrefix(id, "_") || !ValidNoteID(id) {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		updated := NoteCreatedFromID(id)
		if info, err := entry.Info(); err == nil {
			updated = info.ModTime()
		}
		title := NoteTitle(string(content))
		notes = append(notes, NoteEntry{
			ID:       id,
			Title:    title,
			Content:  string(content),
			Color:    NoteColor(title),
			Created:  NoteCreatedFromID(id),
			Updated:  updated,
			Archived: archived,
		})
	}
	sort.Slice(notes, func(i, j int) bool {
		return notes[i].Updated.After(notes[j].Updated)
	})
	return notes, nil
}

// ArchiveNote moves a note off the active board into notes/archive/.
func (s *PageStore) ArchiveNote(id string) error { return s.moveNote(id, true) }

// RestoreNote moves an archived note back onto the active board.
func (s *PageStore) RestoreNote(id string) error { return s.moveNote(id, false) }

func (s *PageStore) moveNote(id string, toArchive bool) error {
	if !ValidNoteID(id) {
		return ErrPageNotFound
	}
	path, archived := s.notePath(id)
	if path == "" {
		return ErrPageNotFound
	}
	if archived == toArchive {
		return nil // already in the desired state
	}
	destDir := s.notesDir
	if toArchive {
		destDir = s.notesArchiveDir()
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(destDir, MarkdownFilename(id))
	if err := os.Rename(path, dest); err != nil {
		return err
	}
	if content, err := os.ReadFile(dest); err == nil {
		s.indexNote(id, string(content), toArchive)
	}
	return nil
}

// DeleteNote permanently removes a note (active or archived) and its index entry.
func (s *PageStore) DeleteNote(id string) error {
	if !ValidNoteID(id) {
		return ErrPageNotFound
	}
	path, _ := s.notePath(id)
	if path == "" {
		return ErrPageNotFound
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if s.db != nil {
		_ = s.db.RemoveNote(id)
	}
	return nil
}

// SearchNotes returns notes matching query, optionally including archived
// notes. It uses the FTS index when available and falls back to a brute-force
// scan otherwise.
func (s *PageStore) SearchNotes(query string, includeArchived bool) ([]NoteEntry, error) {
	if s.db != nil {
		results, err := s.db.SearchFTSNotes(query, includeArchived)
		if err == nil {
			out := make([]NoteEntry, 0, len(results))
			for _, r := range results {
				if n, err := s.LoadNote(r.ID); err == nil {
					out = append(out, *n)
				}
			}
			return out, nil
		}
	}
	terms := splitSearchTerms(query)
	if len(terms) == 0 {
		return nil, nil
	}
	pool, _ := s.ListNotes(false)
	if includeArchived {
		if arch, err := s.ListNotes(true); err == nil {
			pool = append(pool, arch...)
		}
	}
	var out []NoteEntry
	for _, n := range pool {
		if scoreMatch(n.Title, n.Content, terms) > 0 {
			out = append(out, n)
		}
	}
	return out, nil
}

// reindexNotes rebuilds the notes FTS index from disk, covering both the active
// and archived directories. The notes corpus is small, so archive moves and
// pulls trigger a full reindex rather than per-file diffing.
func (s *PageStore) reindexNotes() {
	if s.db == nil {
		return
	}
	var notes []FTSNoteEntry
	collect := func(dir string, archived bool) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
				continue
			}
			id := SlugFromFilename(entry.Name())
			if strings.HasPrefix(id, "_") || !ValidNoteID(id) {
				continue
			}
			content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				continue
			}
			notes = append(notes, FTSNoteEntry{
				ID:       id,
				Title:    NoteTitle(string(content)),
				Content:  string(content),
				Archived: archived,
			})
		}
	}
	collect(s.notesDir, false)
	collect(s.notesArchiveDir(), true)
	if err := s.db.ReindexNotes(notes); err != nil {
		log.Printf("fts: reindex notes failed: %v", err)
	} else {
		log.Printf("fts: indexed %d notes", len(notes))
	}
}
