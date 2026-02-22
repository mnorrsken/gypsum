package wiki

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"strings"
)

type Handler struct {
	store      *PageStore
	crypto     *ServerCrypto
	renderer   *MarkdownRenderer
	templates  string
	autoCommit *GitAutoCommitter
}

type TemplateData struct {
	Title        string
	Sidebar      []PageLink
	Favorites    []PageLink
	RecentPages  []PageLink
	AllPages     []PageLink
	Page         *Page
	RenderedHTML template.HTML
	RawContent   string
	Query        string
	Results      []SearchResult
}

func NewHandler(store *PageStore, crypto *ServerCrypto, renderer *MarkdownRenderer, templatesDir string, autoCommitter *GitAutoCommitter) *Handler {
	return &Handler{
		store:      store,
		crypto:     crypto,
		renderer:   renderer,
		templates:  templatesDir,
		autoCommit: autoCommitter,
	}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleIndex)
	mux.HandleFunc("/pages", h.handlePages)
	mux.HandleFunc("/wiki/", h.handleView)
	mux.HandleFunc("/edit/", h.handleEdit)
	mux.HandleFunc("/search", h.handleSearch)
	mux.HandleFunc("/secure-inline/unlock", h.handleInlineSecureUnlock)
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

	html, err := h.renderer.Render(page.Content)
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
			Title:      "Edit: " + title,
			Page:       &Page{Slug: slug, Title: title},
			RawContent: h.crypto.DecryptForEdit(raw),
		})
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		content := r.FormValue("content")
		encrypted, err := h.crypto.EncryptForSave(content)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := h.store.Save(slug, encrypted); err != nil {
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

	ciphertext := strings.TrimSpace(r.FormValue("ciphertext"))
	if ciphertext == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "missing ciphertext"})
		return
	}

	plain, err := h.crypto.Decrypt(ciphertext)
	if err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "decryption failed"})
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "content": plain})
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, payload map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
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

func (h *Handler) handlePages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	allPages, err := h.store.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.render(w, "pages", TemplateData{
		Title:    "All Pages",
		AllPages: allPages,
	})
}

func (h *Handler) render(w http.ResponseWriter, name string, data TemplateData) {
	favorites, err := h.store.LoadFavorites()
	if err == nil {
		data.Favorites = favorites
	}

	recent, err := h.store.RecentPages(5)
	if err == nil {
		data.RecentPages = recent
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
