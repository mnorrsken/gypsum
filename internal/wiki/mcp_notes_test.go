package wiki

import (
	"encoding/json"
	"strings"
	"testing"
)

func callTool(t *testing.T, h *MCPHandler, name string, args map[string]any) jsonRPCResponse {
	t.Helper()
	return mcpCall(t, h, 1, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
}

func TestMCPNoteRoundTrip(t *testing.T) {
	h, _ := newTestMCP(t)

	// Create.
	txt := toolResultText(t, callTool(t, h, "create_note", map[string]any{"content": "MCP note\nbody line"}))
	if !strings.Contains(txt, "Created note") {
		t.Fatalf("create_note text = %q", txt)
	}

	// List → find the id.
	listTxt := toolResultText(t, callTool(t, h, "list_notes", map[string]any{}))
	var notes []NoteEntry
	if err := json.Unmarshal([]byte(listTxt), &notes); err != nil {
		t.Fatalf("list_notes not JSON: %v (%s)", err, listTxt)
	}
	if len(notes) != 1 {
		t.Fatalf("list_notes returned %d notes", len(notes))
	}
	id := notes[0].ID
	if notes[0].Title != "MCP note" {
		t.Fatalf("title = %q, want MCP note", notes[0].Title)
	}
	if notes[0].Content != "" {
		t.Errorf("list_notes should omit content, got %q", notes[0].Content)
	}

	// Get → full content present.
	getTxt := toolResultText(t, callTool(t, h, "get_note", map[string]any{"id": id}))
	var note NoteEntry
	if err := json.Unmarshal([]byte(getTxt), &note); err != nil {
		t.Fatalf("get_note not JSON: %v", err)
	}
	if !strings.Contains(note.Content, "body line") {
		t.Fatalf("get_note content = %q", note.Content)
	}

	// Edit (append).
	toolResultText(t, callTool(t, h, "edit_note", map[string]any{"id": id, "append": true, "content": "appended"}))
	getTxt = toolResultText(t, callTool(t, h, "get_note", map[string]any{"id": id}))
	if !strings.Contains(getTxt, "appended") {
		t.Fatalf("edit_note append not reflected: %s", getTxt)
	}

	// Archive → excluded from default list, included with include_archived.
	toolResultText(t, callTool(t, h, "archive_note", map[string]any{"id": id}))
	if txt := toolResultText(t, callTool(t, h, "list_notes", map[string]any{})); !strings.Contains(txt, "No notes found") {
		t.Fatalf("archived note still listed: %s", txt)
	}
	inclTxt := toolResultText(t, callTool(t, h, "list_notes", map[string]any{"include_archived": true}))
	if err := json.Unmarshal([]byte(inclTxt), &notes); err != nil || len(notes) != 1 {
		t.Fatalf("include_archived list = %s (err %v)", inclTxt, err)
	}
	if !notes[0].Archived {
		t.Error("archived note missing Archived flag in list")
	}

	// Restore.
	toolResultText(t, callTool(t, h, "archive_note", map[string]any{"id": id, "restore": true}))
	if txt := toolResultText(t, callTool(t, h, "list_notes", map[string]any{})); strings.Contains(txt, "No notes found") {
		t.Fatal("note not restored to board")
	}

	// Delete.
	toolResultText(t, callTool(t, h, "delete_note", map[string]any{"id": id}))
	if txt := toolResultIsError(t, callTool(t, h, "get_note", map[string]any{"id": id})); !strings.Contains(txt, "not found") {
		t.Fatalf("get_note after delete = %q", txt)
	}
}

func TestMCPNoteSearch(t *testing.T) {
	h, _ := newTestMCP(t)
	toolResultText(t, callTool(t, h, "create_note", map[string]any{"content": "Kubernetes rollout\ncheck the deploy"}))
	toolResultText(t, callTool(t, h, "create_note", map[string]any{"content": "Grocery list\nmilk and eggs"}))

	txt := toolResultText(t, callTool(t, h, "list_notes", map[string]any{"query": []any{"kubernetes"}}))
	var notes []NoteEntry
	if err := json.Unmarshal([]byte(txt), &notes); err != nil {
		t.Fatalf("search not JSON: %v (%s)", err, txt)
	}
	if len(notes) != 1 || notes[0].Title != "Kubernetes rollout" {
		t.Fatalf("query search = %s", txt)
	}
}

func TestMCPNoteSectionDisabled(t *testing.T) {
	dir := t.TempDir()
	store := NewPageStore(dir + "/pages")
	h := NewMCPHandler(store, nil, map[MCPSection]bool{MCPSectionRead: true})

	txt := toolResultIsError(t, callTool(t, h, "create_note", map[string]any{"content": "x"}))
	if !strings.Contains(txt, "not available") {
		t.Fatalf("disabled section error = %q", txt)
	}
}
