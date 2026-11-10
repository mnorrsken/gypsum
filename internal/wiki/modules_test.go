package wiki

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mnorrsken/gypsum/web"
)

func TestParseModules(t *testing.T) {
	tests := []struct {
		in      string
		want    []Module
		wantErr bool
	}{
		{in: "", want: []Module{ModuleFiles, ModuleSecrets, ModuleGraph, ModuleSkills, ModuleScan, ModuleSharing, ModuleMCP}},
		{in: "notes, Skills", want: []Module{ModuleNotes, ModuleSkills}},
		{in: "none", want: nil},
		{in: "files,bogus", wantErr: true},
	}
	for _, tt := range tests {
		got, err := ParseModules(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseModules(%q): expected error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseModules(%q): %v", tt.in, err)
		}
		if len(got) != len(tt.want) {
			t.Errorf("ParseModules(%q) = %v, want %v", tt.in, got, tt.want)
		}
		for _, m := range tt.want {
			if !got.Has(m) {
				t.Errorf("ParseModules(%q): %s not enabled", tt.in, m)
			}
		}
	}
	if !ModuleSet(nil).Has(ModuleNotes) {
		t.Error("nil ModuleSet should enable every module")
	}
}

func TestModuleRoutes(t *testing.T) {
	store := NewPageStore(t.TempDir())
	h := NewHandler(store, NewMarkdownRenderer(), web.Templates(), nil, nil, nil, AllMCPSections)
	h.SetModules(ModuleSet{ModuleSecrets: true})
	routes := h.Routes()

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	for _, path := range []string{"/notes", "/images", "/graph", "/skills", "/new-skill", "/scan", "/share/Home", "/public/abc"} {
		if code := get(path).Code; code != http.StatusNotFound {
			t.Errorf("GET %s with module off: got %d, want 404", path, code)
		}
	}
	if code := get("/secrets").Code; code != http.StatusOK {
		t.Errorf("GET /secrets with module on: got %d, want 200", code)
	}

	// The sidebar lists only enabled modules.
	body := get("/pages").Body.String()
	if !strings.Contains(body, `href="/secrets"`) {
		t.Error("sidebar should link to /secrets")
	}
	for _, link := range []string{`href="/notes"`, `href="/images"`, `href="/graph"`, `href="/skills"`, `href="/new-skill"`} {
		if strings.Contains(body, link) {
			t.Errorf("sidebar should not contain %s", link)
		}
	}
}

func TestMCPModuleRoutes(t *testing.T) {
	for _, on := range []bool{true, false} {
		h, _, _ := newTestHandlerWithOAuth(t)
		if !on {
			h.SetModules(ModuleSet{ModuleFiles: true})
		}
		routes := h.Routes()
		for _, path := range []string{"/mcp", "/oauth/authorize", "/.well-known/oauth-authorization-server"} {
			rec := httptest.NewRecorder()
			routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if got := rec.Code == http.StatusNotFound; got == on {
				t.Errorf("GET %s with mcp module on=%v: got %d", path, on, rec.Code)
			}
		}
	}
}

func TestMCPModuleTools(t *testing.T) {
	pagesDir := filepath.Join(t.TempDir(), "pages")
	_ = os.MkdirAll(pagesDir, 0o755)
	handler := NewMCPHandler(NewPageStore(pagesDir), nil, AllMCPSections)
	handler.SetModules(ModuleSet{ModuleSkills: true})

	listed := map[string]bool{}
	for _, tool := range handler.toolDefinitions() {
		listed[tool.Name] = true
	}
	for name, mod := range toolModuleMap {
		if want := mod == ModuleSkills; listed[name] != want {
			t.Errorf("tool %s (module %s): listed=%v, want %v", name, mod, listed[name], want)
		}
	}
	if !listed["link_graph"] || !listed["get_page"] {
		t.Error("core tools should stay listed")
	}

	resp := mcpCall(t, handler, 1, "tools/call", map[string]any{
		"name":      "list_notes",
		"arguments": map[string]any{},
	})
	raw, _ := json.Marshal(resp.Result)
	var result mcpCallToolResult
	_ = json.Unmarshal(raw, &result)
	if !result.IsError {
		t.Fatal("expected error calling list_notes with the notes module off")
	}

	if strings.Contains(handler.serverInstructions(), "notes") {
		t.Error("server instructions should not mention notes with the module off")
	}
}
