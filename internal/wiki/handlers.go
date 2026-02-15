package wiki

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Handler struct {
	store       *PageStore
	secureStore *SecureStore
	renderer    *MarkdownRenderer
	templates   string
	autoCommit  *GitAutoCommitter
}

var secureMacroMatcher = regexp.MustCompile(`\{\{\s*secure:([a-zA-Z0-9_\-]+)\s*\}\}`)

type TemplateData struct {
	Title        string
	Sidebar      []PageLink
	Page         *Page
	RenderedHTML template.HTML
	RawContent   string
	Query        string
	Results      []SearchResult
	SecureBlocks []string
}

func NewHandler(store *PageStore, secureStore *SecureStore, renderer *MarkdownRenderer, templatesDir string, autoCommitter *GitAutoCommitter) *Handler {
	return &Handler{
		store:       store,
		secureStore: secureStore,
		renderer:    renderer,
		templates:   templatesDir,
		autoCommit:  autoCommitter,
	}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleIndex)
	mux.HandleFunc("/wiki/", h.handleView)
	mux.HandleFunc("/edit/", h.handleEdit)
	mux.HandleFunc("/search", h.handleSearch)
	mux.HandleFunc("/secure-inline/unlock", h.handleInlineSecureUnlock)
	mux.HandleFunc("/secure-inline/save", h.handleInlineSecureSave)
	return mux
}

func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/wiki/Home", http.StatusFound)
}

func (h *Handler) handleView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	slug := strings.TrimPrefix(r.URL.Path, "/wiki/")
	if slug == "" {
		http.Redirect(w, r, "/wiki/Home", http.StatusFound)
		return
	}

	page, err := h.store.Load(slug)
	if err != nil {
		if errors.Is(err, ErrPageNotFound) {
			http.Redirect(w, r, fmt.Sprintf("/edit/%s", slug), http.StatusFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	html, err := h.renderer.Render(slug, page.Content)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.render(w, "view", TemplateData{
		Title:        page.Title,
		Page:         page,
		RenderedHTML: html,
	})
}

func (h *Handler) handleEdit(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/edit/")
	if slug == "" {
		http.Error(w, "missing page slug", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		page, err := h.store.Load(slug)
		if err != nil && !errors.Is(err, ErrPageNotFound) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		raw := ""
		title := TitleFromSlug(slug)
		if page != nil {
			raw = page.Content
			title = page.Title
		}

		h.render(w, "edit", TemplateData{
			Title:        "Edit: " + title,
			Page:         &Page{Slug: slug, Title: title},
			RawContent:   raw,
			SecureBlocks: extractSecureBlockIDs(raw),
		})
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		content := r.FormValue("content")
		if err := h.store.Save(slug, content); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := h.autoCommit.CommitPageSave(slug); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/wiki/%s", slug), http.StatusFound)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleInlineSecureUnlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid form"})
		return
	}

	pageSlug := strings.TrimSpace(r.FormValue("page_slug"))
	blockID := strings.TrimSpace(r.FormValue("block_id"))
	password := r.FormValue("password")
	if pageSlug == "" || blockID == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "missing page or block id"})
		return
	}

	plain, err := h.secureStore.Load(pageSlug, blockID, password)
	if err != nil {
		h.writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "invalid password"})
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "content": plain})
}

func (h *Handler) handleInlineSecureSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid form"})
		return
	}

	pageSlug := strings.TrimSpace(r.FormValue("page_slug"))
	blockID := strings.TrimSpace(r.FormValue("block_id"))
	password := r.FormValue("password")
	content := r.FormValue("content")
	if pageSlug == "" || blockID == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "missing page or block id"})
		return
	}
	if strings.TrimSpace(password) == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "password required"})
		return
	}

	if err := h.secureStore.Save(pageSlug, blockID, password, content); err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "failed to save secure block"})
		return
	}
	if err := h.autoCommit.CommitSecureBlockSave(pageSlug, blockID); err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "auto-commit failed"})
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, payload map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func extractSecureBlockIDs(markdown string) []string {
	matches := secureMacroMatcher.FindAllStringSubmatch(markdown, -1)
	if len(matches) == 0 {
		return []string{}
	}

	unique := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			unique[match[1]] = struct{}{}
		}
	}

	ids := make([]string, 0, len(unique))
	for id := range unique {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	results, err := h.store.Search(query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.render(w, "search", TemplateData{
		Title:   "Search",
		Query:   query,
		Results: results,
	})
}

func (h *Handler) render(w http.ResponseWriter, name string, data TemplateData) {
	sidebar, err := h.store.List()
	if err == nil {
		data.Sidebar = sidebar
	}

	files := []string{
		filepath.Join(h.templates, "base.html"),
		filepath.Join(h.templates, name+".html"),
	}
	tmpl, err := template.ParseFiles(files...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
