package wiki

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"strings"
)

type Handler struct {
	store       *PageStore
	secureStore *SecureStore
	renderer    *MarkdownRenderer
	templates   string
}

type TemplateData struct {
	Title         string
	Sidebar       []PageLink
	Page          *Page
	RenderedHTML  template.HTML
	RawContent    string
	Query         string
	Results       []SearchResult
	SecureBlockID string
	SecureText    string
	SecureError   string
	HasSecureData bool
}

func NewHandler(store *PageStore, secureStore *SecureStore, renderer *MarkdownRenderer, templatesDir string) *Handler {
	return &Handler{
		store:       store,
		secureStore: secureStore,
		renderer:    renderer,
		templates:   templatesDir,
	}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleIndex)
	mux.HandleFunc("/wiki/", h.handleView)
	mux.HandleFunc("/edit/", h.handleEdit)
	mux.HandleFunc("/search", h.handleSearch)
	mux.HandleFunc("/secure/", h.handleSecure)
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
			Title:      "Edit: " + title,
			Page:       &Page{Slug: slug, Title: title},
			RawContent: raw,
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
		http.Redirect(w, r, fmt.Sprintf("/wiki/%s", slug), http.StatusFound)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
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

func (h *Handler) handleSecure(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/secure/")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 {
		http.Error(w, "invalid secure block route", http.StatusBadRequest)
		return
	}

	pageSlug := parts[0]
	blockID := parts[1]
	hasData := h.secureStore.Exists(pageSlug, blockID)

	switch {
	case r.Method == http.MethodGet:
		h.render(w, "secure", TemplateData{
			Title:         "Secure block",
			Page:          &Page{Slug: pageSlug, Title: TitleFromSlug(pageSlug)},
			SecureBlockID: blockID,
			HasSecureData: hasData,
		})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/unlock"):
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		password := r.FormValue("password")
		plain, err := h.secureStore.Load(pageSlug, blockID, password)
		if err != nil {
			h.render(w, "secure", TemplateData{
				Title:         "Secure block",
				Page:          &Page{Slug: pageSlug, Title: TitleFromSlug(pageSlug)},
				SecureBlockID: blockID,
				SecureError:   "Invalid password.",
				HasSecureData: hasData,
			})
			return
		}

		h.render(w, "secure", TemplateData{
			Title:         "Secure block",
			Page:          &Page{Slug: pageSlug, Title: TitleFromSlug(pageSlug)},
			SecureBlockID: blockID,
			SecureText:    plain,
			HasSecureData: hasData,
		})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/save"):
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		password := r.FormValue("password")
		content := r.FormValue("content")
		if strings.TrimSpace(password) == "" {
			h.render(w, "secure", TemplateData{
				Title:         "Secure block",
				Page:          &Page{Slug: pageSlug, Title: TitleFromSlug(pageSlug)},
				SecureBlockID: blockID,
				SecureText:    content,
				SecureError:   "Password is required to save secure content.",
				HasSecureData: hasData,
			})
			return
		}
		if err := h.secureStore.Save(pageSlug, blockID, password, content); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/secure/%s/%s", pageSlug, blockID), http.StatusFound)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
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
