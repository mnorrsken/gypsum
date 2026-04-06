package wiki

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// ── Skill CRUD tool tests ──────────────────────────────────────────────

func TestMCPListSkillsEmpty(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "list_skills", "arguments": map[string]any{},
	})
	text := toolResultText(t, resp)
	if text != "No skills created yet." {
		t.Fatalf("expected no skills message, got: %s", text)
	}
}

func TestMCPCreateAndGetSkill(t *testing.T) {
	handler, _ := newTestMCP(t)

	// Create
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "create_skill",
		"arguments": map[string]any{
			"title":   "Go Testing",
			"content": "# Go Testing\n\nTags: go, testing",
		},
	})
	text := toolResultText(t, resp)
	if !strings.Contains(text, "Created skill") {
		t.Fatalf("unexpected create result: %s", text)
	}

	// Get
	resp = mcpCall(t, handler, 2, "tools/call", map[string]any{
		"name":      "get_skill",
		"arguments": map[string]any{"slug": "Go_Testing"},
	})
	text = toolResultText(t, resp)
	if !strings.Contains(text, "Go Testing") {
		t.Fatalf("unexpected content: %s", text)
	}
}

func TestMCPCreateSkillDuplicate(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindSkill, "Existing", "content")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "create_skill",
		"arguments": map[string]any{
			"title":   "Existing",
			"content": "new content",
		},
	})
	errText := toolResultIsError(t, resp)
	if !strings.Contains(errText, "already exists") {
		t.Fatalf("unexpected error: %s", errText)
	}
}

func TestMCPEditSkill(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindSkill, "MySkill", "old content")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "edit_skill",
		"arguments": map[string]any{
			"slug":    "MySkill",
			"content": "new content",
		},
	})
	text := toolResultText(t, resp)
	if !strings.Contains(text, "Updated skill") {
		t.Fatalf("unexpected: %s", text)
	}

	skill, _ := store.Load(KindSkill, "MySkill")
	if skill.Content != "new content" {
		t.Fatalf("content not updated: %q", skill.Content)
	}
}

func TestMCPEditSkillNotFound(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "edit_skill",
		"arguments": map[string]any{
			"slug":    "NoSuchSkill",
			"content": "x",
		},
	})
	errText := toolResultIsError(t, resp)
	if !strings.Contains(errText, "not found") {
		t.Fatalf("unexpected error: %s", errText)
	}
}

func TestMCPDeleteSkill(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindSkill, "ToDelete", "bye")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "delete_skill",
		"arguments": map[string]any{"slug": "ToDelete"},
	})
	text := toolResultText(t, resp)
	if !strings.Contains(text, "Deleted skill") {
		t.Fatalf("unexpected: %s", text)
	}

	_, err := store.Load(KindSkill, "ToDelete")
	if err != ErrPageNotFound {
		t.Fatalf("expected ErrPageNotFound, got %v", err)
	}
}

func TestMCPDeleteSkillNotFound(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "delete_skill",
		"arguments": map[string]any{"slug": "Ghost"},
	})
	toolResultIsError(t, resp)
}

func TestMCPGetSkillNotFound(t *testing.T) {
	handler, _ := newTestMCP(t)
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "get_skill",
		"arguments": map[string]any{"slug": "NoSuchSkill"},
	})
	toolResultIsError(t, resp)
}

func TestMCPListSkillsWithEntries(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindSkill, "Alpha_Skill", "# Alpha\n\nTags: alpha")
	_ = store.Save(KindSkill, "Beta_Skill", "# Beta\n\nTags: beta")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "list_skills", "arguments": map[string]any{},
	})
	text := toolResultText(t, resp)
	if !strings.Contains(text, "Alpha_Skill") || !strings.Contains(text, "Beta_Skill") {
		t.Fatalf("expected both skills in list: %s", text)
	}
}

func TestMCPSkillMissingRequiredArgs(t *testing.T) {
	handler, _ := newTestMCP(t)

	tests := []struct {
		tool string
		args map[string]any
	}{
		{"get_skill", map[string]any{}},
		{"create_skill", map[string]any{"content": "x"}},
		{"create_skill", map[string]any{"title": "x"}},
		{"edit_skill", map[string]any{"slug": "x"}},
		{"edit_skill", map[string]any{"content": "x"}},
		{"delete_skill", map[string]any{}},
		{"search_skills", map[string]any{}},
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

func TestMCPFullSkillLifecycle(t *testing.T) {
	handler, _ := newTestMCP(t)

	// Create
	mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "create_skill",
		"arguments": map[string]any{"title": "Lifecycle Skill", "content": "# Lifecycle\n\nTags: test\n\nv1"},
	})

	// List — should appear
	resp := mcpCall(t, handler, 2, "tools/call", map[string]any{
		"name": "list_skills", "arguments": map[string]any{},
	})
	text := toolResultText(t, resp)
	if !strings.Contains(text, "Lifecycle_Skill") {
		t.Fatalf("expected Lifecycle_Skill in list: %s", text)
	}

	// Edit
	mcpCall(t, handler, 3, "tools/call", map[string]any{
		"name":      "edit_skill",
		"arguments": map[string]any{"slug": "Lifecycle_Skill", "content": "v2"},
	})

	// Read — should be v2
	resp = mcpCall(t, handler, 4, "tools/call", map[string]any{
		"name":      "get_skill",
		"arguments": map[string]any{"slug": "Lifecycle_Skill"},
	})
	if toolResultText(t, resp) != "v2" {
		t.Fatal("expected v2")
	}

	// Search — should find it
	resp = mcpCall(t, handler, 5, "tools/call", map[string]any{
		"name":      "search_skills",
		"arguments": map[string]any{"query": "lifecycle"},
	})
	text = toolResultText(t, resp)
	// Single match returns full content, not a summary.
	if text != "v2" {
		t.Fatalf("expected full skill content in single-match search: %s", text)
	}

	// Delete
	mcpCall(t, handler, 6, "tools/call", map[string]any{
		"name":      "delete_skill",
		"arguments": map[string]any{"slug": "Lifecycle_Skill"},
	})

	// List — should be empty
	resp = mcpCall(t, handler, 7, "tools/call", map[string]any{
		"name": "list_skills", "arguments": map[string]any{},
	})
	text = toolResultText(t, resp)
	if text != "No skills created yet." {
		t.Fatalf("expected empty list after delete, got: %s", text)
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

// ── Secure field tests ──────────────────────────────────────────────────
//
// The MCP layer must never decrypt or expose plaintext from {{secure_aes:...}}
// blocks. It passes ciphertext through as-is so that:
//   - Secrets are never exposed unencrypted over the wire.
//   - Pages with encrypted fields can be read and rewritten (round-tripped)
//     without corrupting the ciphertext.

// newTestMCPWithCrypto creates a handler wired to a store that has a crypto
// instance so we can pre-populate pages with real encrypted content.
func newTestMCPWithCrypto(t *testing.T) (*MCPHandler, *PageStore, *ServerCrypto) {
	t.Helper()
	dir := t.TempDir()
	pagesDir := filepath.Join(dir, "pages")
	_ = os.MkdirAll(pagesDir, 0o755)
	store := NewPageStore(pagesDir)
	crypto := NewServerCrypto("test-passphrase")
	handler := NewMCPHandler(store, nil, AllMCPSections)
	return handler, store, crypto
}

// encryptedPage returns markdown where the given plaintext secret has been
// encrypted using crypto and embedded as a {{secure_aes:...}} macro.
func encryptedPage(t *testing.T, crypto *ServerCrypto, plaintext string) string {
	t.Helper()
	enc, err := crypto.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	return "# Secret Page\n\nSome text. {{secure_aes:" + enc + "}} more text."
}

// TestMCPGetPageSecureFieldNotDecrypted verifies that get_page returns the raw
// {{secure_aes:...}} ciphertext and never the decrypted plaintext.
func TestMCPGetPageSecureFieldNotDecrypted(t *testing.T) {
	handler, store, crypto := newTestMCPWithCrypto(t)
	secret := "my-super-secret-password"
	content := encryptedPage(t, crypto, secret)
	_ = store.Save(KindPage, "Secret_Page", content)

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "get_page",
		"arguments": map[string]any{"slug": "Secret_Page"},
	})
	text := toolResultText(t, resp)

	if strings.Contains(text, secret) {
		t.Fatalf("get_page returned decrypted plaintext — secret must never be exposed: %q", text)
	}
	if !strings.Contains(text, "{{secure_aes:") {
		t.Fatalf("get_page should return raw ciphertext macro, got: %q", text)
	}
}

// TestMCPEditPageWithSecureFieldRoundTrips verifies that a page with encrypted
// fields can be read via MCP, then written back unchanged (round-trip), and
// that the ciphertext is preserved correctly on disk.
func TestMCPEditPageWithSecureFieldRoundTrips(t *testing.T) {
	handler, store, crypto := newTestMCPWithCrypto(t)
	secret := "round-trip-secret"
	original := encryptedPage(t, crypto, secret)
	_ = store.Save(KindPage, "RT_Page", original)

	// Read back via MCP
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "get_page",
		"arguments": map[string]any{"slug": "RT_Page"},
	})
	retrieved := toolResultText(t, resp)

	// Write it back as-is (simulates an LLM reading and rewriting the page)
	resp = mcpCall(t, handler, 2, "tools/call", map[string]any{
		"name":      "edit_page",
		"arguments": map[string]any{"slug": "RT_Page", "content": retrieved},
	})
	toolResultText(t, resp) // must not error

	// Verify the on-disk content still decrypts correctly
	saved, err := store.Load(KindPage, "RT_Page")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	decrypted := crypto.DecryptForEdit(saved.Content)
	if !strings.Contains(decrypted, secret) {
		t.Fatalf("secret lost after round-trip: decrypted=%q", decrypted)
	}
	if strings.Contains(saved.Content, secret) {
		t.Fatalf("plaintext found in stored content — must remain encrypted: %q", saved.Content)
	}
}

// TestMCPSearchPagesSecureFieldNotDecrypted verifies that search snippets never
// contain decrypted plaintext from encrypted fields.
func TestMCPSearchPagesSecureFieldNotDecrypted(t *testing.T) {
	handler, store, crypto := newTestMCPWithCrypto(t)
	secret := "searchable-secret-xyz"
	content := encryptedPage(t, crypto, secret)
	_ = store.Save(KindPage, "Secret_Page", content)

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "search_pages",
		"arguments": map[string]any{"query": "Secret Page"},
	})
	text := toolResultText(t, resp)

	if strings.Contains(text, secret) {
		t.Fatalf("search result contains decrypted plaintext: %q", text)
	}
}

// TestMCPGetPageRevisionSecureFieldNotDecrypted verifies that get_page_revision
// also never exposes decrypted plaintext.
func TestMCPGetPageRevisionSecureFieldNotDecrypted(t *testing.T) {
	handler, store, crypto := newTestMCPWithCrypto(t)
	secret := "revision-secret"
	content := encryptedPage(t, crypto, secret)
	_ = store.Save(KindPage, "Rev_Page", content)

	// No git in test env, so the call errors — that's fine. The important thing
	// is that if content were returned it would not be decrypted. We verify the
	// store holds ciphertext, not plaintext.
	stored, _ := store.Load(KindPage, "Rev_Page")
	if strings.Contains(stored.Content, secret) {
		t.Fatalf("store contains plaintext — must be encrypted at rest")
	}
	if !strings.Contains(stored.Content, "{{secure_aes:") {
		t.Fatalf("store does not contain encrypted macro")
	}

	// The get_page_revision call will fail (no git), but must not return plaintext.
	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "get_page_revision",
		"arguments": map[string]any{"slug": "Rev_Page", "hash": "abc123"},
	})
	raw, _ := strings.CutPrefix(toolResultIsError(t, resp), "")
	if strings.Contains(raw, secret) {
		t.Fatalf("error message contains plaintext secret: %q", raw)
	}
}

// TestMCPCreatePageWithSecureAesMacroStoresAsCiphertext verifies that if an LLM
// writes {{secure_aes:...}} content (e.g. a round-trip), it is stored verbatim
// and can still be decrypted by the server.
func TestMCPCreatePageWithSecureAesMacroStoresAsCiphertext(t *testing.T) {
	handler, store, crypto := newTestMCPWithCrypto(t)
	secret := "create-with-aes"
	enc, _ := crypto.Encrypt(secret)
	content := "# Encrypted\n\nValue: {{secure_aes:" + enc + "}}"

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "create_page",
		"arguments": map[string]any{"title": "Encrypted", "content": content},
	})
	toolResultText(t, resp) // must succeed

	saved, _ := store.Load(KindPage, "Encrypted")
	if strings.Contains(saved.Content, secret) {
		t.Fatalf("plaintext stored — must remain as ciphertext")
	}
	decrypted := crypto.DecryptForEdit(saved.Content)
	if !strings.Contains(decrypted, secret) {
		t.Fatalf("ciphertext cannot be decrypted after create: %q", decrypted)
	}
}

// TestMCPExternalHandlerBehavesLikeInternal verifies that the external
// (OAuth-protected) handler exposes the same ciphertext behaviour as the
// internal handler — no redaction, no extra restrictions.
func TestMCPExternalHandlerBehavesLikeInternal(t *testing.T) {
	dir := t.TempDir()
	pagesDir := filepath.Join(dir, "pages")
	_ = os.MkdirAll(pagesDir, 0o755)
	store := NewPageStore(pagesDir)
	crypto := NewServerCrypto("test-passphrase")

	secret := "external-handler-secret"
	enc, _ := crypto.Encrypt(secret)
	content := "# Page\n\nSecret: {{secure_aes:" + enc + "}}"
	_ = store.Save(KindPage, "Page", content)

	// External handler (no oauth in test — we bypass auth by calling HandleRPC directly)
	external := NewMCPHandlerExternal(store, nil, nil, AllMCPSections)

	resp := mcpCall(t, external, 1, "tools/call", map[string]any{
		"name":      "get_page",
		"arguments": map[string]any{"slug": "Page"},
	})
	text := toolResultText(t, resp)

	// Must not decrypt — ciphertext passes through
	if strings.Contains(text, secret) {
		t.Fatalf("external handler decrypted secret — must not: %q", text)
	}
	if !strings.Contains(text, "{{secure_aes:") {
		t.Fatalf("external handler should return ciphertext macro, got: %q", text)
	}

	// Must be able to edit a page with encrypted fields (no longer blocked)
	resp = mcpCall(t, external, 2, "tools/call", map[string]any{
		"name":      "edit_page",
		"arguments": map[string]any{"slug": "Page", "content": text},
	})
	toolResultText(t, resp) // must succeed, not error
}

// TestMCPSecureMacroPlaintextNotLeakedInSnippets checks that FTS/filesystem
// search snippets do not expose the raw ciphertext payload in a readable form
// (the base64 blob is opaque, but plaintext must never appear).
func TestMCPSecureMacroPlaintextNotLeakedInSnippets(t *testing.T) {
	handler, store, crypto := newTestMCPWithCrypto(t)
	secret := "snippet-secret-value"
	enc, _ := crypto.Encrypt(secret)
	// Store a page where the secret appears only inside the encrypted macro
	_ = store.Save(KindPage, "Snippet_Page", "# Snippet Page\n\nToken: {{secure_aes:"+enc+"}}\n\nOther content about snippet testing.")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "search_pages",
		"arguments": map[string]any{"query": "snippet testing"},
	})
	text := toolResultText(t, resp)
	if strings.Contains(text, secret) {
		t.Fatalf("search snippet contains decrypted secret: %q", text)
	}
}

// ── Edit mode tests ────────────────────────────────────────────────────

func TestMCPEditPageSearchReplace(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage, "SR", "Hello world, this is a test.")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "edit_page",
		"arguments": map[string]any{
			"slug":     "SR",
			"old_text": "this is a test",
			"new_text": "this works",
		},
	})
	text := toolResultText(t, resp)
	if !strings.Contains(text, "Updated") {
		t.Fatalf("unexpected result: %s", text)
	}

	page, _ := store.Load(KindPage, "SR")
	if page.Content != "Hello world, this works." {
		t.Fatalf("unexpected content: %q", page.Content)
	}
}

func TestMCPEditPageSearchReplaceNotFound(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage, "SR2", "Hello world.")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "edit_page",
		"arguments": map[string]any{
			"slug":     "SR2",
			"old_text": "does not exist",
			"new_text": "replacement",
		},
	})
	errText := toolResultIsError(t, resp)
	if !strings.Contains(errText, "not found") {
		t.Fatalf("expected not found error, got: %s", errText)
	}
}

func TestMCPEditPageSearchReplaceAmbiguous(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage, "SR3", "foo bar foo baz")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "edit_page",
		"arguments": map[string]any{
			"slug":     "SR3",
			"old_text": "foo",
			"new_text": "qux",
		},
	})
	errText := toolResultIsError(t, resp)
	if !strings.Contains(errText, "2 locations") {
		t.Fatalf("expected ambiguous error, got: %s", errText)
	}
}

func TestMCPEditPageSearchReplaceDelete(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage, "SR4", "keep this remove this keep that")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "edit_page",
		"arguments": map[string]any{
			"slug":     "SR4",
			"old_text": " remove this",
			"new_text": "",
		},
	})
	toolResultText(t, resp) // assert no error

	page, _ := store.Load(KindPage, "SR4")
	if page.Content != "keep this keep that" {
		t.Fatalf("unexpected content: %q", page.Content)
	}
}

func TestMCPEditPageAppend(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage, "AP", "Line 1\n")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "edit_page",
		"arguments": map[string]any{
			"slug":    "AP",
			"content": "Line 2",
			"append":  true,
		},
	})
	toolResultText(t, resp)

	page, _ := store.Load(KindPage, "AP")
	if page.Content != "Line 1\n\nLine 2" {
		t.Fatalf("unexpected content: %q", page.Content)
	}
}

func TestMCPEditPageSection(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage, "SEC", "# Title\nIntro\n# History\nOld history\n# Notes\nOld notes\n")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "edit_page",
		"arguments": map[string]any{
			"slug":    "SEC",
			"section": "History",
			"content": "New history content",
		},
	})
	toolResultText(t, resp)

	page, _ := store.Load(KindPage, "SEC")
	want := "# Title\nIntro\n# History\nNew history content\n# Notes\nOld notes\n"
	if page.Content != want {
		t.Fatalf("got:\n%s\nwant:\n%s", page.Content, want)
	}
}

func TestMCPEditPageSectionNotFound(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage, "SEC2", "# Alpha\nBody\n")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "edit_page",
		"arguments": map[string]any{
			"slug":    "SEC2",
			"section": "Beta",
			"content": "new",
		},
	})
	errText := toolResultIsError(t, resp)
	if !strings.Contains(errText, "section not found") {
		t.Fatalf("expected section not found error, got: %s", errText)
	}
}

func TestMCPEditPageFullReplace(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage, "FR", "old content")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "edit_page",
		"arguments": map[string]any{
			"slug":    "FR",
			"content": "new content",
		},
	})
	toolResultText(t, resp)

	page, _ := store.Load(KindPage, "FR")
	if page.Content != "new content" {
		t.Fatalf("unexpected content: %q", page.Content)
	}
}

// ── Get section tests ──────────────────────────────────────────────────

func TestMCPGetPageSection(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage, "GS", "# Title\nIntro\n# History\nSome history\n# Notes\nSome notes\n")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "get_page",
		"arguments": map[string]any{
			"slug":    "GS",
			"section": "History",
		},
	})
	text := toolResultText(t, resp)
	if text != "# History\nSome history\n" {
		t.Fatalf("unexpected section content: %q", text)
	}
}

func TestMCPGetPageSectionsOnly(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage, "SO", "# Title\nIntro\n# History\nBody\n# Notes\nBody\n")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "get_page",
		"arguments": map[string]any{
			"slug":          "SO",
			"sections_only": true,
		},
	})
	text := toolResultText(t, resp)
	if !strings.Contains(text, "Title") || !strings.Contains(text, "History") || !strings.Contains(text, "Notes") {
		t.Fatalf("expected all headings in output: %s", text)
	}
}

func TestMCPGetPageSectionAndSectionsOnlyConflict(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindPage, "CF", "# A\nBody\n")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "get_page",
		"arguments": map[string]any{
			"slug":          "CF",
			"section":       "A",
			"sections_only": true,
		},
	})
	errText := toolResultIsError(t, resp)
	if !strings.Contains(errText, "cannot use both") {
		t.Fatalf("expected conflict error, got: %s", errText)
	}
}

// ── Skill edit mode tests ──────────────────────────────────────────────

func TestMCPEditSkillSearchReplace(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindSkill, "SK", "# Skill\nOld instruction\n")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "edit_skill",
		"arguments": map[string]any{
			"slug":     "SK",
			"old_text": "Old instruction",
			"new_text": "New instruction",
		},
	})
	toolResultText(t, resp)

	skill, _ := store.Load(KindSkill, "SK")
	if !strings.Contains(skill.Content, "New instruction") {
		t.Fatalf("unexpected content: %q", skill.Content)
	}
}

func TestMCPGetSkillSection(t *testing.T) {
	handler, store := newTestMCP(t)
	_ = store.Save(KindSkill, "SK2", "# Skill\nIntro\n# When to Use\nUse when X\n")

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name": "get_skill",
		"arguments": map[string]any{
			"slug":    "SK2",
			"section": "When to Use",
		},
	})
	text := toolResultText(t, resp)
	if !strings.Contains(text, "Use when X") {
		t.Fatalf("unexpected section content: %q", text)
	}
}
