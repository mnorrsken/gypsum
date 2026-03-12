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
	store      *PageStore
	autoCommit *GitAutoCommitter
	sessions   sync.Map // sessionID → true
}

func NewMCPHandler(store *PageStore, autoCommitter *GitAutoCommitter) *MCPHandler {
	return &MCPHandler{
		store:      store,
		autoCommit: autoCommitter,
	}
}

func (m *MCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// CORS headers — required for Claude's remote MCP connector
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Mcp-Session-Id")
	w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")

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

	resp := m.handleRPC(req, w)
	if resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (m *MCPHandler) handleRPC(req jsonRPCRequest, w http.ResponseWriter) *jsonRPCResponse {
	switch req.Method {
	case "initialize":
		sid := m.newSession()
		w.Header().Set("Mcp-Session-Id", sid)
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

// ── Tool definitions ────────────────────────────────────────────────────

func (m *MCPHandler) toolDefinitions() []mcpTool {
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
				wikiFormattingGuide,
			InputSchema: mcpSchema("object", map[string]any{
				"title":   mcpPropString("Page title, e.g. 'My New Page'. This becomes the slug and the display title."),
				"content": mcpPropString("Markdown content for the page. " + wikiContentGuide),
			}, []string{"title", "content"}),
		},
		{
			Name: "edit_page",
			Description: "Update the content of an existing wiki page. Replaces the entire page content. " +
				"Always use get_page first to read the current content before editing. " +
				wikiFormattingGuide,
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
			Description: "Search across all wiki pages by keyword. Returns matching pages with excerpts.",
			InputSchema: mcpSchema("object", map[string]any{
				"query": mcpPropString("Search query string"),
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
	return mcpText(page.Content)
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

	slug := SlugFromTitle(title)
	if _, err := m.store.Load(slug); err == nil {
		return mcpError("page already exists: " + slug)
	}

	if err := m.store.Save(slug, content); err != nil {
		return mcpError("failed to save page: " + err.Error())
	}
	_ = m.autoCommit.CommitPageSave(slug)
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

	if _, err := m.store.Load(slug); err != nil {
		return mcpError("page not found: " + slug)
	}

	if err := m.store.Save(slug, content); err != nil {
		return mcpError("failed to save page: " + err.Error())
	}
	_ = m.autoCommit.CommitPageSave(slug)
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
		fmt.Fprintf(&sb, "## %s\n**Slug:** %s\n%s\n\n", r.Title, r.Slug, r.Excerpt)
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
	_ = m.autoCommit.CommitImageDelete(filename)
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
	return mcpText(content)
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
