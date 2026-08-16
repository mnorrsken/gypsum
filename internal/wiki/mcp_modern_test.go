package wiki

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// modernParams wraps tool params with the _meta block every 2026-07-28 request
// must carry.
func modernParams(version string, extra map[string]any) map[string]any {
	params := map[string]any{}
	for k, v := range extra {
		params[k] = v
	}
	meta := map[string]any{
		metaKeyClientInfo:         map[string]any{"name": "test-client", "version": "1.0"},
		metaKeyClientCapabilities: map[string]any{},
	}
	if version != "" {
		meta[metaKeyProtocolVersion] = version
	}
	params["_meta"] = meta
	return params
}

// modernPost sends a modern request, deriving the mirrored headers from the
// body the way a conforming client does. Header overrides let tests break that
// mirroring on purpose.
func modernPost(t *testing.T, handler http.Handler, method string, params map[string]any, headers map[string]string) (int, jsonRPCResponse) {
	t.Helper()
	body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		body["params"] = params
	}
	data, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Method", method)
	if p, ok := params["_meta"].(map[string]any); ok {
		if v, ok := p[metaKeyProtocolVersion].(string); ok {
			req.Header.Set("MCP-Protocol-Version", v)
		}
	}
	if name, ok := params["name"].(string); ok {
		req.Header.Set("Mcp-Name", name)
	}
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
			continue
		}
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp jsonRPCResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("%s: decode response failed: %v (body=%s)", method, err, rec.Body.String())
	}
	return rec.Code, resp
}

// decodeResult re-marshals a decoded JSON-RPC result into a typed value.
func decodeResult(t *testing.T, resp jsonRPCResponse, out any) {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
}

// ── server/discover ─────────────────────────────────────────────────────

func TestMCPModernDiscover(t *testing.T) {
	handler, _ := newTestMCP(t)
	status, resp := modernPost(t, handler, "server/discover",
		modernParams(protocolVersionModern, nil), nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}

	var result mcpDiscoverResult
	decodeResult(t, resp, &result)

	if result.ResultType != "complete" {
		t.Errorf("resultType = %q, want %q", result.ResultType, "complete")
	}
	if len(result.SupportedVersions) == 0 || result.SupportedVersions[0] != protocolVersionModern {
		t.Errorf("supportedVersions = %v, want %s first", result.SupportedVersions, protocolVersionModern)
	}
	if _, ok := result.Capabilities["tools"]; !ok {
		t.Errorf("capabilities missing tools: %v", result.Capabilities)
	}
	if result.TTLMs <= 0 || result.CacheScope != mcpCacheScopePublic {
		t.Errorf("caching hints = ttlMs %d / scope %q, want positive ttl and %q",
			result.TTLMs, result.CacheScope, mcpCacheScopePublic)
	}
	if _, ok := result.Meta[metaKeyServerInfo]; !ok {
		t.Errorf("result _meta missing %s: %v", metaKeyServerInfo, result.Meta)
	}
}

// server/discover must work even when the client sends nothing but the method,
// so a dual-era client can use it as an era probe.
func TestMCPModernDiscoverWithoutMetaIsRejectedAsModern(t *testing.T) {
	handler, _ := newTestMCP(t)
	status, resp := modernPost(t, handler, "server/discover", map[string]any{}, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if resp.Error == nil || resp.Error.Code != errCodeInvalidParams {
		t.Fatalf("error = %+v, want code %d", resp.Error, errCodeInvalidParams)
	}
}

// ── tools/list and tools/call ───────────────────────────────────────────

func TestMCPModernToolsList(t *testing.T) {
	handler, _ := newTestMCP(t)
	status, resp := modernPost(t, handler, "tools/list",
		modernParams(protocolVersionModern, nil), nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}

	var result mcpToolsListResult
	decodeResult(t, resp, &result)

	if result.ResultType != "complete" {
		t.Errorf("resultType = %q, want %q", result.ResultType, "complete")
	}
	if result.TTLMs <= 0 || result.CacheScope != mcpCacheScopePublic {
		t.Errorf("caching hints = ttlMs %d / scope %q", result.TTLMs, result.CacheScope)
	}
	if len(result.Tools) == 0 {
		t.Fatal("no tools returned")
	}
	for _, tool := range result.Tools {
		if tool.Title == "" {
			t.Errorf("tool %s has no title", tool.Name)
		}
	}
}

// tools/list must be deterministic so clients can cache it against ttlMs.
func TestMCPModernToolsListIsDeterministic(t *testing.T) {
	handler, _ := newTestMCP(t)
	var first []string
	for i := 0; i < 3; i++ {
		_, resp := modernPost(t, handler, "tools/list",
			modernParams(protocolVersionModern, nil), nil)
		var result mcpToolsListResult
		decodeResult(t, resp, &result)

		var names []string
		for _, tool := range result.Tools {
			names = append(names, tool.Name)
		}
		if i == 0 {
			first = names
			continue
		}
		if len(names) != len(first) {
			t.Fatalf("tool count changed: %d then %d", len(first), len(names))
		}
		for j := range names {
			if names[j] != first[j] {
				t.Fatalf("tool order changed at %d: %s vs %s", j, first[j], names[j])
			}
		}
	}
}

func TestMCPModernToolsCall(t *testing.T) {
	handler, store := newTestMCP(t)
	if err := store.Save(KindPage, "Home", "# Home\n\nhello"); err != nil {
		t.Fatalf("save page: %v", err)
	}

	status, resp := modernPost(t, handler, "tools/call", modernParams(protocolVersionModern, map[string]any{
		"name":      "get_page",
		"arguments": map[string]any{"slug": "Home"},
	}), nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}

	var result mcpCallToolResult
	decodeResult(t, resp, &result)
	if result.ResultType != "complete" {
		t.Errorf("resultType = %q, want %q", result.ResultType, "complete")
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content[0].Text)
	}
	if _, ok := result.Meta[metaKeyServerInfo]; !ok {
		t.Errorf("result _meta missing %s", metaKeyServerInfo)
	}
}

// A tool name that is not header-safe must still validate once the Mcp-Name
// sentinel is decoded.
func TestMCPModernBase64ToolName(t *testing.T) {
	handler, _ := newTestMCP(t)
	const name = "get_page"
	encoded := "=?base64?" + base64.StdEncoding.EncodeToString([]byte(name)) + "?="

	status, resp := modernPost(t, handler, "tools/call", modernParams(protocolVersionModern, map[string]any{
		"name":      name,
		"arguments": map[string]any{"slug": "Missing"},
	}), map[string]string{"Mcp-Name": encoded})

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (resp=%+v)", status, resp.Error)
	}
}

// ── Validation errors ───────────────────────────────────────────────────

func TestMCPModernValidationErrors(t *testing.T) {
	callParams := func(version string) map[string]any {
		return modernParams(version, map[string]any{
			"name":      "get_page",
			"arguments": map[string]any{"slug": "Home"},
		})
	}

	tests := []struct {
		name       string
		method     string
		params     map[string]any
		headers    map[string]string
		wantStatus int
		wantCode   int
	}{
		{
			name:       "missing protocol version in _meta",
			method:     "tools/list",
			params:     modernParams("", nil),
			headers:    map[string]string{"MCP-Protocol-Version": protocolVersionModern},
			wantStatus: http.StatusBadRequest,
			wantCode:   errCodeInvalidParams,
		},
		{
			name:   "missing client capabilities in _meta",
			method: "tools/list",
			params: map[string]any{"_meta": map[string]any{
				metaKeyProtocolVersion: protocolVersionModern,
			}},
			headers:    map[string]string{"MCP-Protocol-Version": protocolVersionModern},
			wantStatus: http.StatusBadRequest,
			wantCode:   errCodeInvalidParams,
		},
		{
			name:       "missing protocol version header",
			method:     "tools/list",
			params:     modernParams(protocolVersionModern, nil),
			headers:    map[string]string{"MCP-Protocol-Version": ""},
			wantStatus: http.StatusBadRequest,
			wantCode:   errCodeHeaderMismatch,
		},
		{
			name:       "protocol version header disagrees with body",
			method:     "tools/list",
			params:     modernParams(protocolVersionModern, nil),
			headers:    map[string]string{"MCP-Protocol-Version": "2025-11-25"},
			wantStatus: http.StatusBadRequest,
			wantCode:   errCodeHeaderMismatch,
		},
		{
			name:       "missing method header",
			method:     "tools/list",
			params:     modernParams(protocolVersionModern, nil),
			headers:    map[string]string{"Mcp-Method": ""},
			wantStatus: http.StatusBadRequest,
			wantCode:   errCodeHeaderMismatch,
		},
		{
			name:       "method header disagrees with body",
			method:     "tools/list",
			params:     modernParams(protocolVersionModern, nil),
			headers:    map[string]string{"Mcp-Method": "tools/call"},
			wantStatus: http.StatusBadRequest,
			wantCode:   errCodeHeaderMismatch,
		},
		{
			name:       "missing name header on tools/call",
			method:     "tools/call",
			params:     callParams(protocolVersionModern),
			headers:    map[string]string{"Mcp-Name": ""},
			wantStatus: http.StatusBadRequest,
			wantCode:   errCodeHeaderMismatch,
		},
		{
			name:       "name header disagrees with body",
			method:     "tools/call",
			params:     callParams(protocolVersionModern),
			headers:    map[string]string{"Mcp-Name": "delete_page"},
			wantStatus: http.StatusBadRequest,
			wantCode:   errCodeHeaderMismatch,
		},
		{
			name:       "unknown modern method",
			method:     "resources/list",
			params:     modernParams(protocolVersionModern, nil),
			wantStatus: http.StatusNotFound,
			wantCode:   errCodeMethodNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, _ := newTestMCP(t)
			status, resp := modernPost(t, handler, tt.method, tt.params, tt.headers)
			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}
			if resp.Error == nil {
				t.Fatalf("expected a JSON-RPC error, got result %v", resp.Result)
			}
			if resp.Error.Code != tt.wantCode {
				t.Errorf("error code = %d (%s), want %d", resp.Error.Code, resp.Error.Message, tt.wantCode)
			}
		})
	}
}

// An unsupported version must come back as -32022 listing what the server does
// speak, so the client can retry or fall back to the legacy handshake.
func TestMCPModernUnsupportedProtocolVersion(t *testing.T) {
	handler, _ := newTestMCP(t)
	status, resp := modernPost(t, handler, "tools/list",
		modernParams("1900-01-01", nil), nil)

	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if resp.Error == nil || resp.Error.Code != errCodeUnsupportedProtocolVersion {
		t.Fatalf("error = %+v, want code %d", resp.Error, errCodeUnsupportedProtocolVersion)
	}

	data, ok := resp.Error.Data.(map[string]any)
	if !ok {
		t.Fatalf("error data = %T, want object", resp.Error.Data)
	}
	if data["requested"] != "1900-01-01" {
		t.Errorf("data.requested = %v, want 1900-01-01", data["requested"])
	}
	supported, ok := data["supported"].([]any)
	if !ok || len(supported) == 0 {
		t.Fatalf("data.supported = %v, want a non-empty list", data["supported"])
	}
	if supported[0] != protocolVersionModern {
		t.Errorf("data.supported[0] = %v, want %s", supported[0], protocolVersionModern)
	}
}

// ── Dual-era behavior ───────────────────────────────────────────────────

// A legacy client opening with initialize must keep the handshake semantics and
// must not see modern-only fields, which some legacy clients reject.
func TestMCPLegacyEraUnaffected(t *testing.T) {
	handler, _ := newTestMCP(t)

	resp := mcpCall(t, handler, 1, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "legacy", "version": "1.0"},
	})
	var init mcpInitializeResult
	decodeResult(t, resp, &init)
	if init.ProtocolVersion != "2025-06-18" {
		t.Errorf("protocolVersion = %q, want the requested 2025-06-18", init.ProtocolVersion)
	}

	resp = mcpCall(t, handler, 2, "tools/list", nil)
	raw, _ := json.Marshal(resp.Result)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	for _, modernOnly := range []string{"resultType", "ttlMs", "cacheScope", "_meta"} {
		if _, present := fields[modernOnly]; present {
			t.Errorf("legacy tools/list result leaked modern field %q", modernOnly)
		}
	}
}

// An unrecognized protocol version on initialize falls back rather than failing.
func TestMCPLegacyVersionFallback(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "initialize", map[string]any{
		"protocolVersion": "1999-01-01",
	})
	var init mcpInitializeResult
	decodeResult(t, resp, &init)
	if init.ProtocolVersion != protocolVersionLegacyFallback {
		t.Errorf("protocolVersion = %q, want %q", init.ProtocolVersion, protocolVersionLegacyFallback)
	}
}

// ── Origin validation ───────────────────────────────────────────────────

func TestMCPOriginValidation(t *testing.T) {
	tests := []struct {
		name       string
		allowed    []string
		origin     string
		wantStatus int
	}{
		{"no origin header is allowed", []string{"https://wiki.example.com"}, "", http.StatusOK},
		{"configured origin is allowed", []string{"https://wiki.example.com"}, "https://wiki.example.com", http.StatusOK},
		{"loopback is always allowed", []string{"https://wiki.example.com"}, "http://localhost:8080", http.StatusOK},
		{"foreign origin is rejected", []string{"https://wiki.example.com"}, "https://evil.example.com", http.StatusForbidden},
		{"wildcard disables checking", []string{"*"}, "https://evil.example.com", http.StatusOK},
		{"unconfigured rejects non-loopback", nil, "https://evil.example.com", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, _ := newTestMCP(t)
			handler.SetAllowedOrigins(tt.allowed)

			body, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": 1, "method": "tools/list",
			})
			req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			// A permitted cross-origin request must be echoed back, not "*".
			if tt.wantStatus == http.StatusOK && tt.origin != "" {
				if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tt.origin {
					t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tt.origin)
				}
			}
		})
	}
}

func TestParseMCPOrigins(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		externalURL string
		want        []string
	}{
		{"external url only", "", "https://wiki.example.com", []string{"https://wiki.example.com"}},
		{"strips path", "", "https://wiki.example.com/wiki", []string{"https://wiki.example.com"}},
		{"keeps port", "", "http://wiki.local:8080", []string{"http://wiki.local:8080"}},
		{"widened by env", "https://app.example.com", "https://wiki.example.com",
			[]string{"https://wiki.example.com", "https://app.example.com"}},
		{"wildcard wins", "https://a.example.com, *", "https://wiki.example.com", []string{"*"}},
		{"ignores blanks and junk", " , not-a-url ,", "https://wiki.example.com",
			[]string{"https://wiki.example.com"}},
		{"nothing configured", "", "", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseMCPOrigins(tt.raw, tt.externalURL)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// ── Tool schemas ────────────────────────────────────────────────────────

// Parameterless tools use the spec's recommended closed-object form so that
// stray arguments are rejected rather than silently ignored.
func TestMCPParameterlessToolSchema(t *testing.T) {
	handler, _ := newTestMCP(t)
	for _, tool := range handler.toolDefinitions() {
		schema, ok := tool.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("tool %s: input schema is %T, want an object", tool.Name, tool.InputSchema)
		}
		if _, hasProps := schema["properties"]; hasProps {
			continue
		}
		if schema["additionalProperties"] != false {
			t.Errorf("tool %s: parameterless schema should set additionalProperties:false, got %v",
				tool.Name, schema)
		}
	}
}
