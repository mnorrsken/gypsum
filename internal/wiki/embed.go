package wiki

// Semantic search. Pages and skills are split into heading-sized chunks,
// embedded through an OpenAI-compatible /embeddings endpoint (Ollama, OpenAI,
// llama.cpp, vLLM, LiteLLM, ...), and kept in gypsum.db next to the FTS5
// index. Like FTS5, the vectors are derived data: git stays the source of
// truth and everything can be rebuilt from it. PageStore.Search blends the
// vector ranking with the FTS5 ranking (see blendSemantic).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// EmbedConfig configures the embeddings endpoint.
type EmbedConfig struct {
	URL    string // base URL; "/embeddings" is appended, e.g. http://ollama:11434/v1
	Model  string // embedding model name, e.g. nomic-embed-text
	APIKey string // optional; sent as a Bearer token
}

const (
	// embedChunkerVersion is part of every document hash, so changing how
	// documents are chunked re-embeds them on the next sync.
	embedChunkerVersion = "1"
	embedMaxChunkChars  = 1500
	embedExcerptRunes   = 240
	embedBatchSize      = 32
	embedIndexTimeout   = 60 * time.Second
	embedQueryTimeout   = 5 * time.Second
	embedRetryMin       = 30 * time.Second
	embedRetryMax       = 10 * time.Minute
	// semanticCandidates is how many vector hits take part in the blend.
	semanticCandidates = 20
)

// ── Embeddings client ───────────────────────────────────────────────────

// EmbedClient calls an OpenAI-compatible embeddings endpoint.
type EmbedClient struct {
	cfg  EmbedConfig
	http *http.Client
}

func NewEmbedClient(cfg EmbedConfig) *EmbedClient {
	return &EmbedClient{cfg: cfg, http: &http.Client{}}
}

// Embed returns one unit-length vector per input, in input order.
func (c *EmbedClient) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	body, err := json.Marshal(map[string]any{"model": c.cfg.Model, "input": inputs})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.cfg.URL, "/")+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building embeddings request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling embeddings endpoint: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("embeddings endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding embeddings response: %w", err)
	}
	if len(out.Data) != len(inputs) {
		return nil, fmt.Errorf("embeddings endpoint returned %d vectors for %d inputs", len(out.Data), len(inputs))
	}
	vecs := make([][]float32, len(inputs))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(inputs) || vecs[d.Index] != nil {
			return nil, fmt.Errorf("embeddings endpoint returned bad index %d", d.Index)
		}
		if len(d.Embedding) == 0 {
			return nil, fmt.Errorf("embeddings endpoint returned an empty vector")
		}
		vecs[d.Index] = normalizeVector(d.Embedding)
	}
	return vecs, nil
}

// normalizeVector scales v to unit length, so cosine similarity becomes a
// plain dot product at query time.
func normalizeVector(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	n := float32(1 / math.Sqrt(sum))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x * n
	}
	return out
}

func dotProduct(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

// ── Chunking ────────────────────────────────────────────────────────────

// docChunk is one embeddable piece of a document.
type docChunk struct {
	Input   string // text sent to the embedding model (title + chunk)
	Excerpt string // short plain excerpt shown in search results
}

// stripSecureBlocks removes encrypted and plaintext secure macros. Ciphertext
// is noise to an embedding model, and plaintext must never leave the server.
func stripSecureBlocks(content string) string {
	content = secureAesMacroRe.ReplaceAllString(content, "")
	content = secureAes2MacroRe.ReplaceAllString(content, "")
	return secureMacroRe.ReplaceAllString(content, "")
}

// chunkDocument splits a document into chunks of at most embedMaxChunkChars,
// keeping heading sections together where they fit. Each chunk's input is
// prefixed with the document title so short sections keep their context.
func chunkDocument(title, content string) []docChunk {
	var pieces []string
	for _, sec := range ParseSections(stripSecureBlocks(content)) {
		pieces = append(pieces, splitLongText(strings.TrimSpace(sec.Body), embedMaxChunkChars)...)
	}

	var texts []string
	var cur strings.Builder
	for _, p := range pieces {
		if p == "" {
			continue
		}
		if cur.Len() > 0 && cur.Len()+2+len(p) > embedMaxChunkChars {
			texts = append(texts, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteString("\n\n")
		}
		cur.WriteString(p)
	}
	if cur.Len() > 0 {
		texts = append(texts, cur.String())
	}

	chunks := make([]docChunk, 0, len(texts))
	for _, t := range texts {
		chunks = append(chunks, docChunk{Input: title + "\n\n" + t, Excerpt: chunkExcerpt(t)})
	}
	return chunks
}

// splitLongText splits text over max bytes at paragraph breaks, then at
// line breaks, then hard at rune boundaries.
func splitLongText(text string, max int) []string {
	if len(text) <= max {
		return []string{text}
	}
	for _, sep := range []string{"\n\n", "\n"} {
		parts := strings.Split(text, sep)
		if len(parts) < 2 {
			continue
		}
		var out []string
		var cur strings.Builder
		for _, p := range parts {
			if cur.Len() > 0 && cur.Len()+len(sep)+len(p) > max {
				out = append(out, cur.String())
				cur.Reset()
			}
			if cur.Len() > 0 {
				cur.WriteString(sep)
			}
			cur.WriteString(p)
		}
		if cur.Len() > 0 {
			out = append(out, cur.String())
		}
		var final []string
		for _, o := range out {
			final = append(final, splitLongText(o, max)...)
		}
		return final
	}
	var out []string
	r := []rune(text)
	for len(r) > 0 {
		n := 0
		size := 0
		for n < len(r) && size+len(string(r[n])) <= max {
			size += len(string(r[n]))
			n++
		}
		if n == 0 {
			n = 1
		}
		out = append(out, string(r[:n]))
		r = r[n:]
	}
	return out
}

// chunkExcerpt drops heading markers, collapses whitespace and cuts the chunk
// to embedExcerptRunes.
func chunkExcerpt(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if _, heading, ok := parseHeading(line); ok {
			lines[i] = heading
		}
	}
	s := strings.Join(strings.Fields(strings.Join(lines, "\n")), " ")
	if r := []rune(s); len(r) > embedExcerptRunes {
		return strings.TrimSpace(string(r[:embedExcerptRunes])) + "…"
	}
	return s
}

// ── Semantic index ──────────────────────────────────────────────────────

type docRef struct {
	Kind DocKind
	Slug string
}

type embedChunk struct {
	Excerpt string
	Vec     []float32
}

// semanticHit is one document matched by vector similarity.
type semanticHit struct {
	Slug    string
	Score   float32
	Excerpt string // excerpt of the best-matching chunk
}

// semanticKinds are the document kinds that get embedded.
var semanticKinds = []DocKind{KindPage, KindSkill}

func isSemanticKind(kind DocKind) bool {
	return kind == KindPage || kind == KindSkill
}

// SemanticIndex keeps document embeddings in gypsum.db and in memory, and
// updates them in the background as documents change.
type SemanticIndex struct {
	client  *EmbedClient
	db      *DB
	store   *PageStore
	model   string
	backoff time.Duration

	mu     sync.RWMutex
	chunks map[DocKind]map[string][]embedChunk

	pendingMu sync.Mutex
	pending   map[docRef]bool
	wake      chan struct{}
}

// NewSemanticIndex loads stored embeddings into memory. Call Start to begin
// background indexing.
func NewSemanticIndex(client *EmbedClient, db *DB, store *PageStore) (*SemanticIndex, error) {
	x := &SemanticIndex{
		client:  client,
		db:      db,
		store:   store,
		model:   client.cfg.Model,
		chunks:  map[DocKind]map[string][]embedChunk{},
		pending: map[docRef]bool{},
		wake:    make(chan struct{}, 1),
	}
	for _, kind := range semanticKinds {
		stored, err := db.LoadEmbeddings(kind)
		if err != nil {
			return nil, fmt.Errorf("loading %s embeddings: %w", kind.Label(), err)
		}
		x.chunks[kind] = stored
	}
	return x, nil
}

// Start queues a sync of every page and skill and starts the background
// worker. Documents whose content and model are unchanged are skipped.
func (x *SemanticIndex) Start() {
	for _, kind := range semanticKinds {
		x.SyncAll(kind)
	}
	go x.run()
}

// Enqueue schedules a document to be (re-)embedded.
func (x *SemanticIndex) Enqueue(kind DocKind, slug string) {
	if !isSemanticKind(kind) || strings.HasPrefix(slug, "_") {
		return
	}
	x.pendingMu.Lock()
	x.pending[docRef{kind, slug}] = true
	x.pendingMu.Unlock()
	select {
	case x.wake <- struct{}{}:
	default:
	}
}

// SyncAll queues every document of kind on disk and drops embeddings of
// documents that no longer exist.
func (x *SemanticIndex) SyncAll(kind DocKind) {
	if !isSemanticKind(kind) {
		return
	}
	onDisk := map[string]bool{}
	entries, err := os.ReadDir(x.store.docDir(kind))
	if err != nil {
		log.Printf("embed: sync %s failed to read dir: %v", kind.Label(), err)
		return
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		onDisk[SlugFromFilename(e.Name())] = true
	}
	known, err := x.db.EmbeddedSlugs(kind)
	if err != nil {
		log.Printf("embed: sync %s failed to list embedded docs: %v", kind.Label(), err)
	}
	for _, slug := range known {
		if !onDisk[slug] {
			x.Remove(kind, slug)
		}
	}
	for slug := range onDisk {
		x.Enqueue(kind, slug)
	}
}

// Remove drops a document's embeddings immediately.
func (x *SemanticIndex) Remove(kind DocKind, slug string) {
	if !isSemanticKind(kind) {
		return
	}
	if err := x.db.DeleteEmbeddings(kind, slug); err != nil {
		log.Printf("embed: removing %s %s: %v", kind.Label(), slug, err)
	}
	x.mu.Lock()
	delete(x.chunks[kind], slug)
	x.mu.Unlock()
}

// run is the background worker: it drains the queue whenever woken and backs
// off while the embeddings endpoint is failing.
func (x *SemanticIndex) run() {
	for range x.wake {
		if err := x.drain(); err != nil {
			x.backoff = min(max(x.backoff*2, embedRetryMin), embedRetryMax)
			log.Printf("embed: indexing failed, retrying in %s: %v", x.backoff, err)
			time.Sleep(x.backoff)
			select {
			case x.wake <- struct{}{}:
			default:
			}
			continue
		}
		x.backoff = 0
	}
}

// drain embeds every queued document. On an endpoint error the failed
// document and the rest of the queue are put back and the error returned.
func (x *SemanticIndex) drain() error {
	x.pendingMu.Lock()
	refs := make([]docRef, 0, len(x.pending))
	for r := range x.pending {
		refs = append(refs, r)
	}
	x.pending = map[docRef]bool{}
	x.pendingMu.Unlock()
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Kind != refs[j].Kind {
			return refs[i].Kind < refs[j].Kind
		}
		return refs[i].Slug < refs[j].Slug
	})

	embedded := 0
	for i, r := range refs {
		did, err := x.indexDoc(r)
		if err != nil {
			x.pendingMu.Lock()
			for _, rest := range refs[i:] {
				x.pending[rest] = true
			}
			x.pendingMu.Unlock()
			return err
		}
		if did {
			embedded++
		}
	}
	if embedded > 0 {
		log.Printf("embed: embedded %d document(s)", embedded)
	}
	return nil
}

// indexDoc embeds one document if its content or the model changed. It
// reports whether the embedding endpoint was called.
func (x *SemanticIndex) indexDoc(r docRef) (bool, error) {
	content, err := os.ReadFile(x.store.DocPath(r.Kind, r.Slug))
	if err != nil {
		if os.IsNotExist(err) {
			x.Remove(r.Kind, r.Slug)
			return false, nil
		}
		return false, err
	}
	hash := x.docHash(content)
	if stored, ok, err := x.db.EmbeddingHash(r.Kind, r.Slug); err != nil {
		return false, err
	} else if ok && stored == hash {
		return false, nil
	}

	chunks := chunkDocument(TitleFromSlug(r.Slug), string(content))
	stored := make([]embedChunk, 0, len(chunks))
	for start := 0; start < len(chunks); start += embedBatchSize {
		end := min(start+embedBatchSize, len(chunks))
		inputs := make([]string, 0, end-start)
		for _, c := range chunks[start:end] {
			inputs = append(inputs, c.Input)
		}
		ctx, cancel := context.WithTimeout(context.Background(), embedIndexTimeout)
		vecs, err := x.client.Embed(ctx, inputs)
		cancel()
		if err != nil {
			return true, fmt.Errorf("embedding %s %s: %w", r.Kind.Label(), r.Slug, err)
		}
		for i, v := range vecs {
			stored = append(stored, embedChunk{Excerpt: chunks[start+i].Excerpt, Vec: v})
		}
	}
	if err := x.db.SaveEmbeddings(r.Kind, r.Slug, hash, stored); err != nil {
		return true, fmt.Errorf("saving embeddings for %s %s: %w", r.Kind.Label(), r.Slug, err)
	}
	x.mu.Lock()
	x.chunks[r.Kind][r.Slug] = stored
	x.mu.Unlock()
	return true, nil
}

// docHash identifies what a stored embedding was built from: the content,
// the model and the chunker version.
func (x *SemanticIndex) docHash(content []byte) string {
	h := sha256.New()
	h.Write([]byte(x.model + "\x00" + embedChunkerVersion + "\x00"))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

// Search embeds the query and returns up to limit documents of kind, best
// match first, scored by their best chunk.
func (x *SemanticIndex) Search(ctx context.Context, kind DocKind, query string, limit int) ([]semanticHit, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, embedQueryTimeout)
	defer cancel()
	vecs, err := x.client.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	q := vecs[0]

	x.mu.RLock()
	hits := make([]semanticHit, 0, len(x.chunks[kind]))
	for slug, chunks := range x.chunks[kind] {
		best := semanticHit{Slug: slug, Score: -2}
		for _, c := range chunks {
			if len(c.Vec) != len(q) {
				continue // stored with another model; replaced on next sync
			}
			if s := dotProduct(q, c.Vec); s > best.Score {
				best.Score, best.Excerpt = s, c.Excerpt
			}
		}
		if best.Score > -2 {
			hits = append(hits, best)
		}
	}
	x.mu.RUnlock()

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Slug < hits[j].Slug
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// ── Blending ────────────────────────────────────────────────────────────

// rrfK is the Reciprocal Rank Fusion constant: a document's blended score is
// the sum of 1/(rrfK+rank) over the rankings it appears in.
const rrfK = 60

// maxBlendedResults matches the FTS5 result cap.
const maxBlendedResults = 50

// blendRankings merges FTS5 results and vector hits with Reciprocal Rank
// Fusion. FTS results keep their highlighted snippets; documents found only
// by meaning get the excerpt of their best chunk.
func blendRankings(fts []SearchResult, hits []semanticHit) []SearchResult {
	type entry struct {
		result SearchResult
		score  float64
		order  int // first-seen position, for stable ties
	}
	bySlug := map[string]*entry{}
	var entries []*entry
	for i, r := range fts {
		e := &entry{result: r, score: 1 / float64(rrfK+i+1), order: len(entries)}
		bySlug[r.Slug] = e
		entries = append(entries, e)
	}
	for i, h := range hits {
		s := 1 / float64(rrfK+i+1)
		if e, ok := bySlug[h.Slug]; ok {
			e.score += s
			continue
		}
		e := &entry{
			result: SearchResult{Slug: h.Slug, Title: TitleFromSlug(h.Slug), Excerpt: h.Excerpt},
			score:  s,
			order:  len(entries),
		}
		bySlug[h.Slug] = e
		entries = append(entries, e)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].score != entries[j].score {
			return entries[i].score > entries[j].score
		}
		return entries[i].order < entries[j].order
	})
	if len(entries) > maxBlendedResults {
		entries = entries[:maxBlendedResults]
	}
	out := make([]SearchResult, len(entries))
	for i, e := range entries {
		out[i] = e.result
	}
	return out
}
