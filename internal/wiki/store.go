package wiki

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var ErrPageNotFound = errors.New("page not found")

type PageStore struct {
	pagesDir string
}

func NewPageStore(pagesDir string) *PageStore {
	return &PageStore{pagesDir: pagesDir}
}

func (s *PageStore) Load(slug string) (*Page, error) {
	fullPath := filepath.Join(s.pagesDir, MarkdownFilename(slug))
	content, err := os.ReadFile(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrPageNotFound
		}
		return nil, err
	}

	return &Page{
		Slug:    slug,
		Title:   TitleFromSlug(slug),
		Content: string(content),
	}, nil
}

func (s *PageStore) Save(slug, content string) error {
	fullPath := filepath.Join(s.pagesDir, MarkdownFilename(slug))
	return os.WriteFile(fullPath, []byte(content), 0o644)
}

func (s *PageStore) List() ([]PageLink, error) {
	entries, err := os.ReadDir(s.pagesDir)
	if err != nil {
		return nil, err
	}

	links := make([]PageLink, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		slug := SlugFromFilename(entry.Name())
		links = append(links, PageLink{Slug: slug, Title: TitleFromSlug(slug)})
	}

	sort.Slice(links, func(i, j int) bool {
		return strings.ToLower(links[i].Title) < strings.ToLower(links[j].Title)
	})

	return links, nil
}

func (s *PageStore) Search(query string) ([]SearchResult, error) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return []SearchResult{}, nil
	}
	lowerQuery := strings.ToLower(trimmed)

	entries, err := os.ReadDir(s.pagesDir)
	if err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		slug := SlugFromFilename(entry.Name())
		title := TitleFromSlug(slug)

		contentBytes, err := os.ReadFile(filepath.Join(s.pagesDir, entry.Name()))
		if err != nil {
			continue
		}
		content := string(contentBytes)
		target := strings.ToLower(title + "\n" + content)
		if !strings.Contains(target, lowerQuery) {
			continue
		}

		excerpt := excerptForQuery(content, lowerQuery)
		results = append(results, SearchResult{Slug: slug, Title: title, Excerpt: excerpt})
	}

	sort.Slice(results, func(i, j int) bool {
		return strings.ToLower(results[i].Title) < strings.ToLower(results[j].Title)
	})

	return results, nil
}

func excerptForQuery(content, query string) string {
	replaced := strings.ReplaceAll(content, "\n", " ")
	clean := strings.TrimSpace(replaced)
	if clean == "" {
		return "(no content)"
	}
	lower := strings.ToLower(clean)
	idx := strings.Index(lower, query)
	if idx < 0 {
		if len(clean) > 180 {
			return clean[:180] + "..."
		}
		return clean
	}

	start := idx - 70
	if start < 0 {
		start = 0
	}
	end := idx + len(query) + 70
	if end > len(clean) {
		end = len(clean)
	}
	fragment := clean[start:end]
	if start > 0 {
		fragment = "..." + fragment
	}
	if end < len(clean) {
		fragment += "..."
	}
	return fragment
}
