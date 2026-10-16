package wiki

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/mnorrsken/gypsum/web"
	"golang.org/x/image/draw"
)

// fakeAnalyzer returns a canned result and records the request.
type fakeAnalyzer struct {
	result *ScanResult
	err    error
	got    ScanRequest
}

func (f *fakeAnalyzer) Analyze(_ context.Context, _ []byte, req ScanRequest) (*ScanResult, error) {
	f.got = req
	return f.result, f.err
}

// waitScan polls a job until it leaves the running state.
func waitScan(t *testing.T, s *Scanner, id string) ScanJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if job, ok := s.Job(id); ok && job.Status != scanRunning {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("scan did not finish")
	return ScanJob{}
}

// saveScan waits for a scan's preview, saves it and returns the job.
func saveScan(t *testing.T, s *Scanner, id string) ScanJob {
	t.Helper()
	if job := waitScan(t, s, id); job.Status != scanReview {
		t.Fatalf("job = %+v, want a preview", job)
	}
	if err := s.Save(id); err != nil {
		t.Fatal(err)
	}
	job, _ := s.Job(id)
	return job
}

// pagePhoto draws a white 400×300 "page" with a black box (the figure) at
// (100,100)-(200,200).
func pagePhoto(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 400; x++ {
			c := color.RGBA{250, 250, 245, 255}
			if x >= 120 && x < 180 && y >= 120 && y < 180 {
				c = color.RGBA{10, 10, 10, 255}
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPlaceFigures(t *testing.T) {
	figs := map[int]string{1: "![a](/images/a.png)", 2: "![b](/images/b.png)"}
	tests := []struct {
		name, in, want string
	}{
		{"placed", "Intro\n\n%%FIGURE 1%%\n\nMore\n\n%%FIGURE 2%%", "Intro\n\n![a](/images/a.png)\n\nMore\n\n![b](/images/b.png)"},
		{"missing marker dropped", "Intro\n\n%%FIGURE 7%%\n\nEnd\n%%FIGURE 1%%\n%%FIGURE 2%%", "Intro\n\nEnd\n![a](/images/a.png)\n![b](/images/b.png)"},
		{"unplaced appended", "Only text", "Only text\n\n![a](/images/a.png)\n\n![b](/images/b.png)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := placeFigures(tt.in, figs); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestKeepExistingLinks(t *testing.T) {
	existing := map[string]bool{"Oven": true, "Heat_Pump": true}
	tests := []struct{ in, want string }{
		{"the [[Heat Pump]] and [[Oven]]", "the [[Heat Pump]] and [[Oven]]"},
		{"a [[Made Up]] page", "a Made Up page"},
		{"self [[Scan Page]] link", "self Scan Page link"},
		{"```mermaid\nA[\"[[Made Up]]\"]\n```", "```mermaid\nA[\"[[Made Up]]\"]\n```"},
		{`escaped \[[Made Up]]`, `escaped \[[Made Up]]`},
	}
	for _, tt := range tests {
		if got := keepExistingLinks(tt.in, existing, "Scan_Page"); got != tt.want {
			t.Errorf("keepExistingLinks(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRenderFigureStyles(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 40, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 40; x++ {
			c := color.RGBA{230, 225, 210, 255} // paper
			if x < 10 {
				c = color.RGBA{40, 40, 50, 255} // ink
			} else if x > 30 {
				c = color.RGBA{200, 30, 30, 255} // red
			}
			img.Set(x, y, c)
		}
	}

	data, ext, err := renderFigure(img, figureBW, nil)
	if err != nil || ext != ".png" {
		t.Fatalf("bw: %v %s", err, ext)
	}
	bw, _ := png.Decode(bytes.NewReader(data))
	if p, ok := bw.(*image.Paletted); !ok || len(p.Palette) != 2 {
		t.Fatalf("bw is %T, want a 2-color paletted image", bw)
	}
	if r, _, _, _ := bw.At(5, 5).RGBA(); r != 0 {
		t.Error("ink is not black")
	}
	if r, _, _, _ := bw.At(20, 5).RGBA(); r != 0xffff {
		t.Error("paper is not white")
	}

	data, _, err = renderFigure(img, figurePalette, []string{"#ffffff", "#000000", "#cc0000", "bogus"})
	if err != nil {
		t.Fatal(err)
	}
	pal, _ := png.Decode(bytes.NewReader(data))
	if r, g, _, _ := pal.At(35, 5).RGBA(); r>>8 != 0xcc || g != 0 {
		t.Errorf("red area mapped to %v", pal.At(35, 5))
	}

	// Gray: a smooth gradient is reduced to exactly 4 gray levels, as PNG.
	grad := image.NewRGBA(image.Rect(0, 0, 256, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 256; x++ {
			grad.SetRGBA(x, y, color.RGBA{uint8(x), uint8(x), uint8(x), 255})
		}
	}
	data, ext, err = renderFigure(grad, figureGray, nil)
	if err != nil || ext != ".png" {
		t.Fatalf("gray: %v %s", err, ext)
	}
	g, _ := png.Decode(bytes.NewReader(data))
	levels := map[color.Color]bool{}
	for x := 0; x < 256; x++ {
		levels[g.At(x, 10)] = true
	}
	if len(levels) != grayLevels {
		t.Errorf("gray figure has %d levels, want %d", len(levels), grayLevels)
	}
	// The earlier style name still works.
	if _, ext, _ := renderFigure(grad, "grayscale", nil); ext != ".png" {
		t.Errorf("old grayscale style stored as %s", ext)
	}

	// Photo: full-color JPEG.
	if _, ext, _ := renderFigure(img, figurePhoto, nil); ext != ".jpg" {
		t.Errorf("photo stored as %s, want .jpg", ext)
	}
}

func TestPaletteOnUnevenlyLitWhiteboard(t *testing.T) {
	// A whiteboard lit from the right: the board goes from dark gray to
	// near white. A pale pink stroke runs across it, a blue one below.
	img := image.NewRGBA(image.Rect(0, 0, 300, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 300; x++ {
			light := 0.55 + 0.4*float64(x)/300
			c := [3]float64{250, 248, 240} // board
			switch {
			case y >= 60 && y < 64:
				c = [3]float64{245, 165, 175} // pale pink marker
			case y >= 130 && y < 134:
				c = [3]float64{60, 90, 180} // blue marker
			}
			img.SetRGBA(x, y, color.RGBA{uint8(c[0] * light), uint8(c[1] * light), uint8(c[2] * light), 255})
		}
	}
	data, _, err := renderFigure(img, figurePalette, []string{"#f4f4f4", "#a8d84a", "#e0408a", "#3a5fb0", "#222222"})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := png.Decode(bytes.NewReader(data))
	want := func(x, y int, hex string) {
		t.Helper()
		r, g, b, _ := out.At(x, y).RGBA()
		if got := fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8); got != hex {
			t.Errorf("(%d,%d) = %s, want %s", x, y, got, hex)
		}
	}
	for _, x := range []int{10, 150, 290} {
		want(x, 20, "#f4f4f4") // the board is paper in the shade too
		want(x, 62, "#e0408a") // pale pink stays pink
		want(x, 132, "#3a5fb0")
	}

	// Pixel art on a dark background is not "paper": colors map as they are.
	dark := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for i := 0; i < len(dark.Pix); i += 4 {
		copy(dark.Pix[i:], []uint8{20, 20, 30, 255})
	}
	if _, onPaper := flattenLighting(dark); onPaper {
		t.Error("a dark image was treated as paper")
	}
}

func TestPrepareScanImageLimits(t *testing.T) {
	p, err := prepareScanImage(testJPEG(t, 4000, 3000, 6))
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(p.jpeg))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	if max(b.Dx(), b.Dy()) > scanMaxPx || b.Dx()*b.Dy() > scanMaxPixels {
		t.Fatalf("scan image %dx%d is over the limits", b.Dx(), b.Dy())
	}
	if b.Dx() > b.Dy() {
		t.Errorf("orientation 6 not applied: %dx%d", b.Dx(), b.Dy())
	}
}

func TestCropFigureUsesFullResolution(t *testing.T) {
	// 4000×3000 stored sideways (orientation 6): upright it is 3000×4000 with
	// the red half on top and blue below.
	p, err := prepareScanImage(testJPEG(t, 4000, 3000, 6))
	if err != nil {
		t.Fatal(err)
	}
	sw, sh := p.sent.Bounds().Dx(), p.sent.Bounds().Dy()
	// Box over the top-left quarter of the sent image, inside the red half.
	crop := p.cropFigure(image.Rect(0, 0, sw/2, sh/4))
	if crop == nil {
		t.Fatal("no crop")
	}
	if got, want := crop.Bounds().Dx(), 1500; got < want-2 || got > want+2 {
		t.Errorf("crop width %d, want about %d (full resolution)", got, want)
	}
	if got, want := crop.Bounds().Dy(), 1000; got < want-2 || got > want+2 {
		t.Errorf("crop height %d, want about %d", got, want)
	}
	if c := crop.RGBAAt(crop.Bounds().Dx()/2, crop.Bounds().Dy()/2); c.R < 200 || c.B > 60 {
		t.Errorf("crop centre is %v, want red", c)
	}
	// The bottom quarter must be blue.
	crop = p.cropFigure(image.Rect(0, sh*3/4, sw, sh))
	if c := crop.RGBAAt(crop.Bounds().Dx()/2, crop.Bounds().Dy()/2); c.B < 200 || c.R > 60 {
		t.Errorf("bottom crop centre is %v, want blue", c)
	}
}

func TestUprightToRawAllOrientations(t *testing.T) {
	// A raw image where every pixel has its own color.
	raw := image.NewRGBA(image.Rect(0, 0, 7, 5))
	for y := 0; y < 5; y++ {
		for x := 0; x < 7; x++ {
			raw.SetRGBA(x, y, color.RGBA{uint8(x * 30), uint8(y * 40), 0, 255})
		}
	}
	r := image.Rect(1, 2, 4, 5) // a rectangle in upright coordinates
	for o := 1; o <= 8; o++ {
		upright := orientImage(raw, o)
		want := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
		draw.Draw(want, want.Bounds(), upright, r.Min, draw.Src)

		rr := uprightToRaw(r, o, 7, 5)
		crop := image.NewRGBA(image.Rect(0, 0, rr.Dx(), rr.Dy()))
		draw.Draw(crop, crop.Bounds(), raw, rr.Min, draw.Src)
		if got := orientImage(crop, o); !bytes.Equal(got.Pix, want.Pix) {
			t.Errorf("orientation %d: crop of raw does not match crop of upright image", o)
		}
	}
}

func TestScannerNewPage(t *testing.T) {
	store := NewPageStore(filepath.Join(t.TempDir(), "pages"))
	_ = store.Save(KindPage, "Home", "# Home\n\n- [[Manuals]]\n")
	_ = store.Save(KindPage, "Manuals", "# Manuals\n\nDishwasher and oven manuals.\n")
	fake := &fakeAnalyzer{result: &ScanResult{
		Title:    "Dishwasher Filter",
		Markdown: "## Cleaning the filter\n\nSee [[Manuals]] and [[Spare Parts]].\n\n1. Turn the filter.\n\n%%FIGURE 1%%\n\n2. Rinse it.",
		Figures:  []ScanFigure{{ID: 1, Caption: "Filter", X0: 110, Y0: 110, X1: 190, Y1: 190, Style: figureBW}},
		Keywords: []string{"dishwasher", "manuals"},
	}}
	s := NewScanner(store, nil, fake, DefaultUploadConfig)

	id := s.Start(pagePhoto(t), "", "dishwasher manual", "tester")
	// The preview stores nothing: its figure is served from the job.
	job := waitScan(t, s, id)
	if job.Status != scanReview || job.Title != "Dishwasher Filter" || job.Figures != 1 ||
		!strings.Contains(job.Draft, "![Filter](/scan/"+id+"/figure/1)") || !strings.HasPrefix(job.Draft, "# Dishwasher Filter\n") {
		t.Fatalf("preview job = %+v", job)
	}
	if data, ok := s.DraftFigure(id, 1); !ok || len(data) == 0 {
		t.Error("preview figure is missing")
	}
	if pages, _ := store.List(KindPage); len(pages) != 2 {
		t.Fatalf("%d pages before saving, want 2", len(pages))
	}
	if images, _ := store.ListImages(); len(images) != 0 {
		t.Fatalf("%d images before saving, want 0", len(images))
	}

	job = saveScan(t, s, id)
	if job.Status != scanDone || job.Slug != "Dishwasher_Filter" || job.Figures != 1 || job.Draft != "" {
		t.Fatalf("job = %+v", job)
	}
	if err := s.Save(id); err == nil {
		t.Error("a scan was saved twice")
	}
	if fake.got.Note != "dishwasher manual" || fake.got.Width != 400 || strings.Join(fake.got.Pages, ",") != "Home,Manuals" {
		t.Errorf("analyzer request = %+v", fake.got)
	}
	page, err := store.Load(KindPage, "Dishwasher_Filter")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(page.Content, "# Dishwasher Filter\n") || !strings.Contains(page.Content, "![Filter](/images/dishwasher-filter-fig1-") ||
		!strings.Contains(page.Content, "See [[Manuals]] and Spare Parts.") {
		t.Fatalf("page content:\n%s", page.Content)
	}
	images, _ := store.ListImages()
	if len(images) != 1 {
		t.Fatalf("stored %d images, want 1", len(images))
	}
	// Nothing is linked until the user picks a parent; the matching page is
	// offered first, the hub (Home) only after it.
	if parent, _ := store.Load(KindPage, "Manuals"); strings.Contains(parent.Content, "[[Dishwasher Filter]]") {
		t.Errorf("new page linked before the user chose a parent")
	}
	var offered []string
	for _, c := range job.Suggestions {
		offered = append(offered, c.Slug)
	}
	if len(offered) < 2 || offered[0] != "Manuals" || offered[len(offered)-1] != "Home" {
		t.Errorf("suggested parents = %v, want Manuals first and Home last", offered)
	}
	if err := s.LinkFrom(job.ID, "Manuals"); err != nil {
		t.Fatal(err)
	}
	if parent, _ := store.Load(KindPage, "Manuals"); !strings.Contains(parent.Content, "[[Dishwasher Filter]]") {
		t.Errorf("new page not linked from the chosen parent:\n%s", parent.Content)
	}
	if job, _ := s.Job(job.ID); job.LinkedFrom != "Manuals" {
		t.Errorf("LinkedFrom = %q", job.LinkedFrom)
	}
	if err := s.LinkFrom(job.ID, "No Such Page"); err == nil {
		t.Error("linking from a missing page succeeded")
	}

	// A discarded scan leaves nothing behind.
	id = s.Start(pagePhoto(t), "", "", "tester")
	waitScan(t, s, id)
	if target, err := s.Discard(id); err != nil || target != "" {
		t.Fatalf("Discard = %q, %v", target, err)
	}
	if _, ok := s.Job(id); ok {
		t.Error("discarded job is still listed")
	}
	if images, _ := store.ListImages(); len(images) != 1 {
		t.Errorf("%d images after a discard, want 1", len(images))
	}

	// A second scan with the same title gets a new page, not an overwrite.
	job = saveScan(t, s, s.Start(pagePhoto(t), "", "", "tester"))
	if job.Slug != "Dishwasher_Filter_2" {
		t.Errorf("second scan slug = %q", job.Slug)
	}
}

func TestScannerAppendAndFailure(t *testing.T) {
	store := NewPageStore(filepath.Join(t.TempDir(), "pages"))
	_ = store.Save(KindPage, "Oven", "# Oven Manual\n\n## Setup\n\nPlug it in.\n")
	fake := &fakeAnalyzer{result: &ScanResult{Title: "Oven Manual", Markdown: "## Cleaning\n\nWipe it."}}
	s := NewScanner(store, nil, fake, DefaultUploadConfig)

	job := saveScan(t, s, s.Start(pagePhoto(t), "Oven", "", "tester"))
	if job.Status != scanDone || !job.Appended || job.Slug != "Oven" {
		t.Fatalf("job = %+v", job)
	}
	if fake.got.AppendTitle != "Oven Manual" || fake.got.PageBody != "## Setup\n\nPlug it in." {
		t.Errorf("analyzer request = %+v", fake.got)
	}
	page, _ := store.Load(KindPage, "Oven")
	if !strings.HasSuffix(page.Content, "Plug it in.\n\n## Cleaning\n\nWipe it.\n") {
		t.Fatalf("page content:\n%q", page.Content)
	}

	// An empty answer fails instead of adding blank lines.
	fake.result = &ScanResult{Title: "Oven Manual", Markdown: "%%FIGURE 1%%"}
	job = waitScan(t, s, s.Start(pagePhoto(t), "Oven", "", "tester"))
	if job.Status != scanFailed || !strings.Contains(job.Error, "nothing to add") {
		t.Fatalf("empty job = %+v", job)
	}

	fake.err = errors.New("claude API error 529: overloaded")
	job = waitScan(t, s, s.Start(pagePhoto(t), "Oven", "", "tester"))
	if job.Status != scanFailed || !strings.Contains(job.Error, "overloaded") {
		t.Fatalf("failed job = %+v", job)
	}
	if page2, _ := store.Load(KindPage, "Oven"); page2.Content != page.Content {
		t.Errorf("failed scans changed the page:\n%q", page2.Content)
	}
}

func TestScannerRewrite(t *testing.T) {
	store := NewPageStore(filepath.Join(t.TempDir(), "pages"))
	_ = store.Save(KindPage, "Icons", "# Pixel Icons\n\n![Tank](/images/tank.png)\n\nTwo tanks.\n\n![Dome](/images/dome.png)\n\nA dome.\n")
	// Claude restructures the page with a heading per icon, but drops the dome image.
	fake := &fakeAnalyzer{result: &ScanResult{
		Title:    "Pixel Icons",
		Rewrite:  true,
		Markdown: "## Tank\n\n![Tank](/images/tank.png)\n\nTwo tanks.\n\n## Dome\n\nA dome.\n\n## Rocket\n\n%%FIGURE 1%%\n\nA rocket.",
		Figures:  []ScanFigure{{ID: 1, Caption: "Rocket", X0: 110, Y0: 110, X1: 190, Y1: 190, Style: figurePhoto}},
	}}
	s := NewScanner(store, nil, fake, DefaultUploadConfig)

	job := saveScan(t, s, s.Start(pagePhoto(t), "Icons", "", "tester"))
	if job.Status != scanDone || !job.Rewritten {
		t.Fatalf("job = %+v", job)
	}
	page, _ := store.Load(KindPage, "Icons")
	c := page.Content
	if !strings.HasPrefix(c, "# Pixel Icons\n\n## Tank\n") || !strings.Contains(c, "## Rocket\n\n![Rocket](/images/pixel-icons-fig1-") {
		t.Fatalf("page content:\n%s", c)
	}
	if !strings.HasSuffix(c, "\n\n![Dome](/images/dome.png)\n") {
		t.Errorf("dropped image was not restored:\n%s", c)
	}

	// Pages with encrypted fields are only sent as headings and never rewritten.
	_ = store.Save(KindPage, "Secret", "# Secret\n\n## Wifi\n\n{{secure_aes:abc=}}\n")
	fake.result = &ScanResult{Title: "Secret", Rewrite: true, Markdown: "## Router\n\nA router."}
	job = saveScan(t, s, s.Start(pagePhoto(t), "Secret", "", "tester"))
	if fake.got.PageBody != "" || len(fake.got.Headings) != 2 || job.Rewritten {
		t.Fatalf("secure page request = %+v, job = %+v", fake.got, job)
	}
	if page, _ := store.Load(KindPage, "Secret"); !strings.Contains(page.Content, "{{secure_aes:abc=}}\n\n## Router") {
		t.Errorf("secure page content:\n%s", page.Content)
	}

	// A rewrite is not saved over edits made while it waited for review.
	fake.result = &ScanResult{Title: "Pixel Icons", Rewrite: true, Markdown: "## Tank\n\nOnly tanks."}
	id := s.Start(pagePhoto(t), "Icons", "", "tester")
	waitScan(t, s, id)
	_ = store.Save(KindPage, "Icons", c+"\nEdited.\n")
	if err := s.Save(id); err == nil || !strings.Contains(err.Error(), "edited") {
		t.Fatalf("Save over an edit = %v", err)
	}
	if page, _ := store.Load(KindPage, "Icons"); page.Content != c+"\nEdited.\n" {
		t.Errorf("rewrite overwrote an edit:\n%s", page.Content)
	}
}

// slowAnalyzer appends a numbered section and records the page body it saw.
type slowAnalyzer struct {
	mu     sync.Mutex
	n      int
	bodies []string
}

func (a *slowAnalyzer) Analyze(_ context.Context, _ []byte, req ScanRequest) (*ScanResult, error) {
	a.mu.Lock()
	a.n++
	n := a.n
	a.bodies = append(a.bodies, req.PageBody)
	a.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	return &ScanResult{Title: "List", Markdown: fmt.Sprintf("Item %d", n)}, nil
}

func TestScannerKeepsBothScansOfOnePage(t *testing.T) {
	store := NewPageStore(filepath.Join(t.TempDir(), "pages"))
	_ = store.Save(KindPage, "List", "# List\n\nStart\n")
	s := NewScanner(store, nil, &slowAnalyzer{}, DefaultUploadConfig)

	a := s.Start(pagePhoto(t), "List", "", "tester")
	b := s.Start(pagePhoto(t), "List", "", "tester")
	waitScan(t, s, a)
	waitScan(t, s, b)
	var wg sync.WaitGroup
	for _, id := range []string{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Save(id); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	page, _ := store.Load(KindPage, "List")
	if !strings.Contains(page.Content, "Item 1") || !strings.Contains(page.Content, "Item 2") {
		t.Fatalf("a concurrent scan was lost:\n%s", page.Content)
	}
}

func TestScanHandlers(t *testing.T) {
	store := NewPageStore(filepath.Join(t.TempDir(), "pages"))
	_ = store.Save(KindPage, "Home", "# Home\n")
	h := NewHandler(store, NewMarkdownRenderer(), web.Templates(), nil, nil, nil, AllMCPSections)
	h.SetScanner(NewScanner(store, nil, &fakeAnalyzer{result: &ScanResult{Title: "Note", Markdown: "Hello"}}, DefaultUploadConfig))
	routes := h.Routes()

	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/scan?page=Home", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `name="page" value="Home"`) {
		t.Fatalf("form: status %d", rec.Code)
	}

	body, ct := multipartPhoto(t, "IMG_1234.JPG", pagePhoto(t))
	req := httptest.NewRequest(http.MethodPost, "/scan", body)
	req.Header.Set("Content-Type", ct)
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/scan/") {
		t.Fatalf("start: status %d, location %q, body %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	id := strings.TrimPrefix(rec.Header().Get("Location"), "/scan/")
	waitScan(t, h.scanner, id)

	// The preview shows the page as it would be saved, and the buttons.
	req = httptest.NewRequest(http.MethodGet, "/scan/"+id, nil)
	req.Header.Set("HX-Request", "true")
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	out := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(out, "<p>Hello</p>") || !strings.Contains(out, `action="/scan/`+id+`/save"`) ||
		!strings.Contains(out, `action="/scan/`+id+`/discard"`) || strings.Contains(out, "<html") || strings.Contains(out, "hx-trigger") {
		t.Fatalf("preview fragment: %d\n%s", rec.Code, out)
	}
	if _, err := store.Load(KindPage, "Note"); err == nil {
		t.Fatal("the page was saved before the preview was accepted")
	}
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/scan/"+id+"/save", nil))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/scan/"+id {
		t.Fatalf("save: status %d, location %q", rec.Code, rec.Header().Get("Location"))
	}
	req = httptest.NewRequest(http.MethodGet, "/scan/"+id, nil)
	req.Header.Set("HX-Request", "true")
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if out := rec.Body.String(); !strings.Contains(out, `href="/wiki/Note"`) {
		t.Fatalf("result fragment:\n%s", out)
	}

	// The result screen offers parents; posting one links the page.
	req = httptest.NewRequest(http.MethodGet, "/scan/"+id, nil)
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `name="parent" value="Home"`) {
		t.Fatalf("result page offers no parent:\n%s", rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/scan/"+id+"/link", strings.NewReader("parent=Home"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("link: status %d", rec.Code)
	}
	if home, _ := store.Load(KindPage, "Home"); !strings.Contains(home.Content, "[[Note]]") {
		t.Errorf("Home does not link the scanned page:\n%s", home.Content)
	}
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/scan/"+id, nil))
	if !strings.Contains(rec.Body.String(), "Linked from") {
		t.Errorf("result page does not say where the page is linked from")
	}

	// Discarding goes back to the capture screen for the same page.
	other := h.scanner.Start(pagePhoto(t), "Home", "", "tester")
	waitScan(t, h.scanner, other)
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/scan/"+other+"/discard", nil))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/scan?page=Home" {
		t.Fatalf("discard: status %d, location %q", rec.Code, rec.Header().Get("Location"))
	}
	if home, _ := store.Load(KindPage, "Home"); strings.Contains(home.Content, "Hello") {
		t.Errorf("a discarded scan changed Home:\n%s", home.Content)
	}

	body, ct = multipartPhoto(t, "IMG_1.HEIC", []byte("x"))
	req = httptest.NewRequest(http.MethodPost, "/scan", body)
	req.Header.Set("Content-Type", ct)
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("HEIC: status %d, want 400", rec.Code)
	}
}

// multipartPhoto builds a scan form with one photo.
func multipartPhoto(t *testing.T, filename string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	body, ct := multipartBody(t, filename, data, nil)
	// multipartBody names the field "file"; the scan form uses "photo".
	raw := bytes.Replace(body.Bytes(), []byte(`name="file"`), []byte(`name="photo"`), 1)
	return bytes.NewBuffer(raw), ct
}

func TestClaudeAnalyzerRequest(t *testing.T) {
	var got map[string]any
	var beta string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		beta = r.Header.Get("anthropic-beta")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5",
			"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1},
			"content":[{"type":"text","text":"{\"title\":\"T\",\"markdown\":\"M\",\"figures\":[{\"id\":1,\"caption\":\"c\",\"x0\":1,\"y0\":2,\"x1\":3,\"y1\":4,\"style\":\"bw\",\"colors\":[]}],\"keywords\":[\"k\"]}"}]}`))
	}))
	defer srv.Close()

	a := NewClaudeAnalyzer("test-key", "", option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
	res, err := a.Analyze(context.Background(), []byte{0xFF, 0xD8}, ScanRequest{Width: 10, Height: 20, AppendTitle: "Oven", Pages: []string{"Heat Pump"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "T" || len(res.Figures) != 1 || res.Figures[0].X1 != 3 || res.Figures[0].Style != figureBW {
		t.Fatalf("result = %+v", res)
	}
	if got["model"] != DefaultScanModel || got["fallbacks"] != "default" || !strings.Contains(beta, "server-side-fallback-2026-07-01") {
		t.Errorf("model/fallbacks/beta = %v / %v / %q", got["model"], got["fallbacks"], beta)
	}
	oc, _ := got["output_config"].(map[string]any)
	format, _ := oc["format"].(map[string]any)
	if oc["effort"] != "medium" || format["type"] != "json_schema" || format["schema"] == nil {
		t.Errorf("output_config = %v", oc)
	}
	if _, ok := got["thinking"]; ok {
		t.Error("request sets thinking; Opus 5.5 runs adaptive by default")
	}
	raw, _ := json.Marshal(got["messages"])
	if !strings.Contains(string(raw), `"type":"image"`) || !strings.Contains(string(raw), `is added to the existing wiki page \"Oven\"`) ||
		!strings.Contains(string(raw), `Existing wiki pages:\nHeat Pump`) {
		t.Errorf("messages = %s", raw)
	}
}

func TestSuggestParentsPrefersMatchesOverHubs(t *testing.T) {
	store := NewPageStore(filepath.Join(t.TempDir(), "pages"))
	var links []string
	for i := 0; i < 30; i++ {
		slug := fmt.Sprintf("Topic_%d", i)
		_ = store.Save(KindPage, slug, "# Topic\n\nUnrelated.\n")
		links = append(links, "[[Topic "+fmt.Sprint(i)+"]]")
	}
	_ = store.Save(KindPage, "Home", "# Home\n\n"+strings.Join(links, "\n"))
	_ = store.Save(KindPage, "Heat_Pump", "# Heat Pump\n\nThe NIBE heat pump and its [[Manuals]].\n")

	got, err := store.SuggestParents("NIBE SMO 10 wiring", []string{"heat pump", "NIBE"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].Slug != "Heat_Pump" {
		var slugs []string
		for _, g := range got {
			slugs = append(slugs, g.Slug)
		}
		t.Fatalf("suggestions = %v, want Heat_Pump first (Home only links to many pages)", slugs)
	}
}

// findingAnalyzer is a fakeAnalyzer that also reports a page outline.
type findingAnalyzer struct {
	fakeAnalyzer
	corners [4]Point // in pixels of a photoW×photoH photo
	photoW  int
	gotW    int // width of the image the scan pass received
	gotH    int
}

func (f *findingAnalyzer) FindDocument(_ context.Context, _ []byte, w, h int) ([4]Point, bool, error) {
	var out [4]Point
	s := float64(w) / float64(f.photoW)
	for i, c := range f.corners {
		out[i] = Point{c.X * s, c.Y * s}
	}
	return out, true, nil
}

func (f *findingAnalyzer) Analyze(ctx context.Context, data []byte, req ScanRequest) (*ScanResult, error) {
	f.gotW, f.gotH = req.Width, req.Height
	return f.fakeAnalyzer.Analyze(ctx, data, req)
}

func TestScannerStraightensThePage(t *testing.T) {
	store := NewPageStore(filepath.Join(t.TempDir(), "pages"))
	doc := quadrantDoc(420, 594)
	quad := cameraQuad(420, 594, 1000, 900, 1100, 35, 12)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, photoOfDoc(t, doc, quad, 900, 1100), &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	a := &findingAnalyzer{fakeAnalyzer: fakeAnalyzer{result: &ScanResult{Title: "Quadrants", Markdown: "Four colors."}}, corners: quad, photoW: 900}
	s := NewScanner(store, nil, a, DefaultUploadConfig)

	job := waitScan(t, s, s.Start(buf.Bytes(), "", "", "tester"))
	if job.Status != scanReview || !job.Straightened {
		t.Fatalf("job = %+v", job)
	}
	if _, err := jpeg.Decode(bytes.NewReader(job.Preview)); err != nil {
		t.Errorf("no outline preview: %v", err)
	}
	// The reading pass got the straightened page, with A4 proportions.
	if ratio := float64(a.gotH) / float64(a.gotW); math.Abs(ratio-594.0/420) > 0.05 {
		t.Errorf("scan pass got a %dx%d image (ratio %.2f), want the straightened A4 page", a.gotW, a.gotH, ratio)
	}

	// A wrong outline can still be undone from the preview.
	newID, err := s.RescanFlat(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job := waitScan(t, s, newID); job.Status != scanReview || job.Straightened || a.gotW != 900 {
		t.Errorf("rescan from the preview = %+v (scan width %d)", job, a.gotW)
	}
	if old, _ := s.Job(job.ID); old.Status != scanFailed || old.Draft != "" {
		t.Errorf("replaced preview = %+v", old)
	}

	// No usable outline (the photo's own frame): the photo is used as it is.
	a.corners = [4]Point{{0, 0}, {900, 0}, {900, 1100}, {0, 1100}}
	job = waitScan(t, s, s.Start(buf.Bytes(), "", "", "tester"))
	if job.Straightened || a.gotW != 900 {
		t.Errorf("full-frame outline straightened the photo: job %+v, scan width %d", job, a.gotW)
	}
}

func TestClaudeFindDocumentRequest(t *testing.T) {
	var got map[string]any
	answer := `{"found":true,"outline":[{"x":10,"y":12},{"x":300,"y":20},{"x":290,"y":400},{"x":5,"y":390}],` +
		`"page_corners":[{"x":10,"y":12},{"x":300,"y":20},{"x":290,"y":400},{"x":5,"y":390}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		text, _ := json.Marshal(answer)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-5-5",
			"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1},
			"content":[{"type":"text","text":` + string(text) + `}]}`))
	}))
	defer srv.Close()

	a := NewClaudeAnalyzer("test-key", "", option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
	corners, found, err := a.FindDocument(context.Background(), []byte{0xFF, 0xD8}, 320, 420)
	if err != nil || !found || corners[2] != (Point{290, 400}) {
		t.Fatalf("FindDocument = %v, %v, %v", corners, found, err)
	}
	if got["model"] != DefaultScanCornerModel {
		t.Errorf("model = %v, want %s", got["model"], DefaultScanCornerModel)
	}
	raw, _ := json.Marshal(got["messages"])
	if !strings.Contains(string(raw), "320×420 pixels") || !strings.Contains(string(raw), "reading order") {
		t.Errorf("prompt lacks the image size or reading order: %s", raw)
	}

	// A page cut off at the right: the corner the model put on the border
	// is rebuilt from the outline's sides.
	answer = `{"found":true,"outline":[{"x":20,"y":20},{"x":320,"y":40},{"x":320,"y":200},{"x":200,"y":400},{"x":10,"y":380}],` +
		`"page_corners":[{"x":20,"y":20},{"x":320,"y":40},{"x":200,"y":400},{"x":10,"y":380}]}`
	corners, _, _ = a.FindDocument(context.Background(), []byte{0xFF, 0xD8}, 320, 420)
	if want := (Point{412.3, 46.15}); math.Abs(corners[1].X-want.X) > 1 || math.Abs(corners[1].Y-want.Y) > 1 {
		t.Errorf("hidden corner = %v, want about %v", corners[1], want)
	}

	a.SetCornerModel("")
	if _, found, err := a.FindDocument(context.Background(), nil, 1, 1); found || err != nil {
		t.Errorf("disabled corner model still searched: %v %v", found, err)
	}
}

// blockingAnalyzer finds a page outline, and its first Analyze call waits
// until the scan is stopped.
type blockingAnalyzer struct {
	findingAnalyzer
	calls atomic.Int32
}

func (b *blockingAnalyzer) Analyze(ctx context.Context, data []byte, req ScanRequest) (*ScanResult, error) {
	if b.calls.Add(1) == 1 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return b.findingAnalyzer.Analyze(ctx, data, req)
}

func TestRescanWithoutStraightening(t *testing.T) {
	store := NewPageStore(filepath.Join(t.TempDir(), "pages"))
	quad := cameraQuad(420, 594, 1000, 900, 1100, 35, 12)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, photoOfDoc(t, quadrantDoc(420, 594), quad, 900, 1100), &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	a := &blockingAnalyzer{findingAnalyzer: findingAnalyzer{
		fakeAnalyzer: fakeAnalyzer{result: &ScanResult{Title: "Quadrants", Markdown: "Four colors."}},
		corners:      quad, photoW: 900}}
	s := NewScanner(store, nil, a, DefaultUploadConfig)
	h := NewHandler(store, NewMarkdownRenderer(), web.Templates(), nil, nil, nil, AllMCPSections)
	h.SetScanner(s)
	routes := h.Routes()

	id := s.Start(buf.Bytes(), "", "", "tester")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if job, _ := s.Job(id); len(job.Preview) > 0 && a.calls.Load() == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// While it reads, the progress screen shows the outline and the button.
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/scan/"+id, nil))
	body := rec.Body.String()
	if !strings.Contains(body, `/scan/`+id+`/preview.jpg`) || !strings.Contains(body, `action="/scan/`+id+`/flat"`) {
		t.Fatalf("progress screen lacks the preview or the button:\n%s", body)
	}
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/scan/"+id+"/preview.jpg", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("preview: status %d, type %q", rec.Code, rec.Header().Get("Content-Type"))
	}

	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/scan/"+id+"/flat", nil))
	newID := strings.TrimPrefix(rec.Header().Get("Location"), "/scan/")
	if rec.Code != http.StatusSeeOther || newID == "" || newID == id {
		t.Fatalf("rescan: status %d, location %q", rec.Code, rec.Header().Get("Location"))
	}
	job := saveScan(t, s, newID)
	if job.Status != scanDone || job.Straightened || a.gotW != 900 {
		t.Fatalf("rescan job = %+v (scan width %d), want the unstraightened photo", job, a.gotW)
	}
	old, _ := s.Job(id)
	if old.Status != scanFailed || old.ReplacedBy != newID || old.Slug != "" {
		t.Errorf("stopped job = %+v", old)
	}
	pages, _ := store.List(KindPage)
	if len(pages) != 1 {
		t.Errorf("%d pages saved, want 1 (the stopped scan must not save)", len(pages))
	}
}
