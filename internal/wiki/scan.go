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

// scanJobTTL is how long scan jobs stay visible, including a preview that
// was never saved.
const scanJobTTL = time.Hour

// Scan job states.
const (
	scanRunning = "running"
	scanReview  = "review" // read; the preview waits to be saved or discarded
	scanDone    = "done"
	scanFailed  = "failed"
)

// ScanJob is one photo being turned into wiki content.
type ScanJob struct {
	ID        string
	Status    string // running, review, done or failed
	Step      string // what is happening now, for the progress view
	Error     string
	Target    string // page the scan was asked to extend; "" = new page
	Slug      string // page created or extended
	Title     string
	Appended  bool // true when added to an existing page
	Rewritten bool // true when the existing page was rewritten around the scan
	Figures   int  // figures cut out (and stored, once saved)
	Created   time.Time

	Straightened bool   // the page was found in the photo and its perspective corrected
	Preview      []byte // JPEG of the photo with the found page outlined
	ReplacedBy   string // job that rescans the photo without straightening

	// What the job was started with, to scan the photo again.
	photo []byte
	note  string
	flat  bool // skip straightening
	stop  context.CancelFunc

	// In review: the page as it would be saved, with figures served from
	// the job, and what is needed to save it. Nothing is stored before that.
	Draft string
	draft *scanDraft

	// For a new page: where it could be linked from, and where it was linked.
	Suggestions []ParentChoice
	LinkedFrom  string
	Author      string
}

// scanDraft is a read photo that is not saved yet.
type scanDraft struct {
	title    string // Claude's title; also names the figure files
	markdown string // with %%FIGURE n%% markers
	keywords []string
	figures  map[int]scanFigure
	rewrite  bool
	original string // target page content when it was read
	header   string // its "# Title" line
	pageBody string // the body sent to Claude, for a rewrite
	existing map[string]bool
}

// scanFigure is a figure cut out of the photo and styled, not stored yet.
type scanFigure struct {
	data    []byte
	ext     string
	caption string
}

// ParentChoice is a page offered as the parent of a newly scanned page.
type ParentChoice struct {
	Slug   string
	Title  string
	Reason string
}

// Scanner turns photos into wiki pages: the analyzer reads the photo and
// figures are cut out; once the user saves the preview, the figures are
// stored as images and the content is saved as a new page or added to an
// existing one. Jobs run in the background and live in memory.
type Scanner struct {
	store      *PageStore
	autoCommit *GitAutoCommitter
	analyzer   ScanAnalyzer
	uploadCfg  UploadConfig

	mu        sync.Mutex
	jobs      map[string]*ScanJob
	pageLocks map[string]*sync.Mutex // serializes saves into the same page
}

// NewScanner creates a scanner that stores results through store.
func NewScanner(store *PageStore, autoCommit *GitAutoCommitter, analyzer ScanAnalyzer, cfg UploadConfig) *Scanner {
	return &Scanner{store: store, autoCommit: autoCommit, analyzer: analyzer, uploadCfg: cfg,
		jobs: make(map[string]*ScanJob), pageLocks: make(map[string]*sync.Mutex)}
}

// Start begins scanning photo in the background and returns the job ID.
// targetSlug is an existing page to append to, or "" for a new page.
func (s *Scanner) Start(photo []byte, targetSlug, note, author string) string {
	return s.start(photo, targetSlug, note, author, false)
}

func (s *Scanner) start(photo []byte, targetSlug, note, author string, flat bool) string {
	ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
	job := &ScanJob{ID: oauthGenerateToken()[:16], Status: scanRunning, Step: "Reading the photo", Target: targetSlug, Author: author, Created: time.Now(),
		photo: photo, note: note, flat: flat, stop: cancel}
	s.mu.Lock()
	for id, j := range s.jobs {
		if time.Since(j.Created) > scanJobTTL {
			delete(s.jobs, id)
		}
	}
	s.jobs[job.ID] = job
	s.mu.Unlock()

	go func() {
		defer cancel()
		if err := s.run(ctx, job.ID, photo, targetSlug, note, author, flat); err != nil {
			log.Printf("scan %s: %v", job.ID, err)
			s.update(job.ID, func(j *ScanJob) {
				if j.ReplacedBy == "" {
					j.Status, j.Error = scanFailed, err.Error()
				}
			})
		}
		s.update(job.ID, func(j *ScanJob) { // keep memory low once done
			if j.Status != scanReview {
				j.photo = nil
			}
		})
	}()
	return job.ID
}

// RescanFlat stops a scan that is running or waiting for review, so nothing
// is saved, and scans the same photo again without straightening, for when
// the page outline was wrong. It returns the new job's ID.
func (s *Scanner) RescanFlat(id string) (string, error) {
	s.mu.Lock()
	j, ok := s.jobs[id]
	if !ok || (j.Status != scanRunning && j.Status != scanReview) || j.photo == nil || j.ReplacedBy != "" {
		s.mu.Unlock()
		return "", fmt.Errorf("this scan can no longer be changed")
	}
	photo, target, note, author := j.photo, j.Target, j.note, j.Author
	j.Status, j.Error = scanFailed, "Stopped; the photo is scanned again without straightening."
	j.ReplacedBy = "pending" // keeps the stopped run from overwriting the message
	j.photo, j.Draft, j.draft = nil, "", nil
	j.stop()
	s.mu.Unlock()

	newID := s.start(photo, target, note, author, true)
	s.update(id, func(j *ScanJob) { j.ReplacedBy = newID })
	return newID, nil
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

func (s *Scanner) run(ctx context.Context, id string, photo []byte, targetSlug, note, author string, flat bool) error {
	photoImg, err := prepareScanImage(photo)
	if err != nil {
		return err
	}
	if finder, ok := s.analyzer.(DocumentFinder); ok && !flat {
		s.step(id, "Finding the page")
		if straight, quad := s.straightenPage(ctx, finder, photoImg); straight != nil {
			preview, err := outlinePreview(photoImg.sent, quad)
			if err != nil {
				log.Printf("scan: preview: %v", err)
			}
			photoImg = straight
			s.update(id, func(j *ScanJob) { j.Straightened, j.Preview = true, preview })
		}
	}

	req := ScanRequest{Width: photoImg.sent.Bounds().Dx(), Height: photoImg.sent.Bounds().Dy(), Note: note}
	var original, header string // target page content and its "# Title" line
	if targetSlug != "" {
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

	if ctx.Err() != nil {
		return ctx.Err() // stopped (RescanFlat) or timed out: save nothing
	}
	s.step(id, "Cutting out figures")
	title := strings.TrimSpace(result.Title)
	if title == "" {
		title = "Scan " + time.Now().Format("2006-01-02 15:04")
	}
	d := &scanDraft{title: title, markdown: result.Markdown, keywords: result.Keywords,
		figures: cutFigures(photoImg, result.Figures), rewrite: result.Rewrite && req.PageBody != "",
		original: original, header: header, pageBody: req.PageBody, existing: existing}
	links := make(map[int]string, len(d.figures))
	for fid, f := range d.figures {
		links[fid] = fmt.Sprintf("![%s](/scan/%s/figure/%d)", figureLabel(f.caption), id, fid)
	}
	preview, title, _ := s.compose(d, targetSlug, links)
	if strings.TrimSpace(preview) == "" {
		return fmt.Errorf("Claude found nothing to add from this photo")
	}
	if targetSlug != "" {
		title = req.AppendTitle
	}
	s.update(id, func(j *ScanJob) {
		if j.ReplacedBy == "" {
			j.Status, j.Title, j.Rewritten, j.Figures, j.Draft, j.draft = scanReview, title, d.rewrite, len(d.figures), preview, d
		}
	})
	return nil
}

// compose builds what a draft saves with the given figure Markdown: the whole
// page for a new page or a rewrite, or the part to append. For a new page it
// also returns the free title and slug.
func (s *Scanner) compose(d *scanDraft, targetSlug string, figures map[int]string) (content, title, slug string) {
	body := placeFigures(d.markdown, figures)
	if targetSlug != "" {
		body = keepExistingLinks(body, d.existing, targetSlug)
		if !d.rewrite {
			return strings.TrimSpace(body), "", ""
		}
		content = strings.TrimSpace(restoreMissingRefs(d.pageBody, body)) + "\n"
		if d.header != "" {
			content = d.header + "\n\n" + content
		}
		return content, "", ""
	}
	title, slug = s.freeTitle(d.title)
	body = keepExistingLinks(body, d.existing, slug)
	if strings.TrimSpace(body) == "" {
		return "", title, slug
	}
	return "# " + title + "\n\n" + strings.TrimSpace(body) + "\n", title, slug
}

// Save stores a reviewed scan: its figures, then the page, each committed.
func (s *Scanner) Save(id string) error {
	s.mu.Lock()
	j, ok := s.jobs[id]
	if !ok || j.Status != scanReview || j.draft == nil {
		s.mu.Unlock()
		return fmt.Errorf("this scan was already saved or discarded")
	}
	d, target, author := j.draft, j.Target, j.Author
	j.Status, j.Step, j.photo, j.Draft, j.draft = scanRunning, "Saving the page", nil, "", nil
	s.mu.Unlock()

	if err := s.save(id, d, target, author); err != nil {
		log.Printf("scan %s: %v", id, err)
		s.update(id, func(j *ScanJob) { j.Status, j.Error = scanFailed, err.Error() })
		return err
	}
	return nil
}

func (s *Scanner) save(id string, d *scanDraft, targetSlug, author string) error {
	if targetSlug != "" {
		defer s.lockPage(targetSlug)()
		current, err := s.store.Load(KindPage, targetSlug)
		if err != nil {
			return fmt.Errorf("page not found: %s", targetSlug)
		}
		// An append goes on whatever the page is now; a rewrite would undo
		// edits made since the photo was read.
		if d.rewrite && current.Content != d.original {
			return fmt.Errorf("the page was edited after the scan; scan again")
		}
	}
	figures := s.storeFigures(d.figures, d.title, author)
	content, title, slug := s.compose(d, targetSlug, figures)
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("the pictures could not be stored, so there is nothing to add")
	}

	if targetSlug != "" {
		if d.rewrite {
			if err := s.store.Save(KindPage, targetSlug, content); err != nil {
				return fmt.Errorf("saving page %s: %w", targetSlug, err)
			}
		} else if err := s.store.AppendToPage(targetSlug, content); err != nil {
			return fmt.Errorf("adding to page %s: %w", targetSlug, err)
		}
		_ = s.autoCommit.CommitSave(KindPage, targetSlug, author)
		s.update(id, func(j *ScanJob) {
			j.Status, j.Slug, j.Appended, j.Figures = scanDone, targetSlug, true, len(figures)
		})
		return nil
	}

	if err := s.store.Save(KindPage, slug, content); err != nil {
		return fmt.Errorf("saving page %s: %w", slug, err)
	}
	_ = s.autoCommit.CommitSave(KindPage, slug, author)
	// The user picks where the page is linked from on the result screen.
	suggestions := s.parentChoices(title, slug, d.keywords)
	s.update(id, func(j *ScanJob) {
		j.Status, j.Slug, j.Title, j.Figures, j.Suggestions = scanDone, slug, title, len(figures), suggestions
	})
	return nil
}

// Discard drops a scan waiting for review; nothing of it was stored. It
// returns the page the scan was meant for, to scan again.
func (s *Scanner) Discard(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok || j.Status != scanReview {
		return "", fmt.Errorf("this scan was already saved or discarded")
	}
	delete(s.jobs, id)
	return j.Target, nil
}

// DraftFigure returns a figure of a scan waiting for review.
func (s *Scanner) DraftFigure(id string, fid int) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok || j.draft == nil {
		return nil, false
	}
	f, ok := j.draft.figures[fid]
	return f.data, ok
}

// straightenPage asks the finder for the page outline and returns the photo
// with the page cut out and its perspective corrected, plus the outline in
// pixels of the sent image; nil keeps the photo as it is (no document, an
// unusable outline, or an error).
func (s *Scanner) straightenPage(ctx context.Context, finder DocumentFinder, photo *scanPhoto) (*scanPhoto, [4]Point) {
	small, scale, w, h, err := photo.cornerImage()
	if err != nil {
		log.Printf("scan: %v", err)
		return nil, [4]Point{}
	}
	corners, found, err := finder.FindDocument(ctx, small, w, h)
	if err != nil {
		log.Printf("scan: %v", err)
		return nil, [4]Point{}
	}
	if !found {
		return nil, [4]Point{}
	}
	for i := range corners {
		corners[i] = Point{corners[i].X * scale, corners[i].Y * scale}
	}
	q := readingOrder(corners)
	sb := photo.sent.Bounds()
	if !usableQuad(q, float64(sb.Dx()), float64(sb.Dy())) {
		log.Printf("scan: page outline %v not usable, keeping the photo as is", q)
		return nil, [4]Point{}
	}
	flat, err := photo.straighten(q)
	if err != nil {
		log.Printf("scan: straightening: %v", err)
		return nil, [4]Point{}
	}
	return flat, q
}

// LinkFrom adds a [[link]] to a scanned page from the parent page (slug or
// title) and records it on the job.
func (s *Scanner) LinkFrom(id, parent string) error {
	job, ok := s.Job(id)
	if !ok || job.Status != scanDone || job.Appended {
		return fmt.Errorf("no new page to link")
	}
	parentSlug := strings.TrimSpace(parent)
	if _, err := s.store.Load(KindPage, parentSlug); err != nil {
		parentSlug = SlugFromTitle(parentSlug)
		if _, err := s.store.Load(KindPage, parentSlug); err != nil {
			return fmt.Errorf("page not found: %s", parent)
		}
	}
	if parentSlug == job.Slug {
		return fmt.Errorf("a page cannot link to itself")
	}
	addWikiLink(s.store, s.autoCommit, parentSlug, job.Title, "", job.Author)
	s.update(id, func(j *ScanJob) { j.LinkedFrom = parentSlug })
	return nil
}

// cutFigures crops and restyles each figure, keyed by figure id. Figures
// that fail are skipped.
func cutFigures(photo *scanPhoto, figures []ScanFigure) map[int]scanFigure {
	out := make(map[int]scanFigure)
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
		out[f.ID] = scanFigure{data: data, ext: ext, caption: f.Caption}
	}
	return out
}

// storeFigures stores each cut-out figure as an image, returning the
// Markdown that embeds it, keyed by figure id. Figures that fail are skipped.
func (s *Scanner) storeFigures(figures map[int]scanFigure, title, author string) map[int]string {
	out := make(map[int]string)
	base := sanitizeImageBasename(title)
	ids := make([]int, 0, len(figures))
	for fid := range figures {
		ids = append(ids, fid)
	}
	sort.Ints(ids)
	for _, fid := range ids {
		f := figures[fid]
		sf, err := s.store.SaveUpload(fmt.Sprintf("%s-fig%d%s", base, fid, f.ext), f.data, s.uploadCfg, f.caption)
		if err != nil {
			log.Printf("scan: saving figure %d: %v", fid, err)
			continue
		}
		_ = s.autoCommit.CommitImageSave(sf.Name, author)
		out[fid] = sf.Markdown
	}
	return out
}

// figureLabel is the alt text of a figure in the preview, as SaveUpload
// would write it.
func figureLabel(caption string) string {
	if label := strings.TrimSpace(strings.NewReplacer("[", "", "]", "", "\n", " ").Replace(caption)); label != "" {
		return label
	}
	return "figure"
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

// parentChoices ranks pages that could link to a new scan: the best matches
// for its title and keywords (full text blended with semantic search), with
// Home added when it is not among them.
func (s *Scanner) parentChoices(title, slug string, keywords []string) []ParentChoice {
	var out []ParentChoice
	hasHome := false
	if suggestions, err := s.store.SuggestParents(title, keywords, 5); err == nil {
		for _, sg := range suggestions {
			if sg.Slug == slug {
				continue
			}
			page, err := s.store.Load(KindPage, sg.Slug)
			if err != nil {
				continue
			}
			out = append(out, ParentChoice{Slug: sg.Slug, Title: pageDisplayTitle(page), Reason: sg.Reason})
			hasHome = hasHome || sg.Slug == "Home"
		}
	}
	if !hasHome {
		if page, err := s.store.Load(KindPage, "Home"); err == nil {
			out = append(out, ParentChoice{Slug: "Home", Title: pageDisplayTitle(page), Reason: "the start page"})
		}
	}
	return out
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
