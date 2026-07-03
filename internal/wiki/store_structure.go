package wiki

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RecentPageInfo is a recently-modified page with its modification time.
type RecentPageInfo struct {
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	Modified string `json:"modified"` // RFC3339 timestamp
}

// ParentSuggestion ranks a candidate page under which a new page could be
// filed. It combines full-text relevance with the page's position in the link
// graph (hub pages with many outgoing links are preferred as parents).
type ParentSuggestion struct {
	Slug         string   `json:"slug"`
	Title        string   `json:"title"`
	OutLinks     int      `json:"out_links"`               // pages this page links to
	BackLinks    int      `json:"back_links"`              // pages linking to this page
	Score        float64  `json:"score"`                   // ranking score (higher = better fit)
	Reason       string   `json:"reason"`                  // why this page was suggested
	LinkSections []string `json:"link_sections,omitempty"` // headings that already contain [[links]]
	SampleLinks  []string `json:"sample_links,omitempty"`  // a few existing linked slugs, for context
}

// RecentPagesWithTime returns the n most recently modified pages including
// their modification timestamps (RFC3339, UTC).
func (s *PageStore) RecentPagesWithTime(n int) ([]RecentPageInfo, error) {
	entries, err := os.ReadDir(s.pagesDir)
	if err != nil {
		return nil, err
	}

	type fileEntry struct {
		slug    string
		modTime int64
		iso     string
	}
	var files []fileEntry
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		slug := SlugFromFilename(entry.Name())
		if strings.HasPrefix(slug, "_") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		mt := info.ModTime().UTC()
		files = append(files, fileEntry{slug: slug, modTime: mt.UnixNano(), iso: mt.Format("2006-01-02T15:04:05Z")})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime > files[j].modTime
	})
	if n > 0 && len(files) > n {
		files = files[:n]
	}

	out := make([]RecentPageInfo, len(files))
	for i, f := range files {
		out[i] = RecentPageInfo{Slug: f.slug, Title: TitleFromSlug(f.slug), Modified: f.iso}
	}
	return out, nil
}

// SuggestParents ranks pages that would be sensible parents (link sources) for
// a new page with the given title and optional topic keywords. It never loads
// full page bodies for non-candidates: relevance comes from the FTS index and
// structure from the link graph.
func (s *PageStore) SuggestParents(title string, keywords []string, max int) ([]ParentSuggestion, error) {
	if max <= 0 {
		max = 5
	}
	graph, err := s.LinkGraph()
	if err != nil {
		return nil, err
	}
	indeg := computeIndegree(graph)
	outdeg := make(map[string]int, len(graph))
	for slug, links := range graph {
		outdeg[slug] = len(links)
	}

	selfSlug := SlugFromTitle(title)

	// Relevance: run the title and each keyword through FTS; earlier results
	// score higher. Accumulate per candidate slug across all queries.
	rel := map[string]float64{}
	queries := append([]string{title}, keywords...)
	for _, q := range queries {
		if strings.TrimSpace(q) == "" {
			continue
		}
		results, err := s.Search(KindPage, q)
		if err != nil {
			continue
		}
		n := len(results)
		for i, r := range results {
			rel[r.Slug] += float64(n - i)
		}
	}

	// Candidate set = relevance matches ∪ top hub pages (so we always offer a
	// good index page even when nothing matches the topic yet).
	cand := map[string]bool{}
	for slug := range rel {
		cand[slug] = true
	}
	hubs := make([]string, 0, len(outdeg))
	for slug := range outdeg {
		hubs = append(hubs, slug)
	}
	sort.Slice(hubs, func(i, j int) bool {
		if outdeg[hubs[i]] != outdeg[hubs[j]] {
			return outdeg[hubs[i]] > outdeg[hubs[j]]
		}
		return hubs[i] < hubs[j]
	})
	for i, slug := range hubs {
		if i >= 5 || outdeg[slug] == 0 {
			break
		}
		cand[slug] = true
	}
	delete(cand, selfSlug)

	type scored struct {
		slug  string
		score float64
	}
	list := make([]scored, 0, len(cand))
	for slug := range cand {
		// Weight relevance more than raw fan-out, but let hub pages surface.
		score := rel[slug]*3 + float64(outdeg[slug])
		list = append(list, scored{slug, score})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		return list[i].slug < list[j].slug
	})
	if len(list) > max {
		list = list[:max]
	}

	out := make([]ParentSuggestion, 0, len(list))
	for _, sc := range list {
		sug := ParentSuggestion{
			Slug:      sc.slug,
			Title:     TitleFromSlug(sc.slug),
			OutLinks:  outdeg[sc.slug],
			BackLinks: indeg[sc.slug],
			Score:     sc.score,
			Reason:    suggestReason(rel[sc.slug] > 0, outdeg[sc.slug]),
		}
		if page, err := s.Load(KindPage, sc.slug); err == nil {
			sug.LinkSections = sectionsWithLinks(page.Content)
			sample := ExtractWikiLinks(page.Content)
			if len(sample) > 3 {
				sample = sample[:3]
			}
			sug.SampleLinks = sample
		}
		out = append(out, sug)
	}
	return out, nil
}

func suggestReason(matchedTopic bool, outLinks int) string {
	switch {
	case matchedTopic && outLinks > 0:
		return fmt.Sprintf("matches the topic and is a hub (%d outgoing links)", outLinks)
	case matchedTopic:
		return "matches the topic"
	case outLinks > 0:
		return fmt.Sprintf("hub page (%d outgoing links)", outLinks)
	default:
		return "candidate page"
	}
}

// ── Pure link-graph helpers (operate on a slug → out-links map) ─────────────

// computeIndegree returns the number of incoming links for every page in the
// graph (pages with no backlinks map to 0).
func computeIndegree(graph map[string][]string) map[string]int {
	indeg := make(map[string]int, len(graph))
	for slug := range graph {
		indeg[slug] = 0
	}
	for _, links := range graph {
		for _, l := range links {
			indeg[l]++ // targets may include not-yet-created pages
		}
	}
	return indeg
}

// orphanSlugs returns pages that no other page links to, sorted alphabetically.
func orphanSlugs(graph map[string][]string) []string {
	indeg := computeIndegree(graph)
	var out []string
	for slug := range graph {
		if indeg[slug] == 0 {
			out = append(out, slug)
		}
	}
	sort.Strings(out)
	return out
}

// neighborhood returns the subgraph of pages within `depth` link hops of start
// (following links in either direction), as a slug → out-links map.
func neighborhood(graph map[string][]string, start string, depth int) map[string][]string {
	if depth < 1 {
		depth = 1
	}
	adj := map[string]map[string]bool{}
	link := func(a, b string) {
		if adj[a] == nil {
			adj[a] = map[string]bool{}
		}
		adj[a][b] = true
	}
	for slug, links := range graph {
		for _, l := range links {
			link(slug, l)
			link(l, slug)
		}
	}

	visited := map[string]bool{start: true}
	frontier := []string{start}
	for d := 0; d < depth; d++ {
		var next []string
		for _, node := range frontier {
			for nb := range adj[node] {
				if !visited[nb] {
					visited[nb] = true
					next = append(next, nb)
				}
			}
		}
		frontier = next
	}

	out := map[string][]string{}
	for slug := range visited {
		links := graph[slug]
		if links == nil {
			links = []string{}
		}
		out[slug] = links
	}
	return out
}

// renderLinkTree renders an indented text tree by walking out-links from the
// given roots. Already-visited pages are shown with a "↑" marker instead of
// being expanded again, so cycles terminate. Pages not reachable from any root
// are listed at the end.
func renderLinkTree(graph map[string][]string, roots []string) string {
	var sb strings.Builder
	visited := map[string]bool{}

	var walk func(slug string, depth int)
	walk = func(slug string, depth int) {
		indent := strings.Repeat("  ", depth)
		if visited[slug] {
			fmt.Fprintf(&sb, "%s- %s ↑\n", indent, slug)
			return
		}
		visited[slug] = true
		fmt.Fprintf(&sb, "%s- %s\n", indent, slug)
		children := append([]string{}, graph[slug]...)
		sort.Strings(children)
		for _, c := range children {
			if _, ok := graph[c]; !ok {
				continue // link target not a real page — skip
			}
			walk(c, depth+1)
		}
	}

	for _, r := range roots {
		if _, ok := graph[r]; ok {
			walk(r, 0)
		}
	}

	var rest []string
	for slug := range graph {
		if !visited[slug] {
			rest = append(rest, slug)
		}
	}
	sort.Strings(rest)
	if len(rest) > 0 {
		sb.WriteString("\nNot reachable from roots:\n")
		for _, s := range rest {
			fmt.Fprintf(&sb, "- %s\n", s)
		}
	}
	return sb.String()
}

// sectionsWithLinks returns the headings of sections whose body already
// contains at least one [[wiki link]] — the natural places to add a child link.
func sectionsWithLinks(content string) []string {
	sections := ParseSections(content)
	var out []string
	for _, sec := range sections {
		if sec.Heading == "" {
			continue
		}
		if wikiLinkPattern.MatchString(sec.Body) {
			out = append(out, strings.Repeat("#", sec.Level)+" "+sec.Heading)
		}
	}
	return out
}
