package wiki

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPCommitAuthor(t *testing.T) {
	tests := []struct {
		name   string
		client *mcpServerInfo
		want   string
	}{
		{"no client info", nil, "MCP"},
		{"empty name", &mcpServerInfo{Name: "  "}, "MCP"},
		{"plain name", &mcpServerInfo{Name: "claude-code", Version: "2.1"}, "claude-code (MCP)"},
		{"strips email delimiters and control chars", &mcpServerInfo{Name: "evil <x@y>\nname"}, "evil x@yname (MCP)"},
		{"caps length", &mcpServerInfo{Name: strings.Repeat("a", 100)}, strings.Repeat("a", mcpCommitAuthorMaxLen) + " (MCP)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mcpCommitAuthor(tt.client); got != tt.want {
				t.Errorf("mcpCommitAuthor() = %q, want %q", got, tt.want)
			}
		})
	}
}

// newTestMCPWithGit returns an MCP handler whose writes are committed to a
// fresh git repo in the returned data dir.
func newTestMCPWithGit(t *testing.T) (*MCPHandler, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dataDir := t.TempDir()
	pagesDir := filepath.Join(dataDir, "pages")
	if err := os.MkdirAll(pagesDir, 0o755); err != nil {
		t.Fatalf("create pages dir: %v", err)
	}
	committer := NewGitAutoCommitter(dataDir, nil)
	return NewMCPHandler(NewPageStore(pagesDir), committer, AllMCPSections), dataDir
}

func lastCommitAuthor(t *testing.T, dataDir string) string {
	t.Helper()
	return strings.TrimSpace(runGit(t, dataDir, "log", "-1", "--format=%an"))
}

func TestMCPLegacyCommitAuthorFromInitialize(t *testing.T) {
	handler, dataDir := newTestMCPWithGit(t)

	post := func(method string, params any, sid string) *httptest.ResponseRecorder {
		data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		if sid != "" {
			req.Header.Set("Mcp-Session-Id", sid)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", method, rec.Code, rec.Body.String())
		}
		return rec
	}

	rec := post("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "claude-code", "version": "2.1"},
	}, "")
	sid := rec.Header().Get("Mcp-Session-Id")

	post("tools/call", map[string]any{
		"name":      "create_page",
		"arguments": map[string]any{"title": "Legacy", "content": "# Legacy"},
	}, sid)
	if got := lastCommitAuthor(t, dataDir); got != "claude-code (MCP)" {
		t.Errorf("author with session = %q, want %q", got, "claude-code (MCP)")
	}

	post("tools/call", map[string]any{
		"name":      "edit_page",
		"arguments": map[string]any{"slug": "Legacy", "content": "# Legacy\n\nno session"},
	}, "unknown-session")
	if got := lastCommitAuthor(t, dataDir); got != "MCP" {
		t.Errorf("author for unknown session = %q, want %q", got, "MCP")
	}
}

func TestMCPModernCommitAuthorFromMeta(t *testing.T) {
	handler, dataDir := newTestMCPWithGit(t)

	status, resp := modernPost(t, handler, "tools/call", modernParams(protocolVersionModern, map[string]any{
		"name":      "create_page",
		"arguments": map[string]any{"title": "Modern", "content": "# Modern"},
	}), nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	var result mcpCallToolResult
	decodeResult(t, resp, &result)
	if result.IsError {
		t.Fatalf("create_page failed: %s", result.Content[0].Text)
	}
	if got := lastCommitAuthor(t, dataDir); got != "test-client (MCP)" {
		t.Errorf("author = %q, want %q", got, "test-client (MCP)")
	}
}
