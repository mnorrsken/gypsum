package wiki

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"
)

// fakeEmbedServer is an OpenAI-compatible embeddings endpoint. Words in the
// same synonym group share a dimension, so "vehicle" is close to "automobile"
// without sharing any text; other words hash into the remaining dimensions.
type fakeEmbedServer struct {
	*httptest.Server
	mu     sync.Mutex
	calls  int
	inputs []string
	auth   string
	fail   bool
}

var fakeSynonyms = map[string]int{
	"car": 0, "automobile": 0, "vehicle": 0,
	"kubernetes": 1, "cluster": 1, "k8s": 1,
	"cake": 2, "dessert": 2, "baking": 2,
}

const fakeDims = 16

func fakeVector(text string) []float32 {
	v := make([]float32, fakeDims)
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if d, ok := fakeSynonyms[w]; ok {
			v[d] += 5
			continue
		}
		h := fnv.New32a()
		h.Write([]byte(w))
		v[3+int(h.Sum32()%(fakeDims-3))] += 0.2
	}
	return v
}

func newFakeEmbedServer(t *testing.T) *fakeEmbedServer {
	t.Helper()
	f := &fakeEmbedServer{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.calls++
		f.inputs = append(f.inputs, req.Input...)
		f.auth = r.Header.Get("Authorization")
		fail := f.fail
		f.mu.Unlock()
		if fail {
			http.Error(w, "model not loaded", http.StatusServiceUnavailable)
			return
		}
		type item struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		data := make([]item, len(req.Input))
		for i, in := range req.Input {
			// Reverse order to check the client sorts by index.
			data[len(req.Input)-1-i] = item{Index: i, Embedding: fakeVector(in)}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeEmbedServer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeEmbedServer) setFail(v bool) {
	f.mu.Lock()
	f.fail = v
	f.mu.Unlock()
}

// newSemanticTestStore returns a store with FTS5 and a semantic index backed
// by the fake server. The background worker is not started; tests call
// drain to index synchronously.
func newSemanticTestStore(t *testing.T, srv *fakeEmbedServer, model string, pages map[string]string) (*PageStore, *SemanticIndex, *DB) {
	t.Helper()
	dir := t.TempDir()
	pagesDir := filepath.Join(dir, "repo", "pages")
	if err := os.MkdirAll(pagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for slug, content := range pages {
		if err := os.WriteFile(filepath.Join(pagesDir, MarkdownFilename(slug)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := OpenDB(dir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store := NewPageStore(pagesDir)
	store.SetDB(db)
	x := indexForStore(t, srv, model, db, store)
	return store, x, db
}

func indexForStore(t *testing.T, srv *fakeEmbedServer, model string, db *DB, store *PageStore) *SemanticIndex {
	t.Helper()
	x, err := NewSemanticIndex(NewEmbedClient(EmbedConfig{URL: srv.URL + "/v1/", Model: model, APIKey: "k"}), db, store)
	if err != nil {
		t.Fatalf("new semantic index: %v", err)
	}
	store.SetSemanticIndex(x)
	x.SyncAll(KindPage)
	if err := x.drain(); err != nil {
		t.Fatalf("drain: %v", err)
	}
	return x
}

func slugsOf(results []SearchResult) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.Slug
	}
	return out
}

func TestSemanticSearchFindsByMeaning(t *testing.T) {
	srv := newFakeEmbedServer(t)
	store, _, _ := newSemanticTestStore(t, srv, "m1", map[string]string{
		"Car_Care": "# Car Care\n\nChange the automobile oil every year.",
		"Baking":   "# Baking\n\nA simple cake recipe.",
		"Infra":    "# Infra\n\nThe kubernetes cluster runs on three nodes.",
	})

	results, err := store.Search(KindPage, "vehicle")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 || results[0].Slug != "Car_Care" {
		t.Fatalf("results = %v, want Car_Care first", slugsOf(results))
	}
	if !strings.Contains(results[0].Excerpt, "automobile oil") {
		t.Errorf("excerpt = %q, want best chunk text", results[0].Excerpt)
	}
	if results[0].Snippets != nil {
		t.Errorf("vector-only hit should have no FTS snippets, got %v", results[0].Snippets)
	}
}

func TestSemanticSearchKeepsFullTextMatchesFirst(t *testing.T) {
	srv := newFakeEmbedServer(t)
	store, _, _ := newSemanticTestStore(t, srv, "m1", map[string]string{
		"Car_Care": "# Car Care\n\nChange the automobile oil every year.",
		"Garage":   "# Garage\n\nWhere the car lives. Also stores oil.",
	})

	results, err := store.Search(KindPage, "oil")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %v, want both pages", slugsOf(results))
	}
	for _, r := range results {
		if len(r.Snippets) == 0 {
			t.Errorf("%s: full-text match lost its snippets", r.Slug)
		}
	}
}

func TestSemanticIndexSkipsUnchangedAndReembedsChanged(t *testing.T) {
	srv := newFakeEmbedServer(t)
	store, x, db := newSemanticTestStore(t, srv, "m1", map[string]string{
		"A": "# A\n\nfirst",
		"B": "# B\n\nsecond",
	})
	after := srv.callCount()

	x.SyncAll(KindPage)
	if err := x.drain(); err != nil {
		t.Fatal(err)
	}
	if got := srv.callCount(); got != after {
		t.Fatalf("unchanged docs were re-embedded: calls %d → %d", after, got)
	}

	if err := store.Save(KindPage, "A", "# A\n\nchanged about cake"); err != nil {
		t.Fatal(err)
	}
	if err := x.drain(); err != nil {
		t.Fatal(err)
	}
	if got := srv.callCount(); got != after+1 {
		t.Fatalf("calls after one save = %d, want %d", got, after+1)
	}
	results, _ := store.Search(KindPage, "dessert")
	if len(results) == 0 || results[0].Slug != "A" {
		t.Fatalf("results = %v, want A first after re-embed", slugsOf(results))
	}

	// A new model re-embeds everything, and stored vectors survive a restart.
	before := srv.callCount()
	indexForStore(t, srv, "m2", db, store)
	if got := srv.callCount(); got != before+2 {
		t.Fatalf("calls after model change = %d, want %d", got, before+2)
	}
	reloaded, err := NewSemanticIndex(NewEmbedClient(EmbedConfig{URL: srv.URL + "/v1", Model: "m2"}), db, store)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(reloaded.chunks[KindPage]); n != 2 {
		t.Fatalf("reloaded %d docs from db, want 2", n)
	}
}

func TestSemanticIndexDeleteAndMissingFiles(t *testing.T) {
	srv := newFakeEmbedServer(t)
	store, x, db := newSemanticTestStore(t, srv, "m1", map[string]string{
		"Car_Care": "# Car Care\n\nautomobile",
		"Old":      "# Old\n\nvehicle notes",
	})

	if err := store.Delete(KindPage, "Car_Care"); err != nil {
		t.Fatal(err)
	}
	// A file removed by a git pull is dropped through ReindexChanged; nil
	// means the pull could not tell what changed, so everything is synced.
	if err := os.Remove(store.DocPath(KindPage, "Old")); err != nil {
		t.Fatal(err)
	}
	store.ReindexChanged(KindPage, nil)
	if err := x.drain(); err != nil {
		t.Fatal(err)
	}
	if slugs, _ := db.EmbeddedSlugs(KindPage); len(slugs) != 0 {
		t.Fatalf("embedded slugs after deletes = %v, want none", slugs)
	}
	if results, _ := store.Search(KindPage, "vehicle"); len(results) != 0 {
		t.Fatalf("results = %v, want none", slugsOf(results))
	}
}

func TestSemanticSearchFallsBackWhenEndpointFails(t *testing.T) {
	srv := newFakeEmbedServer(t)
	store, x, _ := newSemanticTestStore(t, srv, "m1", map[string]string{
		"Car_Care": "# Car Care\n\nChange the automobile oil.",
	})

	srv.setFail(true)
	results, err := store.Search(KindPage, "oil")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 || len(results[0].Snippets) == 0 {
		t.Fatalf("results = %+v, want the FTS match only", results)
	}

	// A failed indexing run keeps the document queued for the retry.
	if err := store.Save(KindPage, "Car_Care", "# Car Care\n\nnew text"); err != nil {
		t.Fatal(err)
	}
	if err := x.drain(); err == nil {
		t.Fatal("drain succeeded against a failing endpoint")
	}
	srv.setFail(false)
	if err := x.drain(); err != nil {
		t.Fatalf("retry drain: %v", err)
	}
	if got := x.chunks[KindPage]["Car_Care"][0].Excerpt; !strings.Contains(got, "new text") {
		t.Fatalf("excerpt after retry = %q", got)
	}
}

func TestSemanticIndexNeverSendsSecrets(t *testing.T) {
	srv := newFakeEmbedServer(t)
	newSemanticTestStore(t, srv, "m1", map[string]string{
		"Creds": "# Creds\n\npassword {{secure:hunter2}} and {{secure_aes2:QUJD}} and {{secure_aes:REVG}} end",
	})
	if srv.auth != "Bearer k" {
		t.Errorf("Authorization = %q, want Bearer k", srv.auth)
	}
	for _, in := range srv.inputs {
		for _, bad := range []string{"hunter2", "QUJD", "REVG", "{{secure"} {
			if strings.Contains(in, bad) {
				t.Fatalf("embedding input leaked %q: %q", bad, in)
			}
		}
	}
}

func TestSemanticIndexAddsModelPrefixes(t *testing.T) {
	tests := []struct {
		model, query, doc string
	}{
		{"ggml-org/embeddinggemma-300M-GGUF:Q8_0", "task: search result | query: ", ""},
		{"nomic-embed-text:latest", "search_query: ", "search_document: "},
		{"m1", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			srv := newFakeEmbedServer(t)
			store, _, _ := newSemanticTestStore(t, srv, tt.model, map[string]string{
				"Car_Care": "# Car Care\n\nChange the automobile oil.",
			})
			if want := tt.doc + "Car Care\n\n"; !strings.HasPrefix(srv.inputs[0], want) {
				t.Errorf("document input = %q, want prefix %q", srv.inputs[0], want)
			}
			if _, err := store.Search(KindPage, "vehicle"); err != nil {
				t.Fatal(err)
			}
			if got := srv.inputs[len(srv.inputs)-1]; got != tt.query+"vehicle" {
				t.Errorf("query input = %q, want %q", got, tt.query+"vehicle")
			}
		})
	}
}

func TestChunkDocument(t *testing.T) {
	long := strings.Repeat("word ", 700) // ~3500 bytes, no paragraph breaks
	content := "intro line\n\n## Small\n\nshort body\n\n## Big\n\n" + long
	chunks := chunkDocument("My Page", content)
	if len(chunks) < 3 {
		t.Fatalf("got %d chunks, want the long section split", len(chunks))
	}
	for i, c := range chunks {
		if !strings.HasPrefix(c.Input, "My Page\n\n") {
			t.Errorf("chunk %d input lacks title prefix: %q", i, c.Input[:20])
		}
		if n := len(strings.TrimPrefix(c.Input, "My Page\n\n")); n > embedMaxChunkChars {
			t.Errorf("chunk %d is %d bytes, max %d", i, n, embedMaxChunkChars)
		}
		if n := len([]rune(c.Excerpt)); n > embedExcerptRunes+1 {
			t.Errorf("chunk %d excerpt is %d runes", i, n)
		}
	}
	if !strings.Contains(chunks[0].Input, "intro line") || !strings.Contains(chunks[0].Input, "## Small") {
		t.Errorf("small sections were not merged into the first chunk: %q", chunks[0].Input)
	}
	if got := chunkDocument("Empty", "  \n\n"); len(got) != 0 {
		t.Errorf("empty doc gave %d chunks", len(got))
	}
}

func TestSplitLongTextRuneSafe(t *testing.T) {
	text := strings.Repeat("ö", 1000) // 2000 bytes, no separators
	parts := splitLongText(text, 301)
	var joined strings.Builder
	for _, p := range parts {
		if len(p) > 301 {
			t.Fatalf("part is %d bytes", len(p))
		}
		joined.WriteString(p)
	}
	if joined.String() != text {
		t.Fatal("split lost or corrupted text")
	}
}

func TestBlendRankings(t *testing.T) {
	fts := []SearchResult{
		{Slug: "Only_Text", Title: "Only Text", Snippets: []string{"<<x>>"}},
		{Slug: "Both", Title: "Both", Snippets: []string{"<<y>>"}},
	}
	hits := []semanticHit{
		{Slug: "Both", Score: 0.9, Excerpt: "both chunk"},
		{Slug: "Only_Meaning", Score: 0.8, Excerpt: "meaning chunk"},
	}
	got := blendRankings(fts, hits)
	want := []string{"Both", "Only_Text", "Only_Meaning"}
	if strings.Join(slugsOf(got), ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", slugsOf(got), want)
	}
	if got[0].Snippets[0] != "<<y>>" {
		t.Errorf("FTS snippets not kept: %+v", got[0])
	}
	if got[2].Title != "Only Meaning" || got[2].Excerpt != "meaning chunk" {
		t.Errorf("vector-only result = %+v", got[2])
	}
}

func TestSearchSkillsSingleMatchWithSemantic(t *testing.T) {
	srv := newFakeEmbedServer(t)
	store, x, _ := newSemanticTestStore(t, srv, "m1", nil)
	for slug, content := range map[string]string{
		"Go_Testing":   "# Go Testing\n\nUse table tests.\n\nTags: go, testing",
		"Baking_Guide": "# Baking Guide\n\nSteps for a cake.\n\nTags: kitchen",
	} {
		if err := os.WriteFile(store.DocPath(KindSkill, slug), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store.ReindexChanged(KindSkill, nil)
	if err := x.drain(); err != nil {
		t.Fatal(err)
	}
	h := NewMCPHandler(store, nil, AllMCPSections)

	// One full-text match still returns the whole skill, even though the
	// semantic side returns every skill as a candidate.
	txt := toolResultText(t, callTool(t, h, "search_skills", map[string]any{"query": []any{"table tests"}}))
	if !strings.HasPrefix(txt, "# Go Testing") {
		t.Fatalf("single match should return full content, got %q", txt)
	}

	// No full-text match: the meaning-based hit is listed.
	txt = toolResultText(t, callTool(t, h, "search_skills", map[string]any{"query": []any{"dessert"}}))
	if !strings.Contains(txt, "## Baking Guide") {
		t.Fatalf("semantic hit not listed: %q", txt)
	}
}

func TestEmbedClientErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{"http error keeps status and body", http.StatusServiceUnavailable, "model not loaded", "returned 503: model not loaded"},
		{"too few vectors", http.StatusOK, `{"data":[{"index":0,"embedding":[1,0]}]}`, "returned 1 vectors for 2 inputs"},
		{"index out of range", http.StatusOK, `{"data":[{"index":0,"embedding":[1,0]},{"index":5,"embedding":[0,1]}]}`, "bad index 5"},
		{"duplicate index", http.StatusOK, `{"data":[{"index":0,"embedding":[1,0]},{"index":0,"embedding":[0,1]}]}`, "bad index 0"},
		{"empty vector", http.StatusOK, `{"data":[{"index":0,"embedding":[]},{"index":1,"embedding":[0,1]}]}`, "empty vector"},
		{"not json", http.StatusOK, `<html>proxy error</html>`, "decoding embeddings response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			c := NewEmbedClient(EmbedConfig{URL: srv.URL, Model: "m"})
			_, err := c.Embed(t.Context(), []string{"a", "b"})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestEmbedClientOrdersAndNormalizes(t *testing.T) {
	var gotPath, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotModel = req.Model
		_, _ = w.Write([]byte(`{"data":[{"index":1,"embedding":[0,2]},{"index":0,"embedding":[3,4]}]}`))
	}))
	defer srv.Close()

	c := NewEmbedClient(EmbedConfig{URL: srv.URL + "/v1/", Model: "my-model"})
	vecs, err := c.Embed(t.Context(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/embeddings" || gotModel != "my-model" {
		t.Errorf("request path %q model %q", gotPath, gotModel)
	}
	want := [][]float32{{0.6, 0.8}, {0, 1}}
	for i := range want {
		for j := range want[i] {
			if d := vecs[i][j] - want[i][j]; d > 1e-6 || d < -1e-6 {
				t.Fatalf("vecs = %v, want %v (sorted by index, unit length)", vecs, want)
			}
		}
	}
}

func TestEmbedClientTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	c := NewEmbedClient(EmbedConfig{URL: srv.URL, Model: "m"})
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Embed(ctx, []string{"a"}); err == nil {
		t.Fatal("expected a timeout error")
	}
}
