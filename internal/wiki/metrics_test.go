package wiki

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPMetricsRecord(t *testing.T) {
	m := NewMCPMetrics()

	m.Record("get_page", 50, 200, false)
	m.Record("get_page", 60, 300, false)
	m.Record("create_page", 100, 30, false)
	m.Record("search_pages", 40, 500, true)

	tm := m.get("get_page")
	if got := tm.calls.Load(); got != 2 {
		t.Fatalf("get_page calls = %d, want 2", got)
	}
	if got := tm.sentChars.Load(); got != 110 {
		t.Fatalf("get_page sentChars = %d, want 110", got)
	}
	if got := tm.receivedChars.Load(); got != 500 {
		t.Fatalf("get_page receivedChars = %d, want 500", got)
	}
	if got := tm.errors.Load(); got != 0 {
		t.Fatalf("get_page errors = %d, want 0", got)
	}

	tm2 := m.get("search_pages")
	if got := tm2.errors.Load(); got != 1 {
		t.Fatalf("search_pages errors = %d, want 1", got)
	}
}

func TestMCPMetricsHandler(t *testing.T) {
	m := NewMCPMetrics()
	m.Record("get_page", 50, 200, false)
	m.Record("create_page", 100, 30, true)

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	body := rec.Body.String()

	checks := []string{
		`gypsum_mcp_tool_calls_total{tool="get_page"} 1`,
		`gypsum_mcp_tool_calls_total{tool="create_page"} 1`,
		`gypsum_mcp_tool_errors_total{tool="create_page"} 1`,
		`gypsum_mcp_tool_errors_total{tool="get_page"} 0`,
		`gypsum_mcp_tool_sent_chars_total{tool="get_page"} 50`,
		`gypsum_mcp_tool_received_chars_total{tool="get_page"} 200`,
		`gypsum_mcp_tool_sent_chars_total{tool="create_page"} 100`,
		`gypsum_mcp_tool_received_chars_total{tool="create_page"} 30`,
	}
	for _, want := range checks {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in output:\n%s", want, body)
		}
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
}

func TestMCPMetricsIntegration(t *testing.T) {
	handler, _ := newTestMCP(t)

	// Make a tool call
	mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "list_pages",
		"arguments": map[string]any{},
	})

	// The handler should have metrics recorded — we can't easily access them
	// from the test MCP handler since newTestMCP doesn't wire metrics.
	// This test verifies that the code path works without panicking.
}
