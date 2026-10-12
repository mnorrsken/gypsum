package wiki

import (
	"context"
	"fmt"
	"image"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// scanTimeout bounds one scan, including the model call.
const scanTimeout = 6 * time.Minute

// scanMaxLinkPages caps how many page titles are offered to the model for
// [[links]]; larger wikis offer the most recently edited ones.
const scanMaxLinkPages = 1500

// scanMaxRewriteChars caps the page size sent to the model for a rewrite.
// Longer pages only get their headings and the scan is appended.
const scanMaxRewriteChars = 20000

// scanJobTTL is how long finished scan jobs stay visible.
const scanJobTTL = time.Hour

// Scan job states.
const (
	scanRunning = "running"
	scanDone    = "done"
	scanFailed  = "failed"
)

// ScanJob is one photo being turned into wiki content.
type ScanJob struct {
	ID        string
	Status    string // running, done or failed
	Step      string // what is happening now, for the progress view
	Error     string
	Target    string // page the scan was asked to extend; "" = new page
	Slug      string // page created or extended
	Title     string
	Appended  bool // true when added to an existing page
	Rewritten bool // true when the existing page was rewritten around the scan
	Figures   int  // figures cut out and stored
	Created   time.Time
}

// Scanner turns photos into wiki pages: the analyzer reads the photo, figures
// are cut out and stored as images, and the content is saved as a new page or
// appended to an existing one. Jobs run in the background and live in memory.
type Scanner struct {
	store      *PageStore
	autoCommit *GitAutoCommitter
	analyzer   ScanAnalyzer
	uploadCfg  UploadConfig

	mu        sync.Mutex
	jobs      map[string]*ScanJob
	pageLocks map[string]*sync.Mutex // serializes scans into the same page
}

// NewScanner creates a scanner that stores results through store.
func NewScanner(store *PageStore, autoCommit *GitAutoCommitter, analyzer ScanAnalyzer, cfg UploadConfig) *Scanner {
	return &Scanner{store: store, autoCommit: autoCommit, analyzer: analyzer, uploadCfg: cfg,
		jobs: make(map[string]*ScanJob), pageLocks: make(map[string]*sync.Mutex)}
}

// Start begins scanning photo in the background and returns the job ID.
// targetSlug is an existing page to append to, or "" for a new page.
func (s *Scanner) Start(photo []byte, targetSlug, note, author string) string {
	job := &ScanJob{ID: oauthGenerateToken()[:16], Status: scanRunning, Step: "Reading the photo", Target: targetSlug, Created: time.Now()}
	s.mu.Lock()
	for id, j := range s.jobs {
		if time.Since(j.Created) > scanJobTTL {
			delete(s.jobs, id)
		}
	}
	s.jobs[job.ID] = job
	s.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
		defer cancel()
		if err := s.run(ctx, job.ID, photo, targetSlug, note, author); err != nil {
			log.Printf("scan %s: %v", job.ID, err)
			s.update(job.ID, func(j *ScanJob) { j.Status, j.Error = scanFailed, err.Error() })
		}
	}()
	return job.ID
}

// Job returns a copy of a job's current state.
func (s *Scanner) Job(id string) (ScanJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return ScanJob{}, false
	}
	return *j, true
}

func (s *Scanner) update(id string, fn func(*ScanJob)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		fn(j)
	}
}

func (s *Scanner) step(id, step string) {
	s.update(id, func(j *ScanJob) { j.Step = step })
}

// lockPage holds a page for the whole of a scan into it, so a second scan
// of the same page waits and then sees the first one's result.
func (s *Scanner) lockPage(slug string) func() {
	s.mu.Lock()
	l, ok := s.pageLocks[slug]
	if !ok {
		l = &sync.Mutex{}
		s.pageLocks[slug] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func (s *Scanner) run(ctx context.Context, id string, photo []byte, targetSlug, note, author string) error {
	photoImg, err := prepareScanImage(photo)
	if err != nil {
		return err
	}

	req := ScanRequest{Width: photoImg.sent.Bounds().Dx(), Height: photoImg.sent.Bounds().Dy(), Note: note}
	var original, header string // target page content and its "# Title" line
	if targetSlug != "" {
		s.step(id, "Waiting for other scans of this page")
		defer s.lockPage(targetSlug)()
		page, err := s.store.Load(KindPage, targetSlug)
		if err != nil {
			return fmt.Errorf("page not found: %s", targetSlug)
		}
		original = page.Content
		req.AppendTitle = pageDisplayTitle(page)
		var body string
		header, body = splitTitleLine(page.Content)
		// Pages with encrypted fields are never rewritten, so the model can
		// not damage them; long pages are appended to keep the request small.
		if len(body) <= scanMaxRewriteChars && !strings.Contains(body, "{{secure") {
			req.PageBody = body
		} else {
			req.Headings = ListSectionHeadings(page.Content)
		}
	}

	existing, offered := s.linkablePages(targetSlug)
	req.Pages = offered

	s.step(id, "Analyzing the photo")
	result, err := s.analyzer.Analyze(ctx, photoImg.jpeg, req)
	if err != nil {
		return err
	}
	log.Printf("scan %s: %q, %d chars, %d figures, rewrite=%v", id, result.Title, len(result.Markdown), len(result.Figures), result.Rewrite)
	if strings.TrimSpace(result.Markdown) == "" && len(result.Figures) == 0 {
		return fmt.Errorf("Claude found nothing to add from this photo")
	}

	s.step(id, "Saving figures")
	title := strings.TrimSpace(result.Title)
	if title == "" {
		title = "Scan " + time.Now().Format("2006-01-02 15:04")
	}
	figureMarkdown := s.saveFigures(photoImg, result.Figures, title, author)
	body := placeFigures(result.Markdown, figureMarkdown)

	s.step(id, "Saving the page")
	if targetSlug != "" {
		body = keepExistingLinks(body, existing, targetSlug)
		if strings.TrimSpace(body) == "" {
			return fmt.Errorf("Claude found nothing to add from this photo")
		}
		// The page lock keeps other scans out, but not edits in the browser.
		if current, err := s.store.Load(KindPage, targetSlug); err != nil || current.Content != original {
			return fmt.Errorf("the page was edited during the scan; scan again")
		}
		rewrite := result.Rewrite && req.PageBody != ""
		if rewrite {
			body = restoreMissingRefs(req.PageBody, body)
			content := strings.TrimSpace(body) + "\n"
			if header != "" {
				content = header + "\n\n" + content
			}
			if err := s.store.Save(KindPage, targetSlug, content); err != nil {
				return fmt.Errorf("saving page %s: %w", targetSlug, err)
			}
		} else if err := s.store.AppendToPage(targetSlug, body); err != nil {
			return fmt.Errorf("adding to page %s: %w", targetSlug, err)
		}
		_ = s.autoCommit.CommitSave(KindPage, targetSlug, author)
		s.update(id, func(j *ScanJob) {
			j.Status, j.Slug, j.Title, j.Appended, j.Rewritten, j.Figures = scanDone, targetSlug, req.AppendTitle, true, rewrite, len(figureMarkdown)
		})
		return nil
	}

	title, slug := s.freeTitle(title)
	body = keepExistingLinks(body, existing, slug)
	if err := s.store.Save(KindPage, slug, "# "+title+"\n\n"+strings.TrimSpace(body)+"\n"); err != nil {
		return fmt.Errorf("saving page %s: %w", slug, err)
	}
	_ = s.autoCommit.CommitSave(KindPage, slug, author)
	addWikiLink(s.store, s.autoCommit, s.parentFor(title, slug, result.Keywords), title, "", author)
	s.update(id, func(j *ScanJob) {
		j.Status, j.Slug, j.Title, j.Figures = scanDone, slug, title, len(figureMarkdown)
	})
	return nil
}

// saveFigures crops, restyles and stores each figure, returning the Markdown
// that embeds it, keyed by figure id. Figures that fail are skipped.
func (s *Scanner) saveFigures(photo *scanPhoto, figures []ScanFigure, title, author string) map[int]string {
	out := make(map[int]string)
	base := sanitizeImageBasename(title)
	for _, f := range figures {
		crop := photo.cropFigure(image.Rect(f.X0, f.Y0, f.X1, f.Y1))
		if crop == nil {
			log.Printf("scan: figure %d box (%d,%d)-(%d,%d) is outside the image or too small", f.ID, f.X0, f.Y0, f.X1, f.Y1)
			continue
		}
		data, ext, err := renderFigure(crop, f.Style, f.Colors)
		if err != nil {
			log.Printf("scan: figure %d: %v", f.ID, err)
			continue
		}
		sf, err := s.store.SaveUpload(fmt.Sprintf("%s-fig%d%s", base, f.ID, ext), data, s.uploadCfg, f.Caption)
		if err != nil {
			log.Printf("scan: saving figure %d: %v", f.ID, err)
			continue
		}
		_ = s.autoCommit.CommitImageSave(sf.Name, author)
		out[f.ID] = sf.Markdown
	}
	return out
}

var (
	figureMarker = regexp.MustCompile(`(?m)^[ \t]*%%FIGURE (\d+)%%[ \t]*$`)
	blankRuns    = regexp.MustCompile(`\n{3,}`)
)

// placeFigures replaces %%FIGURE n%% lines with the stored figures. Markers
// for missing figures are dropped; figures the text never placed are added at
// the end.
func placeFigures(markdown string, figures map[int]string) string {
	used := make(map[int]bool)
	out := figureMarker.ReplaceAllStringFunc(markdown, func(m string) string {
		id, _ := strconv.Atoi(figureMarker.FindStringSubmatch(m)[1])
		if md, ok := figures[id]; ok && !used[id] {
			used[id] = true
			return md
		}
		return ""
	})
	ids := make([]int, 0, len(figures))
	for id := range figures {
		if !used[id] {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	for _, id := range ids {
		out = strings.TrimRight(out, "\n") + "\n\n" + figures[id]
	}
	return strings.TrimSpace(blankRuns.ReplaceAllString(out, "\n\n"))
}

// freeTitle returns title, or "title 2", "title 3"… when a page with that
// slug already exists.
func (s *Scanner) freeTitle(title string) (string, string) {
	candidate := title
	for n := 2; ; n++ {
		slug := SlugFromTitle(candidate)
		if _, err := s.store.Load(KindPage, slug); err != nil {
			return candidate, slug
		}
		candidate = fmt.Sprintf("%s %d", title, n)
	}
}

// parentFor picks the page to link a new scan from: the best parent
// suggestion, or Home.
func (s *Scanner) parentFor(title, slug string, keywords []string) string {
	suggestions, err := s.store.SuggestParents(title, keywords, 3)
	if err == nil {
		for _, sg := range suggestions {
			if sg.Slug != slug {
				return sg.Slug
			}
		}
	}
	return "Home"
}

// pageDisplayTitle is the page's level-1 heading, or its slug-derived title.
func pageDisplayTitle(p *Page) string {
	for _, line := range strings.Split(p.Content, "\n") {
		if t, ok := strings.CutPrefix(strings.TrimSpace(line), "# "); ok {
			return strings.TrimSpace(t)
		}
		if strings.TrimSpace(line) != "" {
			break
		}
	}
	return p.Title
}

// linkablePages returns the slugs of all pages and the titles offered to the
// model for [[links]], leaving out the page being extended.
func (s *Scanner) linkablePages(targetSlug string) (map[string]bool, []string) {
	pages, err := s.store.List(KindPage)
	if err != nil {
		log.Printf("scan: listing pages: %v", err)
		return nil, nil
	}
	existing := make(map[string]bool, len(pages))
	for _, p := range pages {
		existing[p.Slug] = true
	}
	if len(pages) > scanMaxLinkPages {
		if recent, err := s.store.RecentPages(scanMaxLinkPages); err == nil {
			pages = recent
		}
	}
	titles := make([]string, 0, len(pages))
	for _, p := range pages {
		if p.Slug != targetSlug {
			titles = append(titles, p.Title)
		}
	}
	return existing, titles
}

// keepExistingLinks turns [[links]] to pages that do not exist, or to the
// page itself, back into plain text. Code blocks and Mermaid are left alone.
func keepExistingLinks(md string, existing map[string]bool, self string) string {
	return applyOutsideCode(md, func(s string) string {
		return wikiLinkPattern.ReplaceAllStringFunc(s, func(m string) string {
			c := wikiLinkPattern.FindStringSubmatch(m)
			if c[1] == `\` {
				return m
			}
			title := strings.TrimSpace(c[2])
			if slug := SlugFromTitle(title); existing[slug] && slug != self {
				return m
			}
			return title
		})
	})
}

// splitTitleLine separates a leading "# Title" line from the rest of a page.
func splitTitleLine(content string) (header, body string) {
	trimmed := strings.TrimLeft(content, "\n")
	first, rest, _ := strings.Cut(trimmed, "\n")
	if strings.HasPrefix(first, "# ") {
		return first, strings.TrimSpace(rest)
	}
	return "", strings.TrimSpace(content)
}

// uploadRefPattern matches a Markdown image or link to an uploaded file.
var uploadRefPattern = regexp.MustCompile(`!?\[[^\]\n]*\]\((/(?:images|files)/[^)\s]+)\)`)

// restoreMissingRefs appends, unchanged, every image or file reference of the
// old page that the rewritten page dropped, so a rewrite never loses one.
func restoreMissingRefs(oldBody, newBody string) string {
	for _, m := range uploadRefPattern.FindAllStringSubmatch(oldBody, -1) {
		if !strings.Contains(newBody, m[1]) {
			log.Printf("scan: rewrite dropped %s; adding it back", m[1])
			newBody = strings.TrimRight(newBody, "\n") + "\n\n" + m[0]
		}
	}
	return newBody
}
