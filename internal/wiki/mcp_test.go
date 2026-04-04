package wiki

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// mcpCall sends a JSON-RPC request to the MCP handler and returns the parsed response.
func mcpCall(t *testing.T, handler http.Handler, id int, method string, params any) jsonRPCResponse {
	t.Helper()
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		raw, _ := json.Marshal(params)
		body["params"] = json.RawMessage(raw)
	}
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("MCP %s: status=%d body=%s", method, rec.Code, rec.Body.String())
	}
	var resp jsonRPCResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("MCP %s: decode failed: %v", method, err)
	}
	return resp
}

// mcpNotify sends a JSON-RPC notification (no id) and returns the HTTP status.
func mcpNotify(t *testing.T, handler http.Handler, method string) int {
	t.Helper()
	body := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code
}

func toolResultText(t *testing.T, resp jsonRPCResponse) string {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	var result mcpCallToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("tool result has no content")
	}
	return result.Content[0].Text
}

func toolResultIsError(t *testing.T, resp jsonRPCResponse) string {
	t.Helper()
	raw, _ := json.Marshal(resp.Result)
	var result mcpCallToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected isError=true, got text: %s", result.Content[0].Text)
	}
	return result.Content[0].Text
}

func newTestMCP(t *testing.T) (*MCPHandler, *PageStore) {
	t.Helper()
	dir := t.TempDir()
	pagesDir := filepath.Join(dir, "pages")
	_ = os.MkdirAll(pagesDir, 0o755)
	store := NewPageStore(pagesDir)
	handler := NewMCPHandler(store, nil, AllMCPSections)
	return handler, store
}

// ── Protocol tests ──────────────────────────────────────────────────────

func TestMCPInitialize(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
	})
	if resp.Error != nil {
		t.Fatalf("initialize error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	var result mcpInitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.ProtocolVersion != "2025-03-26" {
		t.Fatalf("unexpected protocol version: %s", result.ProtocolVersion)
	}
	if result.ServerInfo.Name != "gypsum-wiki" {
		t.Fatalf("unexpected server name: %s", result.ServerInfo.Name)
	}
}

func TestMCPInitializeReturnsSessionID(t *testing.T) {
	handler, _ := newTestMCP(t)
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{},
	}
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	sid := rec.Header().Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatal("missing Mcp-Session-Id header")
	}
	if len(sid) != 32 { // 16 bytes hex-encoded
		t.Fatalf("unexpected session id length: %d", len(sid))
	}
}

func TestMCPNotificationReturns202(t *testing.T) {
	handler, _ := newTestMCP(t)
	code := mcpNotify(t, handler, "notifications/initialized")
	if code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", code)
	}
}

func TestMCPToolsList(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "tools/list", nil)
	if resp.Error != nil {
		t.Fatalf("tools/list error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	var result mcpToolsListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Tools) != 23 {
		t.Fatalf("expected 23 tools, got %d", len(result.Tools))
	}
	// Verify all tools have name, description, and schema
	for _, tool := range result.Tools {
		if tool.Name == "" {
			t.Fatal("tool has empty name")
		}
		if tool.Description == "" {
			t.Fatalf("tool %s has empty description", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Fatalf("tool %s has nil schema", tool.Name)
		}
	}
}

func TestMCPToolsSections(t *testing.T) {
	dir := t.TempDir()
	pagesDir := filepath.Join(dir, "pages")
	_ = os.MkdirAll(pagesDir, 0o755)
	store := NewPageStore(pagesDir)

	// Enable only read section.
	handler := NewMCPHandler(store, nil, map[MCPSection]bool{MCPSectionRead: true})
	resp := mcpCall(t, handler, 1, "tools/list", nil)
	raw, _ := json.Marshal(resp.Result)
	var result mcpToolsListResult
	_ = json.Unmarshal(raw, &result)

	for _, tool := range result.Tools {
		if sec, ok := toolSectionMap[tool.Name]; ok && sec != MCPSectionRead {
			t.Fatalf("tool %s (section %s) should not be listed when only read is enabled", tool.Name, sec)
		}
	}

	// Verify a disabled tool returns an error at call time.
	callResp := mcpCall(t, handler, 2, "tools/call", map[string]any{
		"name":      "create_page",
		"arguments": map[string]any{"title": "X", "content": "Y"},
	})
	raw, _ = json.Marshal(callResp.Result)
	var callResult mcpCallToolResult
	_ = json.Unmarshal(raw, &callResult)
	if !callResult.IsError {
		t.Fatal("expected error calling disabled tool create_page")
	}
}

func TestMCPParseSections(t *testing.T) {
	s := ParseMCPSections("")
	if len(s) != 4 {
		t.Fatalf("empty input should return all 4 sections, got %d", len(s))
	}
	s = ParseMCPSections("read,skills")
	if !s[MCPSectionRead] || !s[MCPSectionSkills] {
		t.Fatal("expected read and skills enabled")
	}
	if s[MCPSectionEdit] || s[MCPSectionDelete] {
		t.Fatal("expected edit and delete disabled")
	}
}

func TestMCPPing(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "ping", nil)
	if resp.Error != nil {
		t.Fatalf("ping error: %s", resp.Error.Message)
	}
}

func TestMCPUnknownMethod(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "bogus/method", nil)
	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Fatalf("expected code -32601, got %d", resp.Error.Code)
	}
}

func TestMCPDeleteSession(t *testing.T) {
	handler, _ := newTestMCP(t)
	req := httptest.NewRequest(http.MethodDelete, "/mcp", nil)
	req.Header.Set("Mcp-Session-Id", "some-session-id")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE /mcp: status=%d", rec.Code)
	}
}

// ── Tool tests ──────────────────────────────────────────────────────────

func TestMCPListPagesEmpty(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "list_pages", "arguments": map[string]any{},
	})
	text := toolResultText(t, resp)
	// Empty store returns empty array
	if text != "[]" {
		t.Fatalf("expected empty array, got: %s", text)
	}
}

func TestMCPCreateAndGetPage(t *testing.T) {
	handler, _ := newTestMCP(t)

	// Create
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "create_page",
		"arguments": map[string]any{
			"title":   "Test Page",
			"content": "# Hello\n\nWorld",
		},
	})
	text := toolResultText(t, resp)
	if text != "Created page 'Test Page' (slug: Test_Page)" {
		t.Fatalf("unexpected create result: %s", text)
	}

	// Get
	resp = mcpCall(t, handler, 2, "tools/call", map[string]any{
		"name":      "get_page",
		"arguments": map[string]any{"slug": "Test_Page"},
	})
	text = toolResultText(t, resp)
	if text != "# Hello\n\nWorld" {
		t.Fatalf("unexpected content: %q", text)
	}
}

func TestMCPCreatePageDuplicate(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage,"Existing", "content")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "create_page",
		"arguments": map[string]any{
			"title":   "Existing",
			"content": "new content",
		},
	})
	errText := toolResultIsError(t, resp)
	if errText != "page already exists: Existing" {
		t.Fatalf("unexpected error: %s", errText)
	}
}

func TestMCPEditPage(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage,"MyPage", "old content")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "edit_page",
		"arguments": map[string]any{
			"slug":    "MyPage",
			"content": "new content",
		},
	})
	text := toolResultText(t, resp)
	if text != "Updated page 'MyPage'" {
		t.Fatalf("unexpected: %s", text)
	}

	// Verify on disk
	page, _ := store.Load(KindPage,"MyPage")
	if page.Content != "new content" {
		t.Fatalf("content not updated: %q", page.Content)
	}
}

func TestMCPEditPageNotFound(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "edit_page",
		"arguments": map[string]any{
			"slug":    "NoSuchPage",
			"content": "x",
		},
	})
	errText := toolResultIsError(t, resp)
	if errText != "page not found: NoSuchPage" {
		t.Fatalf("unexpected error: %s", errText)
	}
}

func TestMCPDeletePage(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage,"ToDelete", "bye")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "delete_page",
		"arguments": map[string]any{"slug": "ToDelete"},
	})
	text := toolResultText(t, resp)
	if text != "Deleted page 'ToDelete'" {
		t.Fatalf("unexpected: %s", text)
	}

	// Verify gone
	_, err := store.Load(KindPage,"ToDelete")
	if err != ErrPageNotFound {
		t.Fatalf("expected ErrPageNotFound, got %v", err)
	}
}

func TestMCPDeletePageNotFound(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "delete_page",
		"arguments": map[string]any{"slug": "Ghost"},
	})
	toolResultIsError(t, resp)
}

func TestMCPSearchPages(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage,"Alpha", "contains keyword gypsum here")
	_ = store.Save(KindPage,"Beta", "nothing relevant")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "search_pages",
		"arguments": map[string]any{"query": "gypsum"},
	})
	text := toolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("Alpha")) {
		t.Fatalf("expected Alpha in results: %s", text)
	}
	if bytes.Contains([]byte(text), []byte("Beta")) {
		t.Fatalf("Beta should not be in results: %s", text)
	}
}

func TestMCPSearchPagesArrayQuery(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage, "Alpha", "contains keyword gypsum here")

	// Single-element array should work like a plain string
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "search_pages",
		"arguments": map[string]any{"query": []string{"gypsum"}},
	})
	text := toolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("Alpha")) {
		t.Fatalf("expected Alpha in results: %s", text)
	}
	// Single query should NOT show per-query headers
	if bytes.Contains([]byte(text), []byte("Results for:")) {
		t.Fatalf("single query should not have per-query headers: %s", text)
	}
}

func TestMCPSearchPagesMultiQuery(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage, "Alpha", "contains keyword gypsum here")
	_ = store.Save(KindPage, "Beta", "talks about golang programming")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "search_pages",
		"arguments": map[string]any{"query": []string{"gypsum", "golang"}},
	})
	text := toolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("Alpha")) {
		t.Fatalf("expected Alpha in results: %s", text)
	}
	if !bytes.Contains([]byte(text), []byte("Beta")) {
		t.Fatalf("expected Beta in results: %s", text)
	}
	// Multiple queries should show per-query headers
	if !bytes.Contains([]byte(text), []byte("Results for:")) {
		t.Fatalf("expected per-query headers for multi-query: %s", text)
	}
}

func TestMCPSearchPagesNoResults(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "search_pages",
		"arguments": map[string]any{"query": "nonexistent"},
	})
	text := toolResultText(t, resp)
	if text != "No results found for: nonexistent" {
		t.Fatalf("unexpected: %s", text)
	}
}

func TestMCPSearchPagesMultiQueryNoResults(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "search_pages",
		"arguments": map[string]any{"query": []string{"zzz_nope", "yyy_nada"}},
	})
	text := toolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("zzz_nope")) || !bytes.Contains([]byte(text), []byte("yyy_nada")) {
		t.Fatalf("expected both queries in no-results message: %s", text)
	}
}

func TestMCPGetPageNotFound(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "get_page",
		"arguments": map[string]any{"slug": "NoSuchPage"},
	})
	toolResultIsError(t, resp)
}

func TestMCPListImages(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "list_images", "arguments": map[string]any{},
	})
	text := toolResultText(t, resp)
	if text != "No images uploaded." {
		t.Fatalf("expected no images message, got: %s", text)
	}
}

func TestMCPDeleteImageNotFound(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "delete_image",
		"arguments": map[string]any{"filename": "nonexistent.png"},
	})
	toolResultIsError(t, resp)
}

func TestMCPDeleteImagePathTraversal(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "delete_image",
		"arguments": map[string]any{"filename": "../../../etc/passwd"},
	})
	errText := toolResultIsError(t, resp)
	if errText != "invalid filename" {
		t.Fatalf("expected invalid filename error, got: %s", errText)
	}
}

func TestMCPGetRecentPages(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage,"Old", "old page")
	_ = store.Save(KindPage,"New", "new page")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "get_recent_pages",
		"arguments": map[string]any{"count": 1},
	})
	text := toolResultText(t, resp)
	// Should contain the most recent page
	if !bytes.Contains([]byte(text), []byte("New")) {
		t.Fatalf("expected New in recent pages: %s", text)
	}
}

func TestMCPGetFavoritesEmpty(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "get_favorites", "arguments": map[string]any{},
	})
	text := toolResultText(t, resp)
	if text != "No favorites set." {
		t.Fatalf("unexpected: %s", text)
	}
}

func TestMCPGetFavorites(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage,"_favorites", "[[Home]]\n[[Notes]]")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "get_favorites", "arguments": map[string]any{},
	})
	text := toolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("Home")) {
		t.Fatalf("expected Home in favorites: %s", text)
	}
}

func TestMCPPageHistoryNoGit(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "page_history",
		"arguments": map[string]any{"slug": "Home"},
	})
	text := toolResultText(t, resp)
	if text != "No history available for: Home" {
		t.Fatalf("unexpected: %s", text)
	}
}

func TestMCPGetPageRevisionNoGit(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "get_page_revision",
		"arguments": map[string]any{"slug": "Home", "hash": "abc123"},
	})
	toolResultIsError(t, resp)
}

func TestMCPUnknownTool(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "nonexistent_tool",
		"arguments": map[string]any{},
	})
	errText := toolResultIsError(t, resp)
	if errText != "unknown tool: nonexistent_tool" {
		t.Fatalf("unexpected error: %s", errText)
	}
}

func TestMCPMissingRequiredArgs(t *testing.T) {
	handler, _ := newTestMCP(t)

	tests := []struct {
		tool string
		args map[string]any
	}{
		{"get_page", map[string]any{}},
		{"create_page", map[string]any{"content": "x"}},
		{"create_page", map[string]any{"title": "x"}},
		{"edit_page", map[string]any{"slug": "x"}},
		{"edit_page", map[string]any{"content": "x"}},
		{"delete_page", map[string]any{}},
		{"search_pages", map[string]any{}},
		{"delete_image", map[string]any{}},
		{"page_history", map[string]any{}},
		{"get_page_revision", map[string]any{"slug": "x"}},
		{"get_page_revision", map[string]any{"hash": "x"}},
		{"page_links", map[string]any{}},
		{"what_links_here", map[string]any{}},
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
				"name":      tt.tool,
				"arguments": tt.args,
			})
			toolResultIsError(t, resp)
		})
	}
}

func TestMCPPageLinks(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage,"Home", "See [[About]] and [[Contact]]")
	_ = store.Save(KindPage,"About", "Back to [[Home]]")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "page_links", "arguments": map[string]any{"slug": "Home"},
	})
	text := toolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("About")) || !bytes.Contains([]byte(text), []byte("Contact")) {
		t.Fatalf("expected About and Contact in links: %s", text)
	}
}

func TestMCPPageLinksNoLinks(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage,"Lonely", "No links here.")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "page_links", "arguments": map[string]any{"slug": "Lonely"},
	})
	text := toolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("no outgoing")) {
		t.Fatalf("expected no outgoing message: %s", text)
	}
}

func TestMCPWhatLinksHere(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage,"Home", "See [[About]]")
	_ = store.Save(KindPage,"Other", "Also see [[About]]")
	_ = store.Save(KindPage,"About", "About page")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "what_links_here", "arguments": map[string]any{"slug": "About"},
	})
	text := toolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("Home")) || !bytes.Contains([]byte(text), []byte("Other")) {
		t.Fatalf("expected Home and Other in backlinks: %s", text)
	}
}

func TestMCPWhatLinksHereOrphaned(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage,"Orphan", "Nobody links here")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "what_links_here", "arguments": map[string]any{"slug": "Orphan"},
	})
	text := toolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("orphaned")) {
		t.Fatalf("expected orphaned message: %s", text)
	}
}

func TestMCPLinkGraph(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage,"Home", "See [[About]]")
	_ = store.Save(KindPage,"About", "Back to [[Home]]")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "link_graph", "arguments": map[string]any{},
	})
	text := toolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("Home")) || !bytes.Contains([]byte(text), []byte("About")) {
		t.Fatalf("expected graph data: %s", text)
	}
}

func TestMCPFullPageLifecycle(t *testing.T) {
	handler, _ := newTestMCP(t)

	// Create
	mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "create_page",
		"arguments": map[string]any{"title": "Lifecycle", "content": "v1"},
	})

	// List — should appear
	resp := mcpCall(t, handler, 2, "tools/call", map[string]any{
		"name": "list_pages", "arguments": map[string]any{},
	})
	text := toolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("Lifecycle")) {
		t.Fatalf("expected Lifecycle in list: %s", text)
	}

	// Edit
	mcpCall(t, handler, 3, "tools/call", map[string]any{
		"name":      "edit_page",
		"arguments": map[string]any{"slug": "Lifecycle", "content": "v2"},
	})

	// Read — should be v2
	resp = mcpCall(t, handler, 4, "tools/call", map[string]any{
		"name":      "get_page",
		"arguments": map[string]any{"slug": "Lifecycle"},
	})
	if toolResultText(t, resp) != "v2" {
		t.Fatalf("expected v2")
	}

	// Search — should find it
	resp = mcpCall(t, handler, 5, "tools/call", map[string]any{
		"name":      "search_pages",
		"arguments": map[string]any{"query": "v2"},
	})
	text = toolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("Lifecycle")) {
		t.Fatalf("expected Lifecycle in search: %s", text)
	}

	// Delete
	mcpCall(t, handler, 6, "tools/call", map[string]any{
		"name":      "delete_page",
		"arguments": map[string]any{"slug": "Lifecycle"},
	})

	// List — should be gone
	resp = mcpCall(t, handler, 7, "tools/call", map[string]any{
		"name": "list_pages", "arguments": map[string]any{},
	})
	text = toolResultText(t, resp)
	if text != "[]" {
		t.Fatalf("expected empty list after delete, got: %s", text)
	}
}

// ── create_page with query tests ────────────────────────────────────────

func TestMCPCreatePageQueryFindsMatch(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage, "Existing_Topic", "# Existing Topic\n\nContent about deployment")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "create_page",
		"arguments": map[string]any{
			"title":   "Deployment Guide",
			"content": "# Deployment Guide\n\nNew content",
			"query":   []string{"deployment"},
		},
	})
	text := toolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("Existing pages found")) {
		t.Fatalf("expected duplicate-check message: %s", text)
	}
	if !bytes.Contains([]byte(text), []byte("Existing_Topic")) {
		t.Fatalf("expected existing page in results: %s", text)
	}
	// Page should NOT have been created
	if _, err := store.Load(KindPage, "Deployment_Guide"); err == nil {
		t.Fatal("page should not have been created when query found matches")
	}
}

func TestMCPCreatePageQueryNoMatch(t *testing.T) {
	handler, store := newTestMCP(t)

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "create_page",
		"arguments": map[string]any{
			"title":   "Brand New Topic",
			"content": "# Brand New Topic\n\nFresh content",
			"query":   []string{"zzz_nonexistent_term"},
		},
	})
	text := toolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("Created page")) {
		t.Fatalf("expected page creation: %s", text)
	}
	if _, err := store.Load(KindPage, "Brand_New_Topic"); err != nil {
		t.Fatalf("page should have been created: %v", err)
	}
}

func TestMCPCreatePageWithoutQueryForceCreates(t *testing.T) {
	handler, store := newTestMCP(t)
	// Even with similar content already existing, no query = force create
	_ = store.Save(KindPage, "Similar", "# Similar\n\nOverlapping content")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "create_page",
		"arguments": map[string]any{
			"title":   "New Page",
			"content": "# New Page\n\nContent",
		},
	})
	text := toolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("Created page")) {
		t.Fatalf("expected page creation: %s", text)
	}
	if _, err := store.Load(KindPage, "New_Page"); err != nil {
		t.Fatalf("page should have been created: %v", err)
	}
}

func TestMCPCreatePageQueryMultipleQueries(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage, "Networking", "# Networking\n\nAll about network config")

	// First query misses, second query hits — should still block creation
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "create_page",
		"arguments": map[string]any{
			"title":   "Network Setup",
			"content": "# Network Setup\n\nGuide",
			"query":   []string{"zzz_nope", "network"},
		},
	})
	text := toolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("Existing pages found")) {
		t.Fatalf("expected duplicate-check message: %s", text)
	}
	if _, err := store.Load(KindPage, "Network_Setup"); err == nil {
		t.Fatal("page should not have been created when one query matched")
	}
}

// ── create_skill with query tests ───────────────────────────────────────

func TestMCPCreateSkillQueryFindsMatch(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindSkill, "Go_Testing", "# Go Testing\n\nHow to test Go code\n\nTags: go, testing")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "create_skill",
		"arguments": map[string]any{
			"title":   "Golang Test Patterns",
			"content": "# Golang Test Patterns\n\nNew testing guide",
			"query":   []string{"go testing"},
		},
	})
	text := toolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("Existing skills found")) {
		t.Fatalf("expected duplicate-check message: %s", text)
	}
	if _, err := store.Load(KindSkill, "Golang_Test_Patterns"); err == nil {
		t.Fatal("skill should not have been created when query found matches")
	}
}

func TestMCPCreateSkillQueryNoMatch(t *testing.T) {
	handler, store := newTestMCP(t)

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "create_skill",
		"arguments": map[string]any{
			"title":   "Unique Skill",
			"content": "# Unique Skill\n\nContent",
			"query":   []string{"zzz_nonexistent"},
		},
	})
	text := toolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("Created skill")) {
		t.Fatalf("expected skill creation: %s", text)
	}
	if _, err := store.Load(KindSkill, "Unique_Skill"); err != nil {
		t.Fatalf("skill should have been created: %v", err)
	}
}

func TestMCPCreateSkillWithoutQueryForceCreates(t *testing.T) {
	handler, store := newTestMCP(t)

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "create_skill",
		"arguments": map[string]any{
			"title":   "Direct Skill",
			"content": "# Direct Skill\n\nForce created",
		},
	})
	text := toolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("Created skill")) {
		t.Fatalf("expected skill creation: %s", text)
	}
	if _, err := store.Load(KindSkill, "Direct_Skill"); err != nil {
		t.Fatalf("skill should have been created: %v", err)
	}
}

// ── search_skills multi-query tests ─────────────────────────────────────

func TestMCPSearchSkillsMultiQuery(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindSkill, "Go_Testing", "# Go Testing\n\nTags: go, testing")
	_ = store.Save(KindSkill, "Deploy_K8s", "# Deploy K8s\n\nTags: deploy, kubernetes")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "search_skills",
		"arguments": map[string]any{"query": []string{"go testing", "deploy"}},
	})
	text := toolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("Go_Testing")) {
		t.Fatalf("expected Go_Testing in results: %s", text)
	}
	if !bytes.Contains([]byte(text), []byte("Deploy_K8s")) {
		t.Fatalf("expected Deploy_K8s in results: %s", text)
	}
	if !bytes.Contains([]byte(text), []byte("Results for:")) {
		t.Fatalf("expected per-query headers: %s", text)
	}
}

func TestMCPSearchSkillsNoResults(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "search_skills",
		"arguments": map[string]any{"query": "zzz_nope"},
	})
	text := toolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("No skills found for:")) {
		t.Fatalf("unexpected: %s", text)
	}
}
