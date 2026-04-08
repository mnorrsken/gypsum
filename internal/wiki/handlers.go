package wiki

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

type Handler struct {
	store       *PageStore
	crypto      *ServerCrypto
	renderer    *MarkdownRenderer
	templates   string
	docsDir     string // path to docs/ directory; empty = no docs section
	autoCommit  *GitAutoCommitter
	oauth       *OAuthServer        // non-nil → register /mcp/external + OAuth discovery routes
	db          *DB                 // SQLite database for shares and token storage
	mcpSections map[MCPSection]bool // enabled MCP tool sections
	mcpMetrics  *MCPMetrics         // shared across all MCP handlers
	tmplCache   map[string]*template.Template
	userMgr     *UserManager        // non-nil → multi-user mode enabled
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
	URLPrefix    string           // URL prefix for per-user wiki links (e.g. "/~alice")
	WikiOwner    string           // username of the wiki owner in multi-user mode
}

func NewHandler(store *PageStore, crypto *ServerCrypto, renderer *MarkdownRenderer, templatesDir string, autoCommitter *GitAutoCommitter, oauth *OAuthServer, db *DB, mcpSections map[MCPSection]bool) *Handler {
	h := &Handler{
		store:       store,
		crypto:      crypto,
		renderer:    renderer,
		templates:   templatesDir,
		autoCommit:  autoCommitter,
		oauth:       oauth,
		db:          db,
		mcpSections: mcpSections,
		mcpMetrics:  NewMCPMetrics(),
		tmplCache:   make(map[string]*template.Template),
	}
	h.parseTemplates()
	return h
}

// MCPMetricsHandler returns an HTTP handler serving Prometheus metrics for MCP tools.
func (h *Handler) MCPMetricsHandler() http.Handler {
	return h.mcpMetrics.Handler()
}

// SetDocsDir enables the /docs/ section, serving markdown files from dir.
func (h *Handler) SetDocsDir(dir string) {
	h.docsDir = dir
}

// SetUserManager enables multi-user mode. When set, requests under /~username/
// are routed to per-user wikis while the base routes serve the shared wiki.
func (h *Handler) SetUserManager(mgr *UserManager) {
	h.userMgr = mgr
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

	// Partial templates (standalone fragments for htmx responses).
	partials := []string{"image_grid"}
	for _, name := range partials {
		partialPath := filepath.Join(h.templates, "partials", name+".html")
		tmpl, err := template.New("").Funcs(templateFuncs).ParseFiles(partialPath)
		if err != nil {
			continue
		}
		h.tmplCache["partial_"+name] = tmpl
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
	mux.HandleFunc("/toggle-checkbox/", h.handleToggleCheckbox)
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

	if h.oauth != nil {
		// OAuth discovery endpoints (must be bypassed in Authelia / reverse proxy)
		mux.HandleFunc("GET /.well-known/oauth-protected-resource", h.oauth.HandleProtectedResource)
		mux.HandleFunc("GET /.well-known/oauth-authorization-server", h.oauth.HandleAuthServerMeta)
		// OAuth authorization flow — stricter rate limit to prevent brute-force.
		oauthRL := NewRateLimiter(5, 10, time.Second)
		mux.HandleFunc("/oauth/authorize", RateLimitFunc(oauthRL, h.oauth.HandleAuthorize))
		mux.HandleFunc("POST /oauth/token", RateLimitFunc(oauthRL, h.oauth.HandleToken))
		mux.HandleFunc("POST /oauth/register", RateLimitFunc(oauthRL, h.oauth.HandleRegister))
		// Both /mcp and /mcp/external require OAuth when OAuth is configured.
		// /mcp/external is kept as a backwards-compatible alias.
		mcpOAuth := NewMCPHandlerExternal(h.store, h.autoCommit, h.oauth, h.mcpSections)
		mcpOAuth.SetMetrics(h.mcpMetrics)
		mux.Handle("/mcp", RateLimit(mcpRL, mcpOAuth))
		mux.Handle("/mcp/external", RateLimit(mcpRL, mcpOAuth))
	}

	return mux
}

// handleMultiUser routes /~username/... requests to per-user wiki contexts.
// The username is extracted from the path, the UserContext is resolved (lazily
// provisioned if needed), and the remaining path is dispatched to the
// standard wiki handlers with the per-user store set in context.
func (h *Handler) handleMultiUser(w http.ResponseWriter, r *http.Request) {
	// Path: /~username/wiki/Home → extract "username" and "/wiki/Home"
	path := strings.TrimPrefix(r.URL.Path, "/~")
	slashIdx := strings.IndexByte(path, '/')
	var username, remainder string
	if slashIdx < 0 {
		username = path
		remainder = "/"
	} else {
		username = path[:slashIdx]
		remainder = path[slashIdx:]
	}

	if username == "" {
		http.Error(w, "missing username in path", http.StatusBadRequest)
		return
	}

	uc, err := h.userMgr.Resolve(username)
	if err != nil {
		http.Error(w, "failed to load user wiki: "+err.Error(), http.StatusInternalServerError)
		return
	}

	prefix := "/~" + username

	// Inject the user context and rewrite the URL path so handlers see
	// the same paths they expect (e.g. /wiki/Home, /edit/Home).
	r2 := withUserContext(r, uc, prefix, username)
	r2.URL = cloneURL(r.URL)
	r2.URL.Path = remainder

	// Build a per-user mux that dispatches to the standard handlers.
	// Image file serving needs the per-user images directory.
	userMux := http.NewServeMux()
	userMux.HandleFunc("/", h.handleUserIndex)
	userMux.HandleFunc("/wiki/", h.handleView)
	userMux.HandleFunc("/edit/", h.handleEdit)
	userMux.HandleFunc("/new", h.handleNewPage)
	userMux.HandleFunc("/pages", h.handlePages)
	userMux.HandleFunc("/search", h.handleSearch)
	userMux.HandleFunc("/history/", h.handleHistory)
	userMux.HandleFunc("/history-diff/", h.handleHistoryDiff)
	userMux.HandleFunc("/images", h.handleImages)
	userMux.HandleFunc("/images/upload", h.handleImageUpload)
	userMux.HandleFunc("/images/delete", h.handleImageDelete)
	userMux.HandleFunc("/images/list", h.handleImageList)
	userMux.Handle("/images/", http.StripPrefix("/images/", http.FileServer(http.Dir(uc.Store.ImagesDir()))))
	userMux.HandleFunc("/delete/", h.handleDeletePage)
	userMux.HandleFunc("/toggle-checkbox/", h.handleToggleCheckbox)
	userMux.HandleFunc("/share/", h.handleShare)
	userMux.HandleFunc("/graph", h.handleGraph)
	userMux.HandleFunc("/recent-edits", h.handleRecentEdits)
	userMux.HandleFunc("/skills", h.handleSkillsList)
	userMux.HandleFunc("/skills/", h.handleSkillView)
	userMux.HandleFunc("/new-skill", h.handleNewSkill)
	userMux.HandleFunc("/edit-skill/", h.handleEditSkill)
	userMux.HandleFunc("/delete-skill/", h.handleDeleteSkill)
	userMux.HandleFunc("/skill-history/", h.handleSkillHistory)

	userMux.ServeHTTP(w, r2)
}

// handleUserIndex redirects /~username/ to /~username/wiki/Home.
func (h *Handler) handleUserIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, urlPrefix(r)+"/wiki/Home", http.StatusFound)
}

// cloneURL returns a shallow copy of a URL.
func cloneURL(u *url.URL) *url.URL {
	u2 := *u
	return &u2
}

func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	// Multi-user routing: /~username/... dispatches to per-user wikis.
	if h.userMgr != nil && strings.HasPrefix(r.URL.Path, "/~") {
		h.handleMultiUser(w, r)
		return
	}
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/wiki/Home", http.StatusFound)
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

	if isHTMX(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, template.HTMLEscapeString(plain))
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "content": plain})
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, payload map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, name string, data TemplateData) {
	store := h.storeFor(r)

	favorites, err := store.LoadFavorites()
	if err == nil {
		data.Favorites = favorites
	}

	recent, err := store.RecentPages(5)
	if err == nil {
		data.RecentPages = recent
	}

	if h.docsDir != "" {
		data.DocPages = h.listDocPages()
	}

	if skills, err := store.ListSkillEntries(); err == nil {
		data.Skills = skills
	}

	data.URLPrefix = urlPrefix(r)
	data.WikiOwner = targetUser(r)

	tmpl := h.tmplCache[name]
	if tmpl == nil {
		http.Error(w, "template not found: "+name, http.StatusInternalServerError)
		return
	}

	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// isHTMX returns true when the request was made by htmx.
func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// renderFragment renders only the "content" block without the base layout,
// suitable for htmx partial responses.
func (h *Handler) renderFragment(w http.ResponseWriter, r *http.Request, name string, data TemplateData) {
	data.URLPrefix = urlPrefix(r)
	data.WikiOwner = targetUser(r)
	tmpl := h.tmplCache[name]
	if tmpl == nil {
		http.Error(w, "template not found: "+name, http.StatusInternalServerError)
		return
	}
	if err := tmpl.ExecuteTemplate(w, "content", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
