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
	sessions   sync.Map            // sessionID → true
	oauth      *OAuthServer        // non-nil → Bearer token required
	sections   map[MCPSection]bool // enabled tool sections
	metrics    *MCPMetrics         // optional; nil = no metrics
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
		if m.metrics != nil {
			sent := len(req.Params)
			received := 0
			for _, c := range result.Content {
				received += len(c.Text)
			}
			m.metrics.Record(params.Name, sent, received, result.IsError)
		}
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

func (m *MCPHandler) newSession() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	sid := hex.EncodeToString(b)
	m.sessions.Store(sid, true)
	return sid
}
