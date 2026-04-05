package wiki

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

func (s *PageStore) Search(kind DocKind, query string) ([]SearchResult, error) {
	// Use FTS5 when a database is available.
	if s.db != nil {
		return s.searchFTS(kind, query)
	}
	return s.searchFilesystem(kind, query)
}

// searchFTS delegates to the FTS5 index in SQLite.
func (s *PageStore) searchFTS(kind DocKind, query string) ([]SearchResult, error) {
	var ftsResults []FTSSearchResult
	var err error
	if kind == KindSkill {
		ftsResults, err = s.db.SearchFTSSkills(query)
	} else {
		ftsResults, err = s.db.SearchFTS(query)
	}
	if err != nil {
		// Fallback to filesystem scan on FTS error.
		log.Printf("fts: %s search failed, falling back to filesystem: %v", kind.Label(), err)
		return s.searchFilesystem(kind, query)
	}
	results := make([]SearchResult, len(ftsResults))
	for i, r := range ftsResults {
		excerpt := ""
		if len(r.Snippets) > 0 {
			excerpt = r.Snippets[0]
		}
		results[i] = SearchResult{
			Slug:     r.Slug,
			Title:    r.Title,
			Excerpt:  excerpt,
			Snippets: r.Snippets,
		}
	}
	return results, nil
}

// searchFilesystem is the legacy brute-force search across all document files.
func (s *PageStore) searchFilesystem(kind DocKind, query string) ([]SearchResult, error) {
	terms := splitSearchTerms(query)
	if len(terms) == 0 {
		return []SearchResult{}, nil
	}

	dir := s.docDir(kind)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []SearchResult{}, nil
		}
		return nil, err
	}

	type scored struct {
		result SearchResult
		score  int
	}
	var results []scored

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		slug := SlugFromFilename(entry.Name())
		if strings.HasPrefix(slug, "_") {
			continue
		}
		title := TitleFromSlug(slug)

		contentBytes, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		content := string(contentBytes)

		score := scoreMatch(title, content, terms)
		if score == 0 {
			continue
		}

		excerpt := excerptForTerms(content, terms)
		results = append(results, scored{
			result: SearchResult{Slug: slug, Title: title, Excerpt: excerpt},
			score:  score,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		return strings.ToLower(results[i].result.Title) < strings.ToLower(results[j].result.Title)
	})

	out := make([]SearchResult, len(results))
	for i, r := range results {
		out[i] = r.result
	}
	return out, nil
}

// splitSearchTerms extracts lowercase search terms from a query,
// splitting on whitespace and punctuation so that e.g. "tokens & lösen"
// becomes ["tokens", "lösen"].
func splitSearchTerms(query string) []string {
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, query)
	words := strings.Fields(strings.ToLower(cleaned))
	seen := make(map[string]bool)
	var terms []string
	for _, w := range words {
		if !seen[w] {
			seen[w] = true
			terms = append(terms, w)
		}
	}
	return terms
}

// scoreMatch returns a relevance score for a page against the search terms.
// Returns 0 if no terms match. Title matches score higher than content matches,
// and matching all terms gives a bonus multiplier.
func scoreMatch(title, content string, terms []string) int {
	lowerTitle := strings.ToLower(title)
	lowerContent := strings.ToLower(content)
	titleWords := strings.Fields(lowerTitle)

	score := 0
	matchedTerms := 0

	for _, term := range terms {
		termScore := 0

		// Check title words for exact or prefix match.
		for _, tw := range titleWords {
			if tw == term {
				termScore = 20
				break
			} else if strings.HasPrefix(tw, term) && termScore < 15 {
				termScore = 15
			}
		}
		// Substring match anywhere in title.
		if termScore == 0 && strings.Contains(lowerTitle, term) {
			termScore = 10
		}
		// Content match.
		if termScore == 0 && strings.Contains(lowerContent, term) {
			termScore = 3
		}

		if termScore > 0 {
			matchedTerms++
			score += termScore
		}
	}

	if matchedTerms == 0 {
		return 0
	}

	// Bonus for matching all terms.
	if matchedTerms == len(terms) {
		score *= 2
	}

	return score
}

func excerptForTerms(content string, terms []string) string {
	replaced := strings.ReplaceAll(content, "\n", " ")
	clean := strings.TrimSpace(replaced)
	if clean == "" {
		return "(no content)"
	}

	// Work with runes to avoid slicing mid-character on multi-byte UTF-8.
	runes := []rune(clean)
	lowerRunes := []rune(strings.ToLower(clean))

	// Find the earliest matching term in the content for the excerpt window.
	bestIdx := -1
	bestLen := 0
	for _, term := range terms {
		termRunes := []rune(term)
		idx := runeIndex(lowerRunes, termRunes)
		if idx >= 0 && (bestIdx < 0 || idx < bestIdx) {
			bestIdx = idx
			bestLen = len(termRunes)
		}
	}

	if bestIdx < 0 {
		if len(runes) > 180 {
			return string(runes[:180]) + "..."
		}
		return clean
	}

	start := bestIdx - 70
	if start < 0 {
		start = 0
	}
	end := bestIdx + bestLen + 70
	if end > len(runes) {
		end = len(runes)
	}
	fragment := string(runes[start:end])
	if start > 0 {
		fragment = "..." + fragment
	}
	if end < len(runes) {
		fragment += "..."
	}
	return fragment
}

// runeIndex returns the index of the first occurrence of needle in haystack, or -1.
func runeIndex(haystack, needle []rune) int {
	if len(needle) == 0 {
		return 0
	}
	if len(needle) > len(haystack) {
		return -1
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
