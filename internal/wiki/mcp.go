package wiki

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
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
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
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

const (
	mcpServerName    = "gypsum-wiki"
	mcpServerVersion = "0.1.0"
)

// MCPSection identifies a group of MCP tools that can be enabled/disabled.
type MCPSection string

const (
	MCPSectionRead   MCPSection = "read"
	MCPSectionEdit   MCPSection = "edit"
	MCPSectionDelete MCPSection = "delete"
	MCPSectionSkills MCPSection = "skills"
	MCPSectionNotes  MCPSection = "notes"
)

// AllMCPSections is the default set when no restriction is configured.
var AllMCPSections = map[MCPSection]bool{
	MCPSectionRead:   true,
	MCPSectionEdit:   true,
	MCPSectionDelete: true,
	MCPSectionSkills: true,
	MCPSectionNotes:  true,
}

// ParseMCPSections parses a comma-separated list of section names (e.g.
// "read,edit,skills") into a set. Returns AllMCPSections if input is empty.
func ParseMCPSections(s string) map[MCPSection]bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return AllMCPSections
	}
	sections := make(map[MCPSection]bool)
	for _, part := range strings.Split(s, ",") {
		sec := MCPSection(strings.TrimSpace(part))
		if sec != "" {
			sections[sec] = true
		}
	}
	return sections
}

type mcpToolAnnotations struct {
	ReadOnlyHint    *bool `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
}

type mcpTool struct {
	Name        string              `json:"name"`
	Title       string              `json:"title,omitempty"` // human-readable display name
	Description string              `json:"description"`
	InputSchema any                 `json:"inputSchema"`
	Annotations *mcpToolAnnotations `json:"annotations,omitempty"`
	Section     MCPSection          `json:"-"` // not exposed in JSON
}

func boolPtr(b bool) *bool { return &b }

// sectionAnnotations returns MCP annotations for a tool based on its section
// and name, so the Claude connector UI can categorize tools correctly.
func sectionAnnotations(section MCPSection, name string) *mcpToolAnnotations {
	switch section {
	case MCPSectionRead:
		return &mcpToolAnnotations{ReadOnlyHint: boolPtr(true)}
	case MCPSectionDelete:
		return &mcpToolAnnotations{DestructiveHint: boolPtr(true)}
	case MCPSectionEdit:
		return &mcpToolAnnotations{DestructiveHint: boolPtr(false)}
	case MCPSectionSkills, MCPSectionNotes:
		switch {
		case strings.HasPrefix(name, "list_"), strings.HasPrefix(name, "get_"), strings.HasPrefix(name, "search_"):
			return &mcpToolAnnotations{ReadOnlyHint: boolPtr(true)}
		case strings.HasPrefix(name, "delete_"):
			return &mcpToolAnnotations{DestructiveHint: boolPtr(true)}
		default:
			return &mcpToolAnnotations{DestructiveHint: boolPtr(false)}
		}
	}
	return nil
}

// mcpToolsListResult answers tools/list. resultType, ttlMs, cacheScope and
// _meta are required by revision 2026-07-28 and omitted for legacy clients,
// which reject unknown fields in some implementations.
type mcpToolsListResult struct {
	ResultType string         `json:"resultType,omitempty"`
	Tools      []mcpTool      `json:"tools"`
	TTLMs      int64          `json:"ttlMs,omitempty"`
	CacheScope string         `json:"cacheScope,omitempty"`
	Meta       map[string]any `json:"_meta,omitempty"`
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
	ResultType string         `json:"resultType,omitempty"` // "complete" on the modern era only
	Content    []mcpContent   `json:"content"`
	IsError    bool           `json:"isError,omitempty"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

// ── MCP Handler ─────────────────────────────────────────────────────────

// MCPHandler serves the MCP Streamable HTTP transport at a single endpoint.
// Claude's custom connector POSTs JSON-RPC messages here.
//
// The handler is dual-era: requests carrying per-request _meta are served
// statelessly under revision 2026-07-28 (see mcp_modern.go), while clients that
// open with initialize get the legacy session-based semantics below.
type MCPHandler struct {
	store          *PageStore
	autoCommit     *GitAutoCommitter
	sessions       sync.Map            // sessionID → true (legacy era only)
	oauth          *OAuthServer        // non-nil → Bearer token required
	sections       map[MCPSection]bool // enabled tool sections
	metrics        *MCPMetrics         // optional; nil = no metrics
	allowedOrigins []string            // Origin allowlist; loopback always permitted
}

// SetAllowedOrigins sets the Origin allowlist enforced on the MCP endpoint.
// Build it with ParseMCPOrigins.
func (m *MCPHandler) SetAllowedOrigins(origins []string) {
	m.allowedOrigins = origins
}

// NewMCPHandler creates an internal (unauthenticated) MCP handler.
func NewMCPHandler(store *PageStore, autoCommitter *GitAutoCommitter, sections map[MCPSection]bool) *MCPHandler {
	return &MCPHandler{
		store:      store,
		autoCommit: autoCommitter,
		sections:   sections,
	}
}

// NewMCPHandlerExternal creates an OAuth-protected MCP handler.
// It behaves identically to NewMCPHandler — {{secure_aes:...}} ciphertext is
// passed through as-is (already encrypted, not plaintext). Registered at
// /mcp/external for backwards compatibility; /mcp is the canonical endpoint.
func NewMCPHandlerExternal(store *PageStore, autoCommitter *GitAutoCommitter, oauth *OAuthServer, sections map[MCPSection]bool) *MCPHandler {
	return &MCPHandler{
		store:      store,
		autoCommit: autoCommitter,
		oauth:      oauth,
		sections:   sections,
	}
}

func (m *MCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Origin validation guards against DNS rebinding: a browser page on an
	// unrelated origin must not be able to drive the wiki's MCP endpoint.
	// Non-browser clients send no Origin at all and are unaffected.
	origin := r.Header.Get("Origin")
	if !m.originAllowed(origin) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(jsonRPCResponse{
			JSONRPC: "2.0",
			Error:   &jsonRPCError{Code: errCodeInvalidRequest, Message: "origin not allowed: " + origin},
		})
		return
	}

	// CORS headers — required for Claude's remote MCP connector
	if origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Add("Vary", "Origin")
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers",
		"Content-Type, Authorization, Mcp-Session-Id, MCP-Protocol-Version, Mcp-Method, Mcp-Name")
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

	// Era selection: a request carrying per-request _meta is stateless and
	// served under 2026-07-28; an initialize request selects legacy semantics.
	meta, metaFound := parseRequestMeta(req.Params)
	if isModernRequest(r, req, metaFound) {
		resp, status := m.handleModernRPC(r, req, meta)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(resp)
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

// HandleRPC processes a single legacy-era (initialize/session) JSON-RPC request
// and returns a response. Returns nil for notifications that need no response.
// The optional headerFn is called to set HTTP headers (ignored for stdio).
//
// Modern (2026-07-28) requests are routed to handleModernRPC instead; see
// mcp_modern.go.
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
				ProtocolVersion: negotiateLegacyVersion(req.Params),
				Capabilities: map[string]any{
					"tools": map[string]any{},
				},
				ServerInfo: mcpServerInfo{
					Name:    mcpServerName,
					Version: mcpServerVersion,
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
		m.recordToolMetrics(params.Name, req.Params, result)
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

// SetMetrics enables Prometheus metrics collection for MCP tool calls.
func (m *MCPHandler) SetMetrics(metrics *MCPMetrics) {
	m.metrics = metrics
}

// recordToolMetrics reports one tools/call to Prometheus, sizing the exchange
// by the raw request params and the text returned.
func (m *MCPHandler) recordToolMetrics(name string, params json.RawMessage, result mcpCallToolResult) {
	if m.metrics == nil {
		return
	}
	received := 0
	for _, c := range result.Content {
		received += len(c.Text)
	}
	m.metrics.Record(name, len(params), received, result.IsError)
}

// negotiateLegacyVersion echoes back the protocol version a legacy client asked
// for when Gypsum supports it, so 2025-06-18 and 2025-11-25 clients are not
// silently downgraded. Unknown versions fall back to the oldest revision that
// uses the Streamable HTTP transport.
func negotiateLegacyVersion(params json.RawMessage) string {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(params, &p); err == nil && isSupportedLegacyVersion(p.ProtocolVersion) {
		return p.ProtocolVersion
	}
	return protocolVersionLegacyFallback
}

func (m *MCPHandler) newSession() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	sid := hex.EncodeToString(b)
	m.sessions.Store(sid, true)
	return sid
}
