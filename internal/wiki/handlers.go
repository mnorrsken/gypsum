package wiki

import (
	"encoding/json"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"
)

type Handler struct {
	store       *PageStore
	renderer    *MarkdownRenderer
	templates   fs.FS
	docsDir     string // path to docs/ directory; empty = no docs section
	autoCommit  *GitAutoCommitter
	oauth       *OAuthServer        // non-nil → register /mcp/external + OAuth discovery routes
	db          *DB                 // SQLite database for shares and token storage
	mcpSections map[MCPSection]bool // enabled MCP tool sections
	mcpMetrics  *MCPMetrics         // shared across all MCP handlers
	tmplCache   map[string]*template.Template
	secureSalt  string       // base64 PBKDF2 salt served to the browser for {{secure_aes2}}
	mcpOrigins  []string     // Origin allowlist for the MCP endpoint
	siteImages  *http.Client // HTTP client for secret site-image fetches (nil = default)
}

// SetMCPAllowedOrigins sets the Origin allowlist enforced on /mcp. Build it
// with ParseMCPOrigins; loopback origins are always permitted.
func (h *Handler) SetMCPAllowedOrigins(origins []string) {
	h.mcpOrigins = origins
}

// SetSecureSalt sets the base64-encoded per-deployment PBKDF2 salt that is
// injected into every page so the browser can derive the secure_aes2 key.
func (h *Handler) SetSecureSalt(saltB64 string) {
	h.secureSalt = saltB64
}

type ImageInfo struct {
	Name    string
	URL     string
	Size    int64
	ModTime time.Time
	UsedBy  []string // page slugs referencing this image
}

type TemplateData struct {
	Title         string
	Sidebar       []PageLink
	Favorites     []PageLink
	RecentPages   []PageLink
	AllPages      []PageLink
	Page          *Page
	RenderedHTML  template.HTML
	RawContent    string
	Query         string
	Results       []SearchResult
	Images        []ImageInfo
	History       []HistoryEntry
	GlobalEdits   []GlobalHistoryEntry
	CurrentPage   int
	TotalPages    int
	IsNew         bool
	DiffHTML      template.HTML
	GraphJSON     template.JS
	ShareToken    string           // non-empty if page has an active share link
	ShareURL      string           // full public URL for the share link
	ShareCreated  string           // human-readable creation time
	DocPages      []PageLink       // documentation pages from docs/
	Skills        []SkillListEntry // skill pages with tags
	SkillTags     []string         // tags for the current skill being viewed
	Notes         []NoteEntry      // quick notes for the notes board
	NotesArchived bool             // true when rendering the archived notes board
	Secrets       []SecretEntry    // vault entries for the secrets view
	SecretHold    int              // seconds a revealed secret stays visible
	SecretHues    int              // number of mnemonic tile colors
	SecureSalt    string           // base64 PBKDF2 salt for {{secure_aes2}}
	SecureIters   int              // PBKDF2 iteration count
}

func NewHandler(store *PageStore, renderer *MarkdownRenderer, templates fs.FS, autoCommitter *GitAutoCommitter, oauth *OAuthServer, db *DB, mcpSections map[MCPSection]bool) *Handler {
	h := &Handler{
		store:       store,
		renderer:    renderer,
		templates:   templates,
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
	if h.templates == nil {
		return
	}
	const basePath = "base.html"
	names := []string{
		"view", "edit", "new", "search", "pages", "history",
		"history_diff", "images", "diff", "graph", "recent_edits", "share",
		"doc", "docs",
		"skills", "skill_view", "skill_edit",
		"notes",
		"secrets",
	}
	for _, name := range names {
		pagePath := name + ".html"
		// Skip templates that aren't present in the FS (test environments
		// may pass an empty FS). Log every other parse error — the usual
		// cause is a stray "{{" in a script block tripping the
		// html/template lexer, and silently dropping the template makes
		// that very hard to diagnose.
		if _, statErr := fs.Stat(h.templates, pagePath); statErr != nil {
			continue
		}
		tmpl, err := template.New("").Funcs(templateFuncs).ParseFS(h.templates, basePath, pagePath)
		if err != nil {
			log.Printf("template %q failed to parse: %v", name, err)
			continue
		}
		h.tmplCache[name] = tmpl
	}

	// Public page template is standalone (no base layout).
	const publicPath = "public.html"
	if tmpl, err := template.New("").Funcs(templateFuncs).ParseFS(h.templates, publicPath); err == nil {
		h.tmplCache["public"] = tmpl
	}

	// Partial templates (standalone fragments for htmx responses).
	partials := []string{"image_grid"}
	for _, name := range partials {
		partialPath := "partials/" + name + ".html"
		tmpl, err := template.New("").Funcs(templateFuncs).ParseFS(h.templates, partialPath)
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
	mux.HandleFunc("GET /git-status", h.handleGitStatus)
	mux.HandleFunc("/convert/mediawiki", h.handleConvertMediaWiki)
	mux.HandleFunc("/skills", h.handleSkillsList)
	mux.HandleFunc("/skills/", h.handleSkillView)
	mux.HandleFunc("/new-skill", h.handleNewSkill)
	mux.HandleFunc("/edit-skill/", h.handleEditSkill)
	mux.HandleFunc("/delete-skill/", h.handleDeleteSkill)
	mux.HandleFunc("/skill-history/", h.handleSkillHistory)
	mux.HandleFunc("GET /notes", h.handleNotesBoard)
	mux.HandleFunc("GET /notes/archived", h.handleNotesBoard)
	mux.HandleFunc("POST /notes/create", h.handleNoteCreate)
	mux.HandleFunc("POST /notes/save/{id}", h.handleNoteSave)
	mux.HandleFunc("POST /notes/archive/{id}", h.handleNoteArchive)
	mux.HandleFunc("POST /notes/restore/{id}", h.handleNoteRestore)
	mux.HandleFunc("POST /notes/delete/{id}", h.handleNoteDelete)
	mux.HandleFunc("GET /secrets", h.handleSecretsVault)
	mux.HandleFunc("POST /secrets/create", h.handleSecretCreate)
	mux.HandleFunc("POST /secrets/save/{id}", h.handleSecretSave)
	mux.HandleFunc("POST /secrets/delete/{id}", h.handleSecretDelete)
	mux.HandleFunc("POST /secrets/image/{id}", h.handleSecretImage)
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
		mcpOAuth.SetAllowedOrigins(h.mcpOrigins)
		mux.Handle("/mcp", RateLimit(mcpRL, mcpOAuth))
		mux.Handle("/mcp/external", RateLimit(mcpRL, mcpOAuth))
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

// handleGitStatus reports remote git-sync health as JSON so the top-bar
// indicator can show whether syncing is healthy (green) or failing (red).
func (h *Handler) handleGitStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(h.autoCommit.SyncStatus())
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, payload map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
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

	data.SecureSalt = h.secureSalt
	data.SecureIters = SecurePBKDF2Iterations

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
func (h *Handler) renderFragment(w http.ResponseWriter, name string, data TemplateData) {
	tmpl := h.tmplCache[name]
	if tmpl == nil {
		http.Error(w, "template not found: "+name, http.StatusInternalServerError)
		return
	}
	if err := tmpl.ExecuteTemplate(w, "content", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
