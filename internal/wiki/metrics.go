package wiki

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// MCPMetrics tracks approximate sent and received characters for each MCP tool.
type MCPMetrics struct {
	mu    sync.RWMutex
	tools map[string]*toolMetrics
}

type toolMetrics struct {
	sentChars     atomic.Int64
	receivedChars atomic.Int64
	calls         atomic.Int64
	errors        atomic.Int64
}

func NewMCPMetrics() *MCPMetrics {
	return &MCPMetrics{tools: make(map[string]*toolMetrics)}
}

func (m *MCPMetrics) get(tool string) *toolMetrics {
	m.mu.RLock()
	tm := m.tools[tool]
	m.mu.RUnlock()
	if tm != nil {
		return tm
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tm = m.tools[tool]
	if tm == nil {
		tm = &toolMetrics{}
		m.tools[tool] = tm
	}
	return tm
}

// Record records a tool call with the given sent/received character counts.
func (m *MCPMetrics) Record(tool string, sentChars, receivedChars int, isError bool) {
	tm := m.get(tool)
	tm.sentChars.Add(int64(sentChars))
	tm.receivedChars.Add(int64(receivedChars))
	tm.calls.Add(1)
	if isError {
		tm.errors.Add(1)
	}
}

// Handler returns an HTTP handler that serves Prometheus exposition format.
func (m *MCPMetrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		m.mu.RLock()
		tools := make([]string, 0, len(m.tools))
		for name := range m.tools {
			tools = append(tools, name)
		}
		m.mu.RUnlock()
		sort.Strings(tools)

		var sb strings.Builder

		sb.WriteString("# HELP gypsum_mcp_tool_calls_total Total number of MCP tool calls.\n")
		sb.WriteString("# TYPE gypsum_mcp_tool_calls_total counter\n")
		for _, name := range tools {
			tm := m.get(name)
			fmt.Fprintf(&sb, "gypsum_mcp_tool_calls_total{tool=%q} %d\n", name, tm.calls.Load())
		}

		sb.WriteString("# HELP gypsum_mcp_tool_errors_total Total number of MCP tool errors.\n")
		sb.WriteString("# TYPE gypsum_mcp_tool_errors_total counter\n")
		for _, name := range tools {
			tm := m.get(name)
			fmt.Fprintf(&sb, "gypsum_mcp_tool_errors_total{tool=%q} %d\n", name, tm.errors.Load())
		}

		sb.WriteString("# HELP gypsum_mcp_tool_sent_chars_total Approximate characters sent to MCP tools.\n")
		sb.WriteString("# TYPE gypsum_mcp_tool_sent_chars_total counter\n")
		for _, name := range tools {
			tm := m.get(name)
			fmt.Fprintf(&sb, "gypsum_mcp_tool_sent_chars_total{tool=%q} %d\n", name, tm.sentChars.Load())
		}

		sb.WriteString("# HELP gypsum_mcp_tool_received_chars_total Approximate characters received from MCP tools.\n")
		sb.WriteString("# TYPE gypsum_mcp_tool_received_chars_total counter\n")
		for _, name := range tools {
			tm := m.get(name)
			fmt.Fprintf(&sb, "gypsum_mcp_tool_received_chars_total{tool=%q} %d\n", name, tm.receivedChars.Load())
		}

		w.Write([]byte(sb.String()))
	})
}
