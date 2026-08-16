package wiki

// Support for MCP revision 2026-07-28 — the "modern", stateless era of the
// protocol. There is no initialize handshake and no session: every request
// carries its own protocol version and client capabilities in params._meta,
// mirrored into HTTP headers that the server must validate against the body.
//
// Gypsum is a dual-era server, which the spec explicitly permits on a single
// endpoint: a request carrying modern per-request _meta is served statelessly
// here, while an initialize request selects the legacy session semantics in
// mcp.go. See https://modelcontextprotocol.io/specification/2026-07-28/basic/versioning

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// ── Protocol revisions ──────────────────────────────────────────────────

// protocolVersionModern is the stateless revision served by this file.
const protocolVersionModern = "2026-07-28"

// protocolVersionLegacyFallback is echoed from initialize when a legacy client
// asks for a revision Gypsum does not recognize.
const protocolVersionLegacyFallback = "2025-03-26"

// supportedProtocolVersions is advertised by server/discover and listed in
// UnsupportedProtocolVersionError, newest first. Everything below
// protocolVersionModern is served through the legacy initialize handshake, so
// a dual-era client that cannot speak the modern revision can fall back.
var supportedProtocolVersions = []string{
	protocolVersionModern,
	"2025-11-25",
	"2025-06-18",
	"2025-03-26",
	"2024-11-05",
}

// isSupportedLegacyVersion reports whether v is a handshake-era revision we
// echo back from initialize unchanged.
func isSupportedLegacyVersion(v string) bool {
	for _, sv := range supportedProtocolVersions {
		if sv == v && sv != protocolVersionModern {
			return true
		}
	}
	return false
}

// ── Reserved _meta keys and MCP error codes ─────────────────────────────

const (
	metaKeyProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	metaKeyClientInfo         = "io.modelcontextprotocol/clientInfo"
	metaKeyClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
	metaKeyServerInfo         = "io.modelcontextprotocol/serverInfo"
)

// JSON-RPC error codes defined by the MCP spec in its reserved -32020..-32099
// sub-range, plus the standard codes we emit.
const (
	errCodeHeaderMismatch             = -32020
	errCodeUnsupportedProtocolVersion = -32022
	errCodeInvalidRequest             = -32600
	errCodeMethodNotFound             = -32601
	errCodeInvalidParams              = -32602
)

// Caching hints. The spec requires them on server/discover and tools/list
// results. Gypsum's tool set is fixed per deployment and identical for every
// caller, so it is safe to cache publicly.
const (
	mcpCacheTTLMs       = 300000 // 5 minutes
	mcpCacheScopePublic = "public"
)

const mcpServerInstructions = "Gypsum is a git-backed personal wiki. Pages are markdown addressed by slug " +
	"(spaces become underscores) and linked with [[Page Title]]. Prefer search_pages over list_pages to find a " +
	"page, and never guess a slug. Skills hold procedural knowledge for AI retrieval and are found with " +
	"search_skills; notes are short sticky-note jottings. {{secure_aes:...}} blocks are encrypted and must be " +
	"passed through unchanged."

// ── Per-request metadata ────────────────────────────────────────────────

// mcpRequestMeta holds the io.modelcontextprotocol/* fields that every modern
// request carries in params._meta in place of connection state.
type mcpRequestMeta struct {
	ProtocolVersion    string
	ClientInfo         *mcpServerInfo // {name, version}, same shape as serverInfo
	ClientCapabilities map[string]any

	hasProtocolVersion bool
	hasCapabilities    bool
}

// parseRequestMeta extracts the reserved _meta fields from a request's params.
// The bool reports whether any of them were present at all, which is what
// distinguishes a modern request from a legacy one — the body is the source of
// truth for era detection.
func parseRequestMeta(params json.RawMessage) (mcpRequestMeta, bool) {
	var meta mcpRequestMeta
	if len(params) == 0 {
		return meta, false
	}
	var envelope struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if err := json.Unmarshal(params, &envelope); err != nil || envelope.Meta == nil {
		return meta, false
	}

	found := false
	if raw, ok := envelope.Meta[metaKeyProtocolVersion]; ok {
		found = true
		if json.Unmarshal(raw, &meta.ProtocolVersion) == nil && meta.ProtocolVersion != "" {
			meta.hasProtocolVersion = true
		}
	}
	if raw, ok := envelope.Meta[metaKeyClientInfo]; ok {
		found = true
		var info mcpServerInfo
		if json.Unmarshal(raw, &info) == nil {
			meta.ClientInfo = &info
		}
	}
	if raw, ok := envelope.Meta[metaKeyClientCapabilities]; ok {
		found = true
		if json.Unmarshal(raw, &meta.ClientCapabilities) == nil {
			meta.hasCapabilities = true
		}
	}
	return meta, found
}

// isModernRequest reports whether req should be served under the stateless
// 2026-07-28 semantics rather than the legacy handshake.
//
// initialize (and its follow-up notification) always selects the legacy era.
// Otherwise per-request _meta marks a modern request; server/discover and the
// modern protocol-version header are accepted as signals too, so that a
// malformed modern request gets a modern error rather than "method not found".
func isModernRequest(r *http.Request, req jsonRPCRequest, metaFound bool) bool {
	switch req.Method {
	case "initialize", "notifications/initialized":
		return false
	case "server/discover":
		return true
	}
	if metaFound {
		return true
	}
	return r != nil && r.Header.Get("MCP-Protocol-Version") == protocolVersionModern
}

// ── Modern result envelopes ─────────────────────────────────────────────

// mcpDiscoverResult answers server/discover, the mandatory RPC that reports
// which protocol versions and capabilities the server speaks.
type mcpDiscoverResult struct {
	ResultType        string         `json:"resultType"`
	SupportedVersions []string       `json:"supportedVersions"`
	Capabilities      map[string]any `json:"capabilities"`
	Instructions      string         `json:"instructions,omitempty"`
	TTLMs             int64          `json:"ttlMs"`
	CacheScope        string         `json:"cacheScope"`
	Meta              map[string]any `json:"_meta,omitempty"`
}

// modernResultMeta is the _meta block servers should attach to every modern
// result so they identify themselves without relying on connection state.
func modernResultMeta() map[string]any {
	return map[string]any{
		metaKeyServerInfo: mcpServerInfo{Name: mcpServerName, Version: mcpServerVersion},
	}
}

// ── Modern dispatch ─────────────────────────────────────────────────────

// handleModernRPC serves one request under the 2026-07-28 semantics. It
// returns the response together with the HTTP status it must be sent with:
// unlike the legacy era, modern protocol errors travel on 400 and 404 rather
// than 200, which is how a dual-era client tells the two apart.
func (m *MCPHandler) handleModernRPC(r *http.Request, req jsonRPCRequest, meta mcpRequestMeta) (*jsonRPCResponse, int) {
	fail := func(code int, msg string, data any, status int) (*jsonRPCResponse, int) {
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: code, Message: msg, Data: data},
		}, status
	}

	// The body is the source of truth: a request missing a required _meta
	// field is malformed regardless of what the headers claim.
	if !meta.hasProtocolVersion {
		return fail(errCodeInvalidParams,
			"invalid params: missing required _meta field "+metaKeyProtocolVersion,
			nil, http.StatusBadRequest)
	}
	if !meta.hasCapabilities {
		return fail(errCodeInvalidParams,
			"invalid params: missing required _meta field "+metaKeyClientCapabilities,
			nil, http.StatusBadRequest)
	}

	// Headers mirror body fields so intermediaries can route without parsing
	// the body; a mismatch means two components would disagree on what the
	// request is, so it must be rejected rather than reconciled.
	if rpcErr := validateModernHeaders(r, req, meta); rpcErr != nil {
		return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}, http.StatusBadRequest
	}

	if meta.ProtocolVersion != protocolVersionModern {
		return fail(errCodeUnsupportedProtocolVersion, "Unsupported protocol version",
			map[string]any{
				"supported": supportedProtocolVersions,
				"requested": meta.ProtocolVersion,
			}, http.StatusBadRequest)
	}

	switch req.Method {
	case "server/discover":
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: mcpDiscoverResult{
				ResultType:        "complete",
				SupportedVersions: supportedProtocolVersions,
				Capabilities:      map[string]any{"tools": map[string]any{}},
				Instructions:      mcpServerInstructions,
				TTLMs:             mcpCacheTTLMs,
				CacheScope:        mcpCacheScopePublic,
				Meta:              modernResultMeta(),
			},
		}, http.StatusOK

	case "tools/list":
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: mcpToolsListResult{
				ResultType: "complete",
				Tools:      m.toolDefinitions(),
				TTLMs:      mcpCacheTTLMs,
				CacheScope: mcpCacheScopePublic,
				Meta:       modernResultMeta(),
			},
		}, http.StatusOK

	case "tools/call":
		var params mcpToolCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			// A malformed request is a protocol error even in the modern era;
			// only input *validation* failures become isError tool results.
			return fail(errCodeInvalidParams, "invalid params: "+err.Error(), nil, http.StatusBadRequest)
		}
		result := m.callTool(params)
		result.ResultType = "complete"
		result.Meta = modernResultMeta()
		m.recordToolMetrics(params.Name, req.Params, result)
		return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result}, http.StatusOK

	default:
		// 404 (not 200) so a dual-era client can distinguish an unknown method
		// on a modern server from a legacy server that lacks the endpoint.
		return fail(errCodeMethodNotFound, "method not found: "+req.Method, nil, http.StatusNotFound)
	}
}

// ── Header validation ───────────────────────────────────────────────────

// validateModernHeaders enforces the Streamable HTTP requirement that the
// mirrored request headers match the request body. Returns nil when there is no
// HTTP request to validate (e.g. a direct in-process call).
func validateModernHeaders(r *http.Request, req jsonRPCRequest, meta mcpRequestMeta) *jsonRPCError {
	if r == nil {
		return nil
	}
	mismatch := func(msg string) *jsonRPCError {
		return &jsonRPCError{Code: errCodeHeaderMismatch, Message: "Header mismatch: " + msg}
	}

	version := r.Header.Get("MCP-Protocol-Version")
	if version == "" {
		return mismatch("missing required MCP-Protocol-Version header")
	}
	if version != meta.ProtocolVersion {
		return mismatch("MCP-Protocol-Version header value '" + version +
			"' does not match body value '" + meta.ProtocolVersion + "'")
	}

	method := r.Header.Get("Mcp-Method")
	if method == "" {
		return mismatch("missing required Mcp-Method header")
	}
	if method != req.Method {
		return mismatch("Mcp-Method header value '" + method +
			"' does not match body value '" + req.Method + "'")
	}

	// Mcp-Name mirrors params.name for tools/call. Gypsum exposes no resources
	// or prompts, so tools/call is the only method that carries it.
	if req.Method == "tools/call" {
		var params struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(req.Params, &params)

		raw := r.Header.Get("Mcp-Name")
		if raw == "" {
			return mismatch("missing required Mcp-Name header")
		}
		name, ok := decodeHeaderValue(raw)
		if !ok {
			return mismatch("Mcp-Name header value is not valid base64")
		}
		if name != params.Name {
			return mismatch("Mcp-Name header value '" + name +
				"' does not match body value '" + params.Name + "'")
		}
	}
	return nil
}

// decodeHeaderValue unwraps the "=?base64?<data>?=" sentinel that clients use
// for header values which cannot be sent as plain ASCII. Plain values are
// returned unchanged.
func decodeHeaderValue(v string) (string, bool) {
	const prefix, suffix = "=?base64?", "?="
	if !strings.HasPrefix(v, prefix) || !strings.HasSuffix(v, suffix) || len(v) < len(prefix)+len(suffix) {
		return v, true
	}
	decoded, err := base64.StdEncoding.DecodeString(v[len(prefix) : len(v)-len(suffix)])
	if err != nil {
		return "", false
	}
	return string(decoded), true
}

// ── Origin validation ───────────────────────────────────────────────────

// ParseMCPOrigins builds the Origin allowlist for the MCP endpoint from the
// GYPSUM_MCP_ALLOWED_ORIGINS value and the deployment's external URL. Loopback
// origins are always allowed and need not be listed. A single "*" entry
// disables Origin checking entirely.
func ParseMCPOrigins(raw, externalURL string) []string {
	var origins []string
	if o := originOf(externalURL); o != "" {
		origins = append(origins, o)
	}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if part == "*" {
			return []string{"*"}
		}
		if o := originOf(part); o != "" {
			origins = append(origins, o)
		}
	}
	return origins
}

// originOf reduces a URL to its scheme://host[:port] origin form.
func originOf(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// originAllowed reports whether an Origin header may talk to the MCP endpoint.
// An absent Origin is allowed: non-browser clients (Claude's remote connector,
// mcp-proxy, curl) do not send one, and the DNS-rebinding attack this guards
// against is browser-only.
func (m *MCPHandler) originAllowed(origin string) bool {
	if origin == "" {
		return true
	}
	for _, allowed := range m.allowedOrigins {
		if allowed == "*" || strings.EqualFold(allowed, origin) {
			return true
		}
	}
	return isLoopbackOrigin(origin)
}

func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}
