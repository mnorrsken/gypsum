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
		if strings.HasPrefix(slug, "_") {
			continue // skip special files like _favorites
		}
		links = append(links, PageLink{Slug: slug, Title: TitleFromSlug(slug)})
	}

	sort.Slice(links, func(i, j int) bool {
		return strings.ToLower(links[i].Title) < strings.ToLower(links[j].Title)
	})

	return links, nil
}

// RecentPages returns the n most recently modified pages.
func (s *PageStore) RecentPages(n int) ([]PageLink, error) {
	entries, err := os.ReadDir(s.pagesDir)
	if err != nil {
		return nil, err
	}

	type fileEntry struct {
		slug    string
		modTime int64
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
		files = append(files, fileEntry{slug: slug, modTime: info.ModTime().UnixNano()})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime > files[j].modTime
	})

	if len(files) > n {
		files = files[:n]
	}

	links := make([]PageLink, len(files))
	for i, f := range files {
		links[i] = PageLink{Slug: f.slug, Title: TitleFromSlug(f.slug)}
	}
	return links, nil
}

// LoadFavorites reads the _favorites.md file and returns wiki links as PageLinks.
func (s *PageStore) LoadFavorites() ([]PageLink, error) {
	content, err := os.ReadFile(filepath.Join(s.pagesDir, "_favorites.md"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var favs []PageLink
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Support "- [[Page Title]]" and "[[Page Title]]" and plain "Page Title"
		line = strings.TrimPrefix(line, "-")
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "[[")
		line = strings.TrimSuffix(line, "]]")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		slug := SlugFromTitle(line)
		favs = append(favs, PageLink{Slug: slug, Title: line})
	}
	return favs, nil
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
		if strings.HasPrefix(slug, "_") {
			continue
		}
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
