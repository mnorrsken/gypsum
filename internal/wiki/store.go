package wiki

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var ErrPageNotFound = errors.New("page not found")

type PageStore struct {
	pagesDir  string
	imagesDir string
	skillsDir string
	db        *DB // optional; enables FTS5 search when non-nil
}

// docDir returns the filesystem directory for the given document kind.
func (s *PageStore) docDir(kind DocKind) string {
	if kind == KindSkill {
		return s.skillsDir
	}
	return s.pagesDir
}

func NewPageStore(pagesDir string) *PageStore {
	parent := filepath.Dir(pagesDir)
	imagesDir := filepath.Join(parent, "images")
	skillsDir := filepath.Join(parent, "skills")
	_ = os.MkdirAll(imagesDir, 0o755)
	_ = os.MkdirAll(skillsDir, 0o755)
	return &PageStore{pagesDir: pagesDir, imagesDir: imagesDir, skillsDir: skillsDir}
}

// SetDB attaches a database for FTS5 full-text search and triggers a full reindex.
func (s *PageStore) SetDB(db *DB) {
	s.db = db
	if db != nil {
		s.reindex(KindPage)
		s.reindex(KindSkill)
	}
}

// ReindexChanged reindexes only the given slugs for the specified kind, or
// does a full reindex if changedSlugs is nil (meaning the diff could not be
// determined). An empty slice means nothing changed.
func (s *PageStore) ReindexChanged(kind DocKind, changedSlugs []string) {
	if s.db == nil {
		return
	}
	if changedSlugs == nil {
		s.reindex(kind)
		return
	}
	if len(changedSlugs) == 0 {
		return
	}
	dir := s.docDir(kind)
	for _, slug := range changedSlugs {
		path := filepath.Join(dir, MarkdownFilename(slug))
		content, err := os.ReadFile(path)
		if err != nil {
			// File was deleted.
			if kind == KindSkill {
				_ = s.db.RemoveSkill(slug)
			} else {
				_ = s.db.RemovePage(slug)
			}
			continue
		}
		if kind == KindSkill {
			tags := strings.Join(ExtractTags(string(content)), " ")
			_ = s.db.IndexSkill(slug, TitleFromSlug(slug), tags, string(content))
		} else {
			_ = s.db.IndexPage(slug, TitleFromSlug(slug), string(content))
		}
	}
	log.Printf("fts: reindexed %d changed %s(s)", len(changedSlugs), kind.Label())
}

// reindex rebuilds the FTS index for the given kind from all markdown files on disk.
func (s *PageStore) reindex(kind DocKind) {
	dir := s.docDir(kind)
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("fts: reindex %s failed to read dir: %v", kind, err)
		return
	}
	if kind == KindSkill {
		var skills []FTSSkillEntry
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
				continue
			}
			slug := SlugFromFilename(entry.Name())
			if strings.HasPrefix(slug, "_") {
				continue
			}
			content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				continue
			}
			tags := strings.Join(ExtractTags(string(content)), " ")
			skills = append(skills, FTSSkillEntry{
				Slug:    slug,
				Title:   TitleFromSlug(slug),
				Tags:    tags,
				Content: string(content),
			})
		}
		if err := s.db.ReindexSkills(skills); err != nil {
			log.Printf("fts: reindex skills failed: %v", err)
		} else {
			log.Printf("fts: indexed %d skills", len(skills))
		}
	} else {
		var pages []Page
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
				continue
			}
			slug := SlugFromFilename(entry.Name())
			if strings.HasPrefix(slug, "_") {
				continue
			}
			content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				continue
			}
			pages = append(pages, Page{
				Slug:    slug,
				Title:   TitleFromSlug(slug),
				Content: string(content),
			})
		}
		if err := s.db.ReindexPages(pages); err != nil {
			log.Printf("fts: reindex failed: %v", err)
		} else {
			log.Printf("fts: indexed %d pages", len(pages))
		}
	}
}

func (s *PageStore) ImagesDir() string {
	return s.imagesDir
}

func (s *PageStore) DocPath(kind DocKind, slug string) string {
	return filepath.Join(s.docDir(kind), MarkdownFilename(slug))
}

func (s *PageStore) PagePath(slug string) string {
	return s.DocPath(KindPage, slug)
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

	// Read image filenames once.
	imgEntries, err := os.ReadDir(s.imagesDir)
	if err != nil {
		return usage
	}
	imageNames := make([]string, 0, len(imgEntries))
	for _, img := range imgEntries {
		if !img.IsDir() {
			imageNames = append(imageNames, img.Name())
		}
	}
	if len(imageNames) == 0 {
		return usage
	}

	// Scan each page once for all image references.
	pageEntries, err := os.ReadDir(s.pagesDir)
	if err != nil {
		return usage
	}
	for _, entry := range pageEntries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(s.pagesDir, entry.Name()))
		if err != nil {
			continue
		}
		slug := SlugFromFilename(entry.Name())
		text := string(content)

		for _, imgName := range imageNames {
			if strings.Contains(text, "/images/"+imgName) {
				usage[imgName] = append(usage[imgName], slug)
			}
		}
	}

	return usage
}

func (s *PageStore) Load(kind DocKind, slug string) (*Page, error) {
	fullPath := filepath.Join(s.docDir(kind), MarkdownFilename(slug))
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

func (s *PageStore) Save(kind DocKind, slug, content string) error {
	dir := s.docDir(kind)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	fullPath := filepath.Join(dir, MarkdownFilename(slug))
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		return err
	}
	if s.db != nil {
		if kind == KindSkill {
			tags := strings.Join(ExtractTags(content), " ")
			_ = s.db.IndexSkill(slug, TitleFromSlug(slug), tags, content)
		} else {
			_ = s.db.IndexPage(slug, TitleFromSlug(slug), content)
		}
	}
	return nil
}

// Delete removes a document from disk and the FTS index.
func (s *PageStore) Delete(kind DocKind, slug string) error {
	fullPath := filepath.Join(s.docDir(kind), MarkdownFilename(slug))
	if err := os.Remove(fullPath); err != nil {
		return err
	}
	if s.db != nil {
		if kind == KindSkill {
			_ = s.db.RemoveSkill(slug)
		} else {
			_ = s.db.RemovePage(slug)
		}
	}
	return nil
}

func (s *PageStore) List(kind DocKind) ([]PageLink, error) {
	dir := s.docDir(kind)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
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
		if links == nil {
			links = []string{}
		}
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

// ListSkillEntries returns all skills with extracted tags (skill-specific).
func (s *PageStore) ListSkillEntries() ([]SkillListEntry, error) {
	entries, err := os.ReadDir(s.skillsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var skills []SkillListEntry
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		slug := SlugFromFilename(entry.Name())
		if strings.HasPrefix(slug, "_") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(s.skillsDir, entry.Name()))
		if err != nil {
			continue
		}
		tags := ExtractTags(string(content))
		if tags == nil {
			tags = []string{}
		}
		skills = append(skills, SkillListEntry{
			Slug:  slug,
			Title: TitleFromSlug(slug),
			Tags:  tags,
		})
	}

	sort.Slice(skills, func(i, j int) bool {
		return strings.ToLower(skills[i].Title) < strings.ToLower(skills[j].Title)
	})
	return skills, nil
}
