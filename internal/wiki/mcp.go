package wiki

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
)

// ── Wiki formatting guides (embedded in MCP tool descriptions) ─────────

const wikiFormattingGuide = "Wiki formatting conventions: " +
	"(1) If the page starts with a level-1 heading (# Title), that heading becomes the page title displayed in the browser — it is not rendered again in the body. " +
	"(2) Use [[Page Title]] to link to other wiki pages; the title is auto-converted to a slug (spaces → underscores). Linking to a non-existent page will let users create it. " +
	"(3) Images: use ![alt text](/images/filename.ext) — optional size hints: ![alt|500](/images/f.png) for max-width 500px, ![alt|50%](/images/f.png) for 50%, ![alt|800x400](/images/f.png) for explicit dimensions. " +
	"(4) Secure/encrypted fields: use {{secure:plaintext}} for inline secrets. For multiline secrets, put {{secure: and }} on their own lines. On save, these are encrypted to {{secure_aes:...}} — never modify secure_aes blocks directly."

// wikiFormattingGuideExternal is used on the /mcp/external endpoint where secure fields are redacted.
const wikiFormattingGuideExternal = "Wiki formatting conventions: " +
	"(1) If the page starts with a level-1 heading (# Title), that heading becomes the page title displayed in the browser — it is not rendered again in the body. " +
	"(2) Use [[Page Title]] to link to other wiki pages; the title is auto-converted to a slug (spaces → underscores). Linking to a non-existent page will let users create it. " +
	"(3) Images: use ![alt text](/images/filename.ext) — optional size hints: ![alt|500](/images/f.png) for max-width 500px, ![alt|50%](/images/f.png) for 50%, ![alt|800x400](/images/f.png) for explicit dimensions. " +
	"(4) Secure/encrypted fields: pages may contain {{secure_aes:...}} blocks (shown as [encrypted field]). " +
	"Pages with encrypted fields cannot be edited via this endpoint — use the local wiki UI or internal MCP endpoint instead."

const wikiContentGuide = "Start with '# Page Title' as the first line to set the display title. " +
	"Use [[Page Title]] for wiki links. " +
	"Reference images as ![alt](/images/filename.ext). " +
	"Use {{secure:secret}} for encrypted inline fields."

// ── JSON-RPC 2.0 types ─────────────────────────────────────────────────

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any           `json:"result,omitempty"`
	Error   *jsonRPCError `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ── MCP protocol types ─────────────────────────────────────────────────

type mcpInitializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      mcpServerInfo  `json:"serverInfo"`
}

type mcpServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type mcpTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

type mcpToolsListResult struct {
	Tools []mcpTool `json:"tools"`
}

type mcpToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpCallToolResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

// ── MCP Handler ─────────────────────────────────────────────────────────

// MCPHandler serves the MCP Streamable HTTP transport at a single endpoint.
// Claude's custom connector POSTs JSON-RPC messages here.
type MCPHandler struct {
	store         *PageStore
	autoCommit    *GitAutoCommitter
	sessions      sync.Map // sessionID → true
	oauth         *OAuthServer // non-nil → Bearer token required
	redactSecure  bool         // when true, {{secure_aes:...}} blocks are hidden in read results
}

// NewMCPHandler creates an internal (unauthenticated) MCP handler.
func NewMCPHandler(store *PageStore, autoCommitter *GitAutoCommitter) *MCPHandler {
	return &MCPHandler{
		store:      store,
		autoCommit: autoCommitter,
	}
}

// NewMCPHandlerExternal creates an OAuth-protected MCP handler that redacts
// encrypted secure fields from all read results and blocks edits on pages that
// contain them.
func NewMCPHandlerExternal(store *PageStore, autoCommitter *GitAutoCommitter, oauth *OAuthServer) *MCPHandler {
	return &MCPHandler{
		store:        store,
		autoCommit:   autoCommitter,
		oauth:        oauth,
		redactSecure: true,
	}
}

func (m *MCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// CORS headers — required for Claude's remote MCP connector
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Mcp-Session-Id, Authorization")
	w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")

	// OAuth bearer token check (external endpoint only)
	if m.oauth != nil && r.Method != http.MethodOptions {
		if !m.oauth.ValidateBearer(r) {
			w.Header().Set("WWW-Authenticate",
				`Bearer resource_metadata="`+m.oauth.externalURL+`/.well-known/oauth-protected-resource"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	switch r.Method {
	case http.MethodOptions:
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPost:
		m.handlePost(w, r)
	case http.MethodGet:
		// SSE endpoint for server-initiated messages — not needed for our tools
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		// Keep connection open; we have nothing to push, so just return.
	case http.MethodDelete:
		// Session termination
		sid := r.Header.Get("Mcp-Session-Id")
		if sid != "" {
			m.sessions.Delete(sid)
		}
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (m *MCPHandler) handlePost(w http.ResponseWriter, r *http.Request) {
	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Handle notifications (no ID = no response expected)
	if req.ID == nil || string(req.ID) == "" {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	resp := m.HandleRPC(req, func(k, v string) { w.Header().Set(k, v) })
	if resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleRPC processes a single JSON-RPC request and returns a response.
// Returns nil for notifications that need no response.
// The optional headerFn is called to set HTTP headers (ignored for stdio).
func (m *MCPHandler) HandleRPC(req jsonRPCRequest, headerFn func(key, value string)) *jsonRPCResponse {
	switch req.Method {
	case "initialize":
		sid := m.newSession()
		if headerFn != nil {
			headerFn("Mcp-Session-Id", sid)
		}
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: mcpInitializeResult{
				ProtocolVersion: "2025-03-26",
				Capabilities: map[string]any{
					"tools": map[string]any{},
				},
				ServerInfo: mcpServerInfo{
					Name:    "gypsum-wiki",
					Version: "0.1.0",
				},
			},
		}

	case "notifications/initialized":
		return nil

	case "tools/list":
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  mcpToolsListResult{Tools: m.toolDefinitions()},
		}

	case "tools/call":
		var params mcpToolCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &jsonRPCError{Code: -32602, Message: "invalid params: " + err.Error()},
			}
		}
		result := m.callTool(params)
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  result,
		}

	case "ping":
		return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}

	default:
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32601, Message: "method not found: " + req.Method},
		}
	}
}

func (m *MCPHandler) newSession() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	sid := hex.EncodeToString(b)
	m.sessions.Store(sid, true)
	return sid
}

// ── Secure field redaction ───────────────────────────────────────────────

// redactContent replaces {{secure_aes:...}} blobs with a safe placeholder so
// that encrypted values are never exposed over the external MCP endpoint.
func (m *MCPHandler) redactContent(content string) string {
	if !m.redactSecure {
		return content
	}
	return secureAesMacroRe.ReplaceAllString(content, "[encrypted field]")
}

// ── Tool definitions ────────────────────────────────────────────────────

func (m *MCPHandler) toolDefinitions() []mcpTool {
	formattingGuide := wikiFormattingGuide
	if m.redactSecure {
		formattingGuide = wikiFormattingGuideExternal
	}

	return []mcpTool{
		{
			Name:        "list_pages",
			Description: "List all wiki pages (alphabetically sorted). Returns page slugs and titles.",
			InputSchema: mcpSchema("object", nil, nil),
		},
		{
			Name: "get_page",
			Description: "Get the raw markdown content of a wiki page. " +
				"The returned content is the full markdown source including any wiki-specific syntax.",
			InputSchema: mcpSchema("object", map[string]any{
				"slug": mcpPropString("Page slug, e.g. 'Home' or 'My_Page'. Slugs use underscores for spaces."),
			}, []string{"slug"}),
		},
		{
			Name: "create_page",
			Description: "Create a new wiki page. Fails if the page already exists. " +
				"The slug is derived from the title (spaces become underscores, e.g. 'My Page' → 'My_Page'). " +
				"IMPORTANT: After creating a page, always add a [[Page Title]] link to it from at least one parent page (e.g. Home or a relevant category page) so it is discoverable. " +
				formattingGuide,
			InputSchema: mcpSchema("object", map[string]any{
				"title":   mcpPropString("Page title, e.g. 'My New Page'. This becomes the slug and the display title."),
				"content": mcpPropString("Markdown content for the page. " + wikiContentGuide),
			}, []string{"title", "content"}),
		},
		{
			Name: "edit_page",
			Description: "Update the content of an existing wiki page. Replaces the entire page content. " +
				"Always use get_page first to read the current content before editing. " +
				"When adding [[wiki links]] to new pages, make sure those pages exist or will be created. " +
				formattingGuide,
			InputSchema: mcpSchema("object", map[string]any{
				"slug":    mcpPropString("Page slug to edit, e.g. 'My_Page'"),
				"content": mcpPropString("New markdown content (replaces entire page). " + wikiContentGuide),
			}, []string{"slug", "content"}),
		},
		{
			Name:        "delete_page",
			Description: "Delete a wiki page permanently.",
			InputSchema: mcpSchema("object", map[string]any{
				"slug": mcpPropString("Page slug to delete"),
			}, []string{"slug"}),
		},
		{
			Name:        "search_pages",
			Description: "Search across all wiki pages by keyword. The query is split into terms (punctuation like & is ignored). Each term is matched independently against page titles and content — prefix matches work, so 'arch' finds 'architecture'. Results are ranked by relevance: title matches score higher than content matches, and pages matching all terms are boosted.",
			InputSchema: mcpSchema("object", map[string]any{
				"query": mcpPropString("Search query — split into terms on whitespace/punctuation, each matched independently (prefix and substring)"),
			}, []string{"query"}),
		},
		{
			Name:        "list_images",
			Description: "List all uploaded images with metadata (name, size, modification time, which pages use them).",
			InputSchema: mcpSchema("object", nil, nil),
		},
		{
			Name:        "delete_image",
			Description: "Delete an uploaded image by filename.",
			InputSchema: mcpSchema("object", map[string]any{
				"filename": mcpPropString("Image filename, e.g. 'photo-20250101-ab12cd34.png'"),
			}, []string{"filename"}),
		},
		{
			Name:        "get_recent_pages",
			Description: "Get the most recently modified wiki pages.",
			InputSchema: mcpSchema("object", map[string]any{
				"count": map[string]any{"type": "number", "description": "Number of pages to return (default 10)"},
			}, nil),
		},
		{
			Name:        "get_favorites",
			Description: "Get the list of favorite/pinned pages from the wiki sidebar.",
			InputSchema: mcpSchema("object", nil, nil),
		},
		{
			Name:        "page_history",
			Description: "Get the git revision history for a wiki page.",
			InputSchema: mcpSchema("object", map[string]any{
				"slug":  mcpPropString("Page slug"),
				"count": map[string]any{"type": "number", "description": "Max entries to return (default 20)"},
			}, []string{"slug"}),
		},
		{
			Name:        "get_page_revision",
			Description: "Get the content of a wiki page at a specific git revision.",
			InputSchema: mcpSchema("object", map[string]any{
				"slug": mcpPropString("Page slug"),
				"hash": mcpPropString("Git commit hash"),
			}, []string{"slug", "hash"}),
		},
		{
			Name:        "page_links",
			Description: "Get all outgoing wiki links from a page. Returns the slugs of pages that this page links to via [[Page Title]] syntax.",
			InputSchema: mcpSchema("object", map[string]any{
				"slug": mcpPropString("Page slug to inspect"),
			}, []string{"slug"}),
		},
		{
			Name: "what_links_here",
			Description: "Find all pages that link to a given page (backlinks/parent pages). " +
				"Every page should be linked from at least one other page to be discoverable in the wiki.",
			InputSchema: mcpSchema("object", map[string]any{
				"slug": mcpPropString("Target page slug to find backlinks for"),
			}, []string{"slug"}),
		},
		{
			Name: "link_graph",
			Description: "Get the full wiki link graph. Returns a map of every page slug to the list of slugs it links to. " +
				"Useful for understanding the overall wiki structure and finding orphaned pages.",
			InputSchema: mcpSchema("object", nil, nil),
		},
		{
			Name: "create_page_from_mediawiki",
			Description: "Create a new wiki page from MediaWiki wikitext. ONLY use this tool when importing content from a MediaWiki source — " +
				"for normal page creation, use create_page with Markdown instead. " +
				"The wikitext is automatically converted to Markdown. " +
				"Handles: '''bold'''/''italic'', == headings ==, <syntaxhighlight>/<source>/<pre>/<nowiki>/<code>, " +
				"* and # lists, {| tables |}, [[wiki links]], [external links], categories, templates, refs, and " +
				"MediaWiki space-prefixed preformatted lines. " +
				"Fails if the page already exists. " +
				"IMPORTANT: After creating a page, always add a [[Page Title]] link to it from at least one parent page.",
			InputSchema: mcpSchema("object", map[string]any{
				"title":    mcpPropString("Page title, e.g. 'My New Page'. This becomes the slug and display title."),
				"wikitext": mcpPropString("MediaWiki wikitext source to convert and save as the page content."),
			}, []string{"title", "wikitext"}),
		},
		{
			Name: "edit_page_from_mediawiki",
			Description: "Update an existing wiki page from MediaWiki wikitext. ONLY use this tool when importing content from a MediaWiki source — " +
				"for normal page editing, use edit_page with Markdown instead. " +
				"The wikitext is automatically converted to Markdown and replaces the entire page content. " +
				"Handles: '''bold'''/''italic'', == headings ==, <syntaxhighlight>/<source>/<pre>/<nowiki>/<code>, " +
				"* and # lists, {| tables |}, [[wiki links]], [external links], categories, templates, refs, and " +
				"MediaWiki space-prefixed preformatted lines.",
			InputSchema: mcpSchema("object", map[string]any{
				"slug":     mcpPropString("Page slug to edit, e.g. 'My_Page'"),
				"wikitext": mcpPropString("MediaWiki wikitext source to convert and save as the new page content."),
			}, []string{"slug", "wikitext"}),
		},
	}
}

// ── Tool dispatch ───────────────────────────────────────────────────────

func (m *MCPHandler) callTool(params mcpToolCallParams) mcpCallToolResult {
	switch params.Name {
	case "list_pages":
		return m.toolListPages()
	case "get_page":
		return m.toolGetPage(params.Arguments)
	case "create_page":
		return m.toolCreatePage(params.Arguments)
	case "edit_page":
		return m.toolEditPage(params.Arguments)
	case "delete_page":
		return m.toolDeletePage(params.Arguments)
	case "search_pages":
		return m.toolSearchPages(params.Arguments)
	case "list_images":
		return m.toolListImages()
	case "delete_image":
		return m.toolDeleteImage(params.Arguments)
	case "get_recent_pages":
		return m.toolGetRecentPages(params.Arguments)
	case "get_favorites":
		return m.toolGetFavorites()
	case "page_history":
		return m.toolPageHistory(params.Arguments)
	case "get_page_revision":
		return m.toolGetPageRevision(params.Arguments)
	case "page_links":
		return m.toolPageLinks(params.Arguments)
	case "what_links_here":
		return m.toolWhatLinksHere(params.Arguments)
	case "link_graph":
		return m.toolLinkGraph()
	case "create_page_from_mediawiki":
		return m.toolCreatePageFromMediaWiki(params.Arguments)
	case "edit_page_from_mediawiki":
		return m.toolEditPageFromMediaWiki(params.Arguments)
	default:
		return mcpError("unknown tool: " + params.Name)
	}
}

// ── Tool implementations ────────────────────────────────────────────────

func (m *MCPHandler) toolListPages() mcpCallToolResult {
	pages, err := m.store.List()
	if err != nil {
		return mcpError("failed to list pages: " + err.Error())
	}
	return mcpJSON(pages)
}

func (m *MCPHandler) toolGetPage(args map[string]any) mcpCallToolResult {
	slug, ok := mcpArgString(args, "slug")
	if !ok {
		return mcpError("missing required argument: slug")
	}
	page, err := m.store.Load(slug)
	if err != nil {
		return mcpError("page not found: " + slug)
	}
	return mcpText(m.redactContent(page.Content))
}

func (m *MCPHandler) toolCreatePage(args map[string]any) mcpCallToolResult {
	title, ok := mcpArgString(args, "title")
	if !ok {
		return mcpError("missing required argument: title")
	}
	content, ok := mcpArgString(args, "content")
	if !ok {
		return mcpError("missing required argument: content")
	}

	if m.redactSecure && secureAesMacroRe.MatchString(content) {
		return mcpError("content contains encrypted fields ({{secure_aes:...}}); creating pages with encrypted fields is not supported via this endpoint")
	}

	slug := SlugFromTitle(title)
	if _, err := m.store.Load(slug); err == nil {
		return mcpError("page already exists: " + slug)
	}

	if err := m.store.Save(slug, content); err != nil {
		return mcpError("failed to save page: " + err.Error())
	}
	_ = m.autoCommit.CommitPageSave(slug, "")
	return mcpText(fmt.Sprintf("Created page '%s' (slug: %s)", title, slug))
}

func (m *MCPHandler) toolEditPage(args map[string]any) mcpCallToolResult {
	slug, ok := mcpArgString(args, "slug")
	if !ok {
		return mcpError("missing required argument: slug")
	}
	content, ok := mcpArgString(args, "content")
	if !ok {
		return mcpError("missing required argument: content")
	}

	existing, err := m.store.Load(slug)
	if err != nil {
		return mcpError("page not found: " + slug)
	}

	if m.redactSecure && secureAesMacroRe.MatchString(existing.Content) {
		return mcpError("page '" + slug + "' contains encrypted fields; editing pages with encrypted fields is not supported via this endpoint — use the local wiki UI or internal MCP endpoint")
	}

	if err := m.store.Save(slug, content); err != nil {
		return mcpError("failed to save page: " + err.Error())
	}
	_ = m.autoCommit.CommitPageSave(slug, "")
	return mcpText(fmt.Sprintf("Updated page '%s'", slug))
}

func (m *MCPHandler) toolDeletePage(args map[string]any) mcpCallToolResult {
	slug, ok := mcpArgString(args, "slug")
	if !ok {
		return mcpError("missing required argument: slug")
	}

	if _, err := m.store.Load(slug); err != nil {
		return mcpError("page not found: " + slug)
	}

	path := m.store.PagePath(slug)
	if err := os.Remove(path); err != nil {
		return mcpError("failed to delete page: " + err.Error())
	}
	_ = m.autoCommit.CommitPageDelete(slug, "")
	return mcpText(fmt.Sprintf("Deleted page '%s'", slug))
}

func (m *MCPHandler) toolSearchPages(args map[string]any) mcpCallToolResult {
	query, ok := mcpArgString(args, "query")
	if !ok {
		return mcpError("missing required argument: query")
	}

	results, err := m.store.Search(query)
	if err != nil {
		return mcpError("search failed: " + err.Error())
	}
	if len(results) == 0 {
		return mcpText("No results found for: " + query)
	}

	var sb strings.Builder
	for _, r := range results {
		excerpt := m.redactContent(r.Excerpt)
		fmt.Fprintf(&sb, "## %s\n**Slug:** %s\n%s\n\n", r.Title, r.Slug, excerpt)
	}
	return mcpText(sb.String())
}

func (m *MCPHandler) toolListImages() mcpCallToolResult {
	images, err := m.store.ListImages()
	if err != nil {
		return mcpError("failed to list images: " + err.Error())
	}
	if len(images) == 0 {
		return mcpText("No images uploaded.")
	}
	return mcpJSON(images)
}

func (m *MCPHandler) toolDeleteImage(args map[string]any) mcpCallToolResult {
	filename, ok := mcpArgString(args, "filename")
	if !ok {
		return mcpError("missing required argument: filename")
	}
	if strings.Contains(filename, "/") || strings.Contains(filename, "..") {
		return mcpError("invalid filename")
	}

	imgPath := m.store.ImagePath(filename)
	if err := os.Remove(imgPath); err != nil {
		return mcpError("image not found: " + filename)
	}
	_ = m.autoCommit.CommitImageDelete(filename, "")
	return mcpText(fmt.Sprintf("Deleted image '%s'", filename))
}



func (m *MCPHandler) toolGetRecentPages(args map[string]any) mcpCallToolResult {
	count := 10
	if n, ok := mcpArgNumber(args, "count"); ok && n > 0 {
		count = int(n)
	}
	pages, err := m.store.RecentPages(count)
	if err != nil {
		return mcpError("failed to get recent pages: " + err.Error())
	}
	return mcpJSON(pages)
}

func (m *MCPHandler) toolGetFavorites() mcpCallToolResult {
	favs, err := m.store.LoadFavorites()
	if err != nil {
		return mcpError("failed to load favorites: " + err.Error())
	}
	if len(favs) == 0 {
		return mcpText("No favorites set.")
	}
	return mcpJSON(favs)
}

func (m *MCPHandler) toolPageHistory(args map[string]any) mcpCallToolResult {
	slug, ok := mcpArgString(args, "slug")
	if !ok {
		return mcpError("missing required argument: slug")
	}
	count := 20
	if n, ok := mcpArgNumber(args, "count"); ok && n > 0 {
		count = int(n)
	}

	entries, err := m.autoCommit.PageHistory(slug, count)
	if err != nil {
		return mcpError("failed to get history: " + err.Error())
	}
	if len(entries) == 0 {
		return mcpText("No history available for: " + slug)
	}
	return mcpJSON(entries)
}

func (m *MCPHandler) toolGetPageRevision(args map[string]any) mcpCallToolResult {
	slug, ok := mcpArgString(args, "slug")
	if !ok {
		return mcpError("missing required argument: slug")
	}
	hash, ok := mcpArgString(args, "hash")
	if !ok {
		return mcpError("missing required argument: hash")
	}

	content, err := m.autoCommit.PageContentAtRevision(slug, hash)
	if err != nil {
		return mcpError("failed to get revision: " + err.Error())
	}
	return mcpText(m.redactContent(content))
}

func (m *MCPHandler) toolPageLinks(args map[string]any) mcpCallToolResult {
	slug, ok := mcpArgString(args, "slug")
	if !ok {
		return mcpError("missing required argument: slug")
	}
	page, err := m.store.Load(slug)
	if err != nil {
		return mcpError("page not found: " + slug)
	}
	links := ExtractWikiLinks(page.Content)
	if len(links) == 0 {
		return mcpText("Page '" + slug + "' has no outgoing wiki links.")
	}
	return mcpJSON(links)
}

func (m *MCPHandler) toolWhatLinksHere(args map[string]any) mcpCallToolResult {
	slug, ok := mcpArgString(args, "slug")
	if !ok {
		return mcpError("missing required argument: slug")
	}
	backlinks, err := m.store.BackLinks(slug)
	if err != nil {
		return mcpError("failed to compute backlinks: " + err.Error())
	}
	if len(backlinks) == 0 {
		return mcpText("No pages link to '" + slug + "'. This page is orphaned — consider linking it from a parent page.")
	}
	return mcpJSON(backlinks)
}

func (m *MCPHandler) toolCreatePageFromMediaWiki(args map[string]any) mcpCallToolResult {
	title, ok := mcpArgString(args, "title")
	if !ok {
		return mcpError("missing required argument: title")
	}
	wikitext, ok := mcpArgString(args, "wikitext")
	if !ok {
		return mcpError("missing required argument: wikitext")
	}

	slug := SlugFromTitle(title)
	if _, err := m.store.Load(slug); err == nil {
		return mcpError("page already exists: " + slug)
	}

	content := ConvertMediaWikiToMarkdown(wikitext)
	if m.redactSecure && secureAesMacroRe.MatchString(content) {
		return mcpError("converted content contains encrypted fields; creating pages with encrypted fields is not supported via this endpoint")
	}
	if err := m.store.Save(slug, content); err != nil {
		return mcpError("failed to save page: " + err.Error())
	}
	_ = m.autoCommit.CommitPageSave(slug, "")
	return mcpText(fmt.Sprintf("Created page '%s' (slug: %s) from MediaWiki source", title, slug))
}

func (m *MCPHandler) toolEditPageFromMediaWiki(args map[string]any) mcpCallToolResult {
	slug, ok := mcpArgString(args, "slug")
	if !ok {
		return mcpError("missing required argument: slug")
	}
	wikitext, ok := mcpArgString(args, "wikitext")
	if !ok {
		return mcpError("missing required argument: wikitext")
	}

	existing, err := m.store.Load(slug)
	if err != nil {
		return mcpError("page not found: " + slug)
	}

	if m.redactSecure && secureAesMacroRe.MatchString(existing.Content) {
		return mcpError("page '" + slug + "' contains encrypted fields; editing pages with encrypted fields is not supported via this endpoint — use the local wiki UI or internal MCP endpoint")
	}

	content := ConvertMediaWikiToMarkdown(wikitext)
	if err := m.store.Save(slug, content); err != nil {
		return mcpError("failed to save page: " + err.Error())
	}
	_ = m.autoCommit.CommitPageSave(slug, "")
	return mcpText(fmt.Sprintf("Updated page '%s' from MediaWiki source", slug))
}

func (m *MCPHandler) toolLinkGraph() mcpCallToolResult {
	graph, err := m.store.LinkGraph()
	if err != nil {
		return mcpError("failed to build link graph: " + err.Error())
	}
	return mcpJSON(graph)
}

// ── Helpers ─────────────────────────────────────────────────────────────

func mcpArgString(args map[string]any, key string) (string, bool) {
	v, ok := args[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func mcpArgNumber(args map[string]any, key string) (float64, bool) {
	v, ok := args[key]
	if !ok {
		return 0, false
	}
	n, ok := v.(float64)
	return n, ok
}

func mcpText(text string) mcpCallToolResult {
	return mcpCallToolResult{
		Content: []mcpContent{{Type: "text", Text: text}},
	}
}

func mcpJSON(v any) mcpCallToolResult {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcpError("failed to marshal result: " + err.Error())
	}
	return mcpCallToolResult{
		Content: []mcpContent{{Type: "text", Text: string(data)}},
	}
}

func mcpError(msg string) mcpCallToolResult {
	return mcpCallToolResult{
		Content: []mcpContent{{Type: "text", Text: msg}},
		IsError: true,
	}
}

func mcpSchema(typ string, properties map[string]any, required []string) map[string]any {
	schema := map[string]any{"type": typ}
	if properties != nil {
		schema["properties"] = properties
	}
	if required != nil {
		schema["required"] = required
	}
	return schema
}

func mcpPropString(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
