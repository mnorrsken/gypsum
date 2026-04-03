package wiki

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

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

var nonSlugChar = regexp.MustCompile(`[^\p{L}\p{N}_\-]+`)

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
