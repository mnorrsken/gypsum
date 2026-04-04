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
	"strconv"
	"strings"
	"time"
)

type Handler struct {
	store      *PageStore
	crypto     *ServerCrypto
	renderer   *MarkdownRenderer
	templates  string
	docsDir    string // path to docs/ directory; empty = no docs section
	autoCommit *GitAutoCommitter
	oauth      *OAuthServer // non-nil → register /mcp/external + OAuth discovery routes
	db         *DB          // SQLite database for shares and token storage
	tmplCache  map[string]*template.Template
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
	History       []HistoryEntry
	GlobalEdits   []GlobalHistoryEntry
	CurrentPage   int
	TotalPages    int
	IsNew         bool
	DiffHTML     template.HTML
	GraphJSON    template.JS
	ShareToken   string // non-empty if page has an active share link
	ShareURL     string // full public URL for the share link
	ShareCreated string // human-readable creation time
	DocPages     []PageLink       // documentation pages from docs/
	Skills       []SkillListEntry // skill pages with tags
	SkillTags    []string         // tags for the current skill being viewed
}

func NewHandler(store *PageStore, crypto *ServerCrypto, renderer *MarkdownRenderer, templatesDir string, autoCommitter *GitAutoCommitter, oauth *OAuthServer, db *DB) *Handler {
	h := &Handler{
		store:      store,
		crypto:     crypto,
		renderer:   renderer,
		templates:  templatesDir,
		autoCommit: autoCommitter,
		oauth:      oauth,
		db:         db,
		tmplCache:  make(map[string]*template.Template),
	}
	h.parseTemplates()
	return h
}

// SetDocsDir enables the /docs/ section, serving markdown files from dir.
func (h *Handler) SetDocsDir(dir string) {
	h.docsDir = dir
}

// templateFuncs are helper functions available in all templates.
var templateFuncs = template.FuncMap{
	"add":      func(a, b int) int { return a + b },
	"subtract": func(a, b int) int { return a - b },
	"highlightSnippet": func(s string) template.HTML {
		escaped := template.HTMLEscapeString(s)
		escaped = strings.ReplaceAll(escaped, "&lt;&lt;", "<mark>")
		escaped = strings.ReplaceAll(escaped, "&gt;&gt;", "</mark>")
		return template.HTML(escaped)
	},
}

// parseTemplates pre-parses all page templates paired with the base layout.
func (h *Handler) parseTemplates() {
	basePath := filepath.Join(h.templates, "base.html")
	names := []string{
		"view", "edit", "new", "search", "pages", "history",
		"history_diff", "images", "diff", "graph", "recent_edits", "share",
		"doc", "docs",
		"skills", "skill_view", "skill_edit",
	}
	for _, name := range names {
		pagePath := filepath.Join(h.templates, name+".html")
		tmpl, err := template.New("").Funcs(templateFuncs).ParseFiles(basePath, pagePath)
		if err != nil {
			// Templates may not exist in test environments; skip.
			continue
		}
		h.tmplCache[name] = tmpl
	}

	// Public page template is standalone (no base layout).
	publicPath := filepath.Join(h.templates, "public.html")
	if tmpl, err := template.New("").Funcs(templateFuncs).ParseFiles(publicPath); err == nil {
		h.tmplCache["public"] = tmpl
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
	mux.HandleFunc("/delete/", h.handleDeletePage)
	mux.HandleFunc("/share/", h.handleShare)
	mux.HandleFunc("/public/", h.handlePublic)
	mux.HandleFunc("/graph", h.handleGraph)
	mux.HandleFunc("/recent-edits", h.handleRecentEdits)
	mux.HandleFunc("/convert/mediawiki", h.handleConvertMediaWiki)
	mux.HandleFunc("/skills", h.handleSkillsList)
	mux.HandleFunc("/skills/", h.handleSkillView)
	mux.HandleFunc("/new-skill", h.handleNewSkill)
	mux.HandleFunc("/edit-skill/", h.handleEditSkill)
	mux.HandleFunc("/delete-skill/", h.handleDeleteSkill)
	mux.HandleFunc("/skill-history/", h.handleSkillHistory)
	if h.docsDir != "" {
		mux.HandleFunc("/docs", h.handleDocsList)
		mux.HandleFunc("/docs/", h.handleDocs)
	}

	// Rate limiter for MCP and OAuth endpoints: 30 requests/sec per IP, burst of 60.
	mcpRL := NewRateLimiter(30, 60, time.Second)
	mux.Handle("/mcp", RateLimit(mcpRL, NewMCPHandler(h.store, h.autoCommit)))

	if h.oauth != nil {
		// OAuth discovery endpoints (must be bypassed in Authelia / reverse proxy)
		mux.HandleFunc("GET /.well-known/oauth-protected-resource", h.oauth.HandleProtectedResource)
		mux.HandleFunc("GET /.well-known/oauth-authorization-server", h.oauth.HandleAuthServerMeta)
		// OAuth authorization flow — stricter rate limit to prevent brute-force.
		oauthRL := NewRateLimiter(5, 10, time.Second)
		mux.HandleFunc("/oauth/authorize", RateLimitFunc(oauthRL, h.oauth.HandleAuthorize))
		mux.HandleFunc("POST /oauth/token", RateLimitFunc(oauthRL, h.oauth.HandleToken))
		mux.HandleFunc("POST /oauth/register", RateLimitFunc(oauthRL, h.oauth.HandleRegister))
		// External MCP endpoint — OAuth-protected, secure fields redacted
		mux.Handle("/mcp/external", RateLimit(mcpRL, NewMCPHandlerExternal(h.store, h.autoCommit, h.oauth)))
	}

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
		_, err := h.store.Load(KindPage, slug)
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

	page, err := h.store.Load(KindPage, slug)
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
		page, err := h.store.Load(KindPage, slug)
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
			page, _ := h.store.Load(KindPage, slug)
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
			if page, _ := h.store.Load(KindPage, slug); page != nil {
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
		if err := h.store.Save(KindPage, slug, encrypted); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := h.autoCommit.CommitSave(KindPage, slug, UsernameFromRequest(r)); err != nil {
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
	results, err := h.store.Search(KindPage, query)
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

	allPages, err := h.store.List(KindPage)
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
	entries, _ := h.autoCommit.DocHistory(KindPage, slug, 50)

	h.render(w, "history", TemplateData{
		Title:   "History: " + title,
		Page:    &Page{Slug: slug, Title: title},
		History: entries,
	})
}

const recentEditsPerPage = 50

func (h *Handler) handleRecentEdits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 1 {
		page = p
	}

	skip := (page - 1) * recentEditsPerPage
	// Fetch one extra to determine if there's a next page.
	entries, _ := h.autoCommit.GlobalHistory(skip, recentEditsPerPage+1)

	totalPages := page
	if len(entries) > recentEditsPerPage {
		totalPages = page + 1
		entries = entries[:recentEditsPerPage]
	}

	h.render(w, "recent_edits", TemplateData{
		Title:       "Recent Edits",
		GlobalEdits: entries,
		CurrentPage: page,
		TotalPages:  totalPages,
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

	oldContent, err := h.autoCommit.DocContentAtRevision(KindPage, slug, fromHash)
	if err != nil {
		http.Error(w, "could not load old revision: "+err.Error(), http.StatusNotFound)
		return
	}
	newContent, err := h.autoCommit.DocContentAtRevision(KindPage, slug, toHash)
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

func (h *Handler) handleGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	graph, err := h.store.LinkGraph()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data, err := json.Marshal(graph)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.render(w, "graph", TemplateData{
		Title:     "Link Graph",
		GraphJSON: template.JS(data),
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
	allowedExt := map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".svg": true}
	if !allowedExt[ext] {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "unsupported image type"})
		return
	}

	// Sniff actual content type from the first 512 bytes.
	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	contentType := http.DetectContentType(buf[:n])
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "failed to read file"})
		return
	}
	allowedMIME := map[string]bool{
		"image/png":     true,
		"image/jpeg":    true,
		"image/gif":     true,
		"image/webp":    true,
		"image/svg+xml": true,
		"text/xml":      true, // SVGs may be detected as text/xml
	}
	if !allowedMIME[contentType] {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "file content does not match an allowed image type"})
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

	_ = h.autoCommit.CommitImageSave(filename, UsernameFromRequest(r))

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

	_ = h.autoCommit.CommitImageDelete(filename, UsernameFromRequest(r))

	http.Redirect(w, r, "/images", http.StatusFound)
}

func (h *Handler) handleDeletePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	slug := strings.TrimPrefix(r.URL.Path, "/delete/")
	if slug == "" {
		http.Error(w, "missing page slug", http.StatusBadRequest)
		return
	}

	if _, err := h.store.Load(KindPage, slug); err != nil {
		if errors.Is(err, ErrPageNotFound) {
			http.Error(w, "page not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.store.Delete(KindPage, slug); err != nil {
		http.Error(w, "failed to delete page", http.StatusInternalServerError)
		return
	}
	_ = h.autoCommit.CommitDelete(KindPage, slug, UsernameFromRequest(r))
	// Clean up any share link for this page.
	if h.db != nil {
		_ = h.db.DeleteShare(slug)
	}
	http.Redirect(w, r, "/pages", http.StatusFound)
}

func (h *Handler) handleShare(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/share/")
	if slug == "" {
		http.Error(w, "missing page slug", http.StatusBadRequest)
		return
	}

	page, err := h.store.Load(KindPage, slug)
	if err != nil {
		if errors.Is(err, ErrPageNotFound) {
			http.Error(w, "page not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	title := page.Title
	if h1, _ := ExtractH1Title(page.Content); h1 != "" {
		title = h1
	}

	switch r.Method {
	case http.MethodGet:
		data := TemplateData{
			Title: "Share: " + title,
			Page:  &Page{Slug: slug, Title: title},
		}
		if h.db != nil {
			if share, err := h.db.GetShare(slug); err == nil && share != nil {
				data.ShareToken = share.Token
				data.ShareURL = buildShareURL(r, share.Token)
				data.ShareCreated = share.CreatedAt.Format("2006-01-02 15:04")
			}
		}
		h.render(w, "share", data)

	case http.MethodPost:
		if h.db == nil {
			http.Error(w, "sharing not available", http.StatusInternalServerError)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch r.FormValue("action") {
		case "create", "regenerate":
			if _, err := h.db.CreateShare(slug); err != nil {
				http.Error(w, "failed to create share link", http.StatusInternalServerError)
				return
			}
		case "revoke":
			if err := h.db.DeleteShare(slug); err != nil {
				http.Error(w, "failed to revoke share link", http.StatusInternalServerError)
				return
			}
		}
		http.Redirect(w, r, "/share/"+slug, http.StatusFound)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handlePublic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := strings.TrimPrefix(r.URL.Path, "/public/")
	if token == "" {
		http.NotFound(w, r)
		return
	}

	if h.db == nil {
		http.NotFound(w, r)
		return
	}

	share, err := h.db.GetShareByToken(token)
	if err != nil || share == nil {
		http.NotFound(w, r)
		return
	}

	page, err := h.store.Load(KindPage, share.Slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	displayTitle := page.Title
	renderSource := page.Content
	if h1, rest := ExtractH1Title(page.Content); h1 != "" {
		displayTitle = h1
		renderSource = rest
	}

	rendered, err := h.renderer.RenderPublic(renderSource)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	tmpl := h.tmplCache["public"]
	if tmpl == nil {
		http.Error(w, "template not found: public", http.StatusInternalServerError)
		return
	}

	data := TemplateData{
		Title:        displayTitle,
		RenderedHTML: rendered,
	}
	if err := tmpl.ExecuteTemplate(w, "public", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// buildShareURL constructs the full public URL for a share token from the request.
func buildShareURL(r *http.Request, token string) string {
	scheme := "http"
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	} else if r.TLS != nil {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/public/%s", scheme, r.Host, token)
}

func (h *Handler) handleConvertMediaWiki(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid form"})
		return
	}
	wikitext := r.FormValue("wikitext")
	if wikitext == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "empty wikitext"})
		return
	}
	markdown := ConvertMediaWikiToMarkdown(wikitext)
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "markdown": markdown})
}

// ── Skill handlers ─────────────────────────────────────────────────────

func (h *Handler) handleSkillsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	skills, err := h.store.ListSkillEntries()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, "skills", TemplateData{
		Title:  "Skills",
		Skills: skills,
	})
}

func (h *Handler) handleSkillView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	slug := strings.TrimPrefix(r.URL.Path, "/skills/")
	if slug == "" {
		http.Redirect(w, r, "/skills", http.StatusFound)
		return
	}

	skill, err := h.store.Load(KindSkill, slug)
	if err != nil {
		if errors.Is(err, ErrPageNotFound) {
			http.Redirect(w, r, fmt.Sprintf("/edit-skill/%s?title=%s", slug, url.QueryEscape(TitleFromSlug(slug))), http.StatusFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	displayTitle := skill.Title
	renderSource := skill.Content
	if h1, rest := ExtractH1Title(skill.Content); h1 != "" {
		displayTitle = h1
		renderSource = rest
	}

	html, err := h.renderer.Render(renderSource)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.render(w, "skill_view", TemplateData{
		Title:        displayTitle,
		Page:         skill,
		RenderedHTML: html,
		SkillTags:    ExtractTags(skill.Content),
	})
}

func (h *Handler) handleNewSkill(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.render(w, "skill_edit", TemplateData{
			Title: "New Skill",
			Page:  &Page{Slug: "New_Skill", Title: "New Skill"},
			IsNew: true,
			RawContent: "# Skill Title\n\nBrief description of what this skill covers.\n\n" +
				"## When to Use\n\nDescribe when to apply this skill.\n\n" +
				"## Instructions\n\n1. Step one\n2. Step two\n\n" +
				"Tags: keyword1, keyword2",
		})
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		title := strings.TrimSpace(r.FormValue("title"))
		if title == "" {
			title = "New Skill"
		}
		slug := SlugFromTitle(title)
		http.Redirect(w, r, fmt.Sprintf("/edit-skill/%s?title=%s", slug, url.QueryEscape(title)), http.StatusFound)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleEditSkill(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/edit-skill/")
	if slug == "" {
		http.Redirect(w, r, "/skills", http.StatusFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		skill, err := h.store.Load(KindSkill, slug)
		isNew := false
		if err != nil {
			title := TitleFromSlug(slug)
			if t := r.URL.Query().Get("title"); t != "" {
				title = t
			}
			skill = &Page{Slug: slug, Title: title}
			isNew = true
		}

		rawContent := skill.Content
		if h.crypto != nil {
			rawContent = h.crypto.DecryptForEdit(rawContent)
		}

		h.render(w, "skill_edit", TemplateData{
			Title:      skill.Title,
			Page:       skill,
			RawContent: rawContent,
			IsNew:      isNew,
		})

	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		content := r.FormValue("content")
		if h.crypto != nil {
			encrypted, err := h.crypto.EncryptForSave(content)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			content = encrypted
		}

		if err := h.store.Save(KindSkill, slug, content); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = h.autoCommit.CommitSave(KindSkill, slug, "")
		http.Redirect(w, r, "/skills/"+slug, http.StatusFound)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleDeleteSkill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	slug := strings.TrimPrefix(r.URL.Path, "/delete-skill/")
	if slug == "" {
		http.Redirect(w, r, "/skills", http.StatusFound)
		return
	}
	if err := h.store.Delete(KindSkill, slug); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.autoCommit.CommitDelete(KindSkill, slug, "")
	http.Redirect(w, r, "/skills", http.StatusFound)
}

func (h *Handler) handleSkillHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	slug := strings.TrimPrefix(r.URL.Path, "/skill-history/")
	if slug == "" {
		http.Redirect(w, r, "/skills", http.StatusFound)
		return
	}

	entries, err := h.autoCommit.DocHistory(KindSkill, slug, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.render(w, "history", TemplateData{
		Title:   TitleFromSlug(slug) + " — History",
		Page:    &Page{Slug: slug, Title: TitleFromSlug(slug)},
		History: entries,
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

	if h.docsDir != "" {
		data.DocPages = h.listDocPages()
	}

	if skills, err := h.store.ListSkillEntries(); err == nil {
		data.Skills = skills
	}

	tmpl := h.tmplCache[name]
	if tmpl == nil {
		http.Error(w, "template not found: "+name, http.StatusInternalServerError)
		return
	}

	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// listDocPages returns a sorted list of documentation pages from docsDir.
func (h *Handler) listDocPages() []PageLink {
	entries, err := os.ReadDir(h.docsDir)
	if err != nil {
		return nil
	}
	var pages []PageLink
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		slug := strings.TrimSuffix(e.Name(), ".md")
		title := docTitleFromSlug(slug)
		pages = append(pages, PageLink{Slug: slug, Title: title})
	}
	return pages
}

// docTitleFromSlug converts "authentication" to "Authentication", "my-guide" to "My Guide".
func docTitleFromSlug(slug string) string {
	words := strings.Fields(strings.ReplaceAll(slug, "-", " "))
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

func (h *Handler) handleDocsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h.render(w, "docs", TemplateData{
		Title: "Documentation",
	})
}

func (h *Handler) handleDocs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	slug := strings.TrimPrefix(r.URL.Path, "/docs/")
	if slug == "" {
		http.Redirect(w, r, "/docs", http.StatusFound)
		return
	}

	path := filepath.Join(h.docsDir, slug+".md")
	content, err := os.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	source := string(content)
	displayTitle := docTitleFromSlug(slug)
	renderSource := source
	if h1, rest := ExtractH1Title(source); h1 != "" {
		displayTitle = h1
		renderSource = rest
	}

	html, err := h.renderer.RenderPublic(renderSource)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.render(w, "doc", TemplateData{
		Title:        displayTitle,
		Page:         &Page{Slug: slug, Title: displayTitle},
		RenderedHTML: html,
	})
}
