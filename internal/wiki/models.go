package wiki

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// DocKind distinguishes document types (pages vs skills).
type DocKind string

const (
	KindPage  DocKind = "pages"
	KindSkill DocKind = "skills"
)

// Label returns a human-readable singular name ("page" or "skill").
func (k DocKind) Label() string {
	if k == KindSkill {
		return "skill"
	}
	return "page"
}

type Page struct {
	Slug    string
	Title   string
	Content string
}

type PageLink struct {
	Slug  string
	Title string
}

type SearchResult struct {
	Slug     string
	Title    string
	Excerpt  string   // primary excerpt (first snippet or legacy fallback)
	Snippets []string // all context snippets around matched terms
}

// SkillListEntry is returned by list_skills with extracted tags.
type SkillListEntry struct {
	Slug  string   `json:"slug"`
	Title string   `json:"title"`
	Tags  []string `json:"tags"`
}

var (
	nonSlugChar = regexp.MustCompile(`[^\p{L}\p{N}_\-]+`)
	tagsLineRe  = regexp.MustCompile(`(?im)^tags:\s*(.+)$`)
)

func SlugFromTitle(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "Untitled"
	}
	replaced := strings.ReplaceAll(trimmed, " ", "_")
	clean := nonSlugChar.ReplaceAllString(replaced, "")
	if clean == "" {
		return "Untitled"
	}
	return clean
}

func TitleFromSlug(slug string) string {
	if slug == "" {
		return "Untitled"
	}
	return strings.ReplaceAll(slug, "_", " ")
}

func MarkdownFilename(slug string) string {
	return fmt.Sprintf("%s.md", slug)
}

func SlugFromFilename(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// ExtractTags parses a "Tags: foo, bar, baz" line from content and returns
// lowercased, trimmed tags. Returns nil if no tags line is found.
func ExtractTags(content string) []string {
	m := tagsLineRe.FindStringSubmatch(content)
	if m == nil {
		return nil
	}
	parts := strings.Split(m[1], ",")
	var tags []string
	for _, p := range parts {
		t := strings.TrimSpace(strings.ToLower(p))
		if t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}
