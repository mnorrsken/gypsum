package wiki

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Handler struct {
	store      *PageStore
	crypto     *ServerCrypto
	renderer   *MarkdownRenderer
	templates  string
	autoCommit *GitAutoCommitter
}

type ImageInfo struct {
	Name    string
	URL     string
	Size    int64
	ModTime time.Time
	UsedBy  []string // page slugs referencing this image
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
	Images       []ImageInfo
	History      []HistoryEntry
	IsNew        bool
	DiffHTML     template.HTML
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
	mux.HandleFunc("/new", h.handleNewPage)
	mux.HandleFunc("/pages", h.handlePages)
	mux.HandleFunc("/wiki/", h.handleView)
	mux.HandleFunc("/edit/", h.handleEdit)
	mux.HandleFunc("/search", h.handleSearch)
	mux.HandleFunc("/history/", h.handleHistory)
	mux.HandleFunc("/history-diff/", h.handleHistoryDiff)
	mux.HandleFunc("/secure-inline/unlock", h.handleInlineSecureUnlock)
	mux.HandleFunc("/images", h.handleImages)
	mux.HandleFunc("/images/upload", h.handleImageUpload)
	mux.HandleFunc("/images/delete", h.handleImageDelete)
	mux.HandleFunc("/images/list", h.handleImageList)
	mux.Handle("/images/", http.StripPrefix("/images/", http.FileServer(http.Dir(h.store.ImagesDir()))))
	mux.Handle("/mcp", NewMCPHandler(h.store, h.autoCommit))
	return mux
}

func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/wiki/Home", http.StatusFound)
}

func (h *Handler) handleNewPage(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.render(w, "new", TemplateData{
			Title: "New Page",
		})
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		title := strings.TrimSpace(r.FormValue("title"))
		if title == "" {
			h.render(w, "new", TemplateData{
				Title: "New Page",
				Query: "Please enter a page title.",
			})
			return
		}
		slug := SlugFromTitle(title)
		_, err := h.store.Load(slug)
		if err == nil {
			// page already exists
			h.render(w, "new", TemplateData{
				Title: "New Page",
				Query: fmt.Sprintf("A page named \"%s\" already exists.", title),
			})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/edit/%s?title=%s", slug, url.QueryEscape(title)), http.StatusFound)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
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
			editURL := fmt.Sprintf("/edit/%s", slug)
			if t := r.URL.Query().Get("title"); t != "" {
				editURL += "?title=" + url.QueryEscape(t)
			}
			http.Redirect(w, r, editURL, http.StatusFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// If the page starts with a level-1 heading, use that as the display
	// title and render only the rest of the content (the heading is shown
	// by the page-title-area in the template, so rendering it again would
	// duplicate it).
	displayTitle := page.Title
	renderSource := page.Content
	if h1, rest := ExtractH1Title(page.Content); h1 != "" {
		displayTitle = h1
		renderSource = rest
	}

	html, err := h.renderer.Render(renderSource)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.render(w, "view", TemplateData{
		Title:        displayTitle,
		Page:         &Page{Slug: page.Slug, Title: displayTitle, Content: page.Content},
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
		isNew := true
		if page != nil {
			raw = page.Content
			title = page.Title
			isNew = false
		}

		// For new pages, pre-populate with a level-1 heading so the proper
		// title (including characters stripped from the slug) is preserved.
		if isNew && raw == "" {
			prettyTitle := title
			if t := r.URL.Query().Get("title"); t != "" {
				prettyTitle = t
			}
			raw = "# " + prettyTitle + "\n\n"
		}

		prefix := "Edit: "
		if isNew {
			prefix = "New page: "
		}

		h.render(w, "edit", TemplateData{
			Title:      prefix + title,
			Page:       &Page{Slug: slug, Title: title},
			RawContent: h.crypto.DecryptForEdit(raw),
			IsNew:      isNew,
		})
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		content := strings.ReplaceAll(r.FormValue("content"), "\r\n", "\n")

		// Validate custom tags before saving
		if validationErr := ValidateContent(content); validationErr != "" {
			title := TitleFromSlug(slug)
			page, _ := h.store.Load(slug)
			isNew := page == nil
			prefix := "Edit: "
			if isNew {
				prefix = "New page: "
			}
			h.render(w, "edit", TemplateData{
				Title:      prefix + title,
				Page:       &Page{Slug: slug, Title: title},
				RawContent: content,
				IsNew:      isNew,
				Query:      validationErr,
			})
			return
		}

		// Show diff preview if requested
		if r.FormValue("showdiff") == "1" {
			title := TitleFromSlug(slug)
			oldContent := ""
			if page, _ := h.store.Load(slug); page != nil {
				oldContent = page.Content
			}
			// Encrypt the incoming editor content, preserving original
			// ciphertext for unchanged blocks so the diff is clean.
			newEncrypted, err := h.crypto.EncryptForSavePreserving(content, oldContent)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			diffHTML := RenderUnifiedDiff(oldContent, newEncrypted, slug)
			h.render(w, "diff", TemplateData{
				Title:      "Diff: " + title,
				Page:       &Page{Slug: slug, Title: title},
				RawContent: content,
				DiffHTML:   diffHTML,
			})
			return
		}

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

func (h *Handler) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	slug := strings.TrimPrefix(r.URL.Path, "/history/")
	if slug == "" {
		http.Error(w, "missing page slug", http.StatusBadRequest)
		return
	}

	title := TitleFromSlug(slug)
	entries, _ := h.autoCommit.PageHistory(slug, 50)

	h.render(w, "history", TemplateData{
		Title:   "History: " + title,
		Page:    &Page{Slug: slug, Title: title},
		History: entries,
	})
}

func (h *Handler) handleHistoryDiff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	slug := strings.TrimPrefix(r.URL.Path, "/history-diff/")
	if slug == "" {
		http.Error(w, "missing page slug", http.StatusBadRequest)
		return
	}

	fromHash := r.URL.Query().Get("from")
	toHash := r.URL.Query().Get("to")
	if fromHash == "" || toHash == "" {
		http.Error(w, "missing from or to parameter", http.StatusBadRequest)
		return
	}

	oldContent, err := h.autoCommit.PageContentAtRevision(slug, fromHash)
	if err != nil {
		http.Error(w, "could not load old revision: "+err.Error(), http.StatusNotFound)
		return
	}
	newContent, err := h.autoCommit.PageContentAtRevision(slug, toHash)
	if err != nil {
		http.Error(w, "could not load new revision: "+err.Error(), http.StatusNotFound)
		return
	}

	diffHTML := RenderUnifiedDiff(oldContent, newContent, slug)
	title := TitleFromSlug(slug)

	h.render(w, "history_diff", TemplateData{
		Title:    "Diff: " + title,
		Page:     &Page{Slug: slug, Title: title},
		DiffHTML: diffHTML,
		Query:    fmt.Sprintf("%s...%s", fromHash[:minLen(len(fromHash), 7)], toHash[:minLen(len(toHash), 7)]),
	})
}

func minLen(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (h *Handler) handleImages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	images, err := h.store.ListImages()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.render(w, "images", TemplateData{
		Title:  "Images",
		Images: images,
	})
}

func (h *Handler) handleImageUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(10 << 20); err != nil { // 10 MB max
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "file too large or invalid"})
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "missing image"})
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowed := map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".svg": true}
	if !allowed[ext] {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "unsupported image type"})
		return
	}

	// Generate unique filename: {original-name}-{YYYYMMDD}-{8hexrandom}{ext}
	// or {YYYYMMDD}-{8hexrandom}{ext} when the original name is generic/absent.
	randBytes := make([]byte, 4)
	_, _ = rand.Read(randBytes)
	namePart := sanitizeImageBasename(header.Filename)
	datePart := time.Now().Format("20060102")
	shortRand := hex.EncodeToString(randBytes) // 8 hex chars
	var filename string
	if namePart != "" {
		filename = fmt.Sprintf("%s-%s-%s%s", namePart, datePart, shortRand, ext)
	} else {
		filename = fmt.Sprintf("%s-%s%s", datePart, shortRand, ext)
	}

	dstPath := filepath.Join(h.store.ImagesDir(), filename)
	dst, err := os.Create(dstPath)
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "failed to save image"})
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "failed to write image"})
		return
	}

	_ = h.autoCommit.CommitImageSave(filename)

	url := "/images/" + filename
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "url": url, "filename": filename})
}

// sanitizeImageBasename converts an original filename (without extension) into
// a lowercase hyphen-separated slug suitable for use in a stored filename.
// Returns "" for generic names like "image" or "pasted-image".
func sanitizeImageBasename(filename string) string {
	// Strip extension
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	base = strings.ToLower(base)

	var sb strings.Builder
	prevHyphen := false
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
			prevHyphen = false
		} else if sb.Len() > 0 && !prevHyphen {
			sb.WriteRune('-')
			prevHyphen = true
		}
	}
	result := strings.TrimRight(sb.String(), "-")

	// Ignore generic fallback names produced by browsers / our own JS
	switch result {
	case "", "image", "pasted-image", "screenshot":
		return ""
	}

	const maxLen = 48
	if len(result) > maxLen {
		result = strings.TrimRight(result[:maxLen], "-")
	}
	return result
}

func (h *Handler) handleImageList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}

	images, err := h.store.ListImages()
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	type imgItem struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	items := make([]imgItem, len(images))
	for i, img := range images {
		items[i] = imgItem{Name: img.Name, URL: img.URL}
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "images": items})
}

func (h *Handler) handleImageDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	filename := strings.TrimSpace(r.FormValue("filename"))
	if filename == "" || strings.Contains(filename, "/") || strings.Contains(filename, "..") {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}

	if err := os.Remove(filepath.Join(h.store.ImagesDir(), filename)); err != nil {
		http.Error(w, "failed to delete image", http.StatusInternalServerError)
		return
	}

	_ = h.autoCommit.CommitImageDelete(filename)

	http.Redirect(w, r, "/images", http.StatusFound)
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
