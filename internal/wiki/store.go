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
	pagesDir  string
	imagesDir string
}

func NewPageStore(pagesDir string) *PageStore {
	imagesDir := filepath.Join(filepath.Dir(pagesDir), "images")
	_ = os.MkdirAll(imagesDir, 0o755)
	return &PageStore{pagesDir: pagesDir, imagesDir: imagesDir}
}

func (s *PageStore) ImagesDir() string {
	return s.imagesDir
}

func (s *PageStore) PagePath(slug string) string {
	return filepath.Join(s.pagesDir, MarkdownFilename(slug))
}

func (s *PageStore) ImagePath(filename string) string {
	return filepath.Join(s.imagesDir, filename)
}

func (s *PageStore) ListImages() ([]ImageInfo, error) {
	entries, err := os.ReadDir(s.imagesDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	// Build a map of image filename -> list of pages referencing it
	usageMap := s.findImageUsage()

	var images []ImageInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".gif" && ext != ".webp" && ext != ".svg" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		images = append(images, ImageInfo{
			Name:    entry.Name(),
			URL:     "/images/" + entry.Name(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			UsedBy:  usageMap[entry.Name()],
		})
	}

	sort.Slice(images, func(i, j int) bool {
		return images[i].ModTime.After(images[j].ModTime)
	})

	return images, nil
}

func (s *PageStore) findImageUsage() map[string][]string {
	usage := make(map[string][]string)

	entries, err := os.ReadDir(s.pagesDir)
	if err != nil {
		return usage
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(s.pagesDir, entry.Name()))
		if err != nil {
			continue
		}
		slug := SlugFromFilename(entry.Name())
		text := string(content)

		// Find all /images/FILENAME references
		imgEntries, _ := os.ReadDir(s.imagesDir)
		for _, img := range imgEntries {
			if strings.Contains(text, "/images/"+img.Name()) {
				usage[img.Name()] = append(usage[img.Name()], slug)
			}
		}
	}

	return usage
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

// ExtractWikiLinks returns all [[Page Title]] link targets found in content as slugs.
func ExtractWikiLinks(content string) []string {
	matches := wikiLinkPattern.FindAllStringSubmatch(content, -1)
	seen := make(map[string]bool)
	var slugs []string
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		slug := SlugFromTitle(strings.TrimSpace(m[1]))
		if !seen[slug] {
			seen[slug] = true
			slugs = append(slugs, slug)
		}
	}
	return slugs
}

// LinkGraph builds a map of page slug → list of slugs it links to via [[wiki links]].
func (s *PageStore) LinkGraph() (map[string][]string, error) {
	entries, err := os.ReadDir(s.pagesDir)
	if err != nil {
		return nil, err
	}

	graph := make(map[string][]string)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		slug := SlugFromFilename(entry.Name())
		if strings.HasPrefix(slug, "_") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(s.pagesDir, entry.Name()))
		if err != nil {
			continue
		}
		links := ExtractWikiLinks(string(content))
		graph[slug] = links
	}
	return graph, nil
}

// BackLinks returns all pages that link to the given slug via [[wiki links]].
func (s *PageStore) BackLinks(targetSlug string) ([]PageLink, error) {
	graph, err := s.LinkGraph()
	if err != nil {
		return nil, err
	}

	var results []PageLink
	for slug, links := range graph {
		for _, link := range links {
			if link == targetSlug {
				results = append(results, PageLink{Slug: slug, Title: TitleFromSlug(slug)})
				break
			}
		}
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
