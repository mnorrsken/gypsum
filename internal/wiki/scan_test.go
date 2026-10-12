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
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
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

	// Flat areas: grayscale is kept as a sharp PNG.
	data, ext, err = renderFigure(img, figureGrayscale, nil)
	if err != nil || ext != ".png" {
		t.Fatalf("grayscale diagram: %v %s", err, ext)
	}
	if g, _ := png.Decode(bytes.NewReader(data)); g.ColorModel() != color.GrayModel {
		t.Errorf("grayscale figure has color model %v", g.ColorModel())
	}

	// Smooth shading with grain, like a photo: grayscale falls back to JPEG.
	photo := image.NewRGBA(image.Rect(0, 0, 256, 256))
	seed := uint32(1)
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			seed = seed*1664525 + 1013904223
			v := uint8((x+y)/2) + uint8(seed>>28)
			photo.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
	if _, ext, _ := renderFigure(photo, figureGrayscale, nil); ext != ".jpg" {
		t.Errorf("grayscale photo stored as %s, want .jpg", ext)
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

	job := waitScan(t, s, s.Start(pagePhoto(t), "", "dishwasher manual", "tester"))
	if job.Status != scanDone || job.Slug != "Dishwasher_Filter" || job.Figures != 1 {
		t.Fatalf("job = %+v", job)
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
	parent, _ := store.Load(KindPage, "Manuals")
	if !strings.Contains(parent.Content, "[[Dishwasher Filter]]") {
		t.Errorf("new page not linked from its parent:\n%s", parent.Content)
	}

	// A second scan with the same title gets a new page, not an overwrite.
	job = waitScan(t, s, s.Start(pagePhoto(t), "", "", "tester"))
	if job.Slug != "Dishwasher_Filter_2" {
		t.Errorf("second scan slug = %q", job.Slug)
	}
}

func TestScannerAppendAndFailure(t *testing.T) {
	store := NewPageStore(filepath.Join(t.TempDir(), "pages"))
	_ = store.Save(KindPage, "Oven", "# Oven Manual\n\n## Setup\n\nPlug it in.\n")
	fake := &fakeAnalyzer{result: &ScanResult{Title: "Oven Manual", Markdown: "## Cleaning\n\nWipe it."}}
	s := NewScanner(store, nil, fake, DefaultUploadConfig)

	job := waitScan(t, s, s.Start(pagePhoto(t), "Oven", "", "tester"))
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
		Figures:  []ScanFigure{{ID: 1, Caption: "Rocket", X0: 110, Y0: 110, X1: 190, Y1: 190, Style: figureColor}},
	}}
	s := NewScanner(store, nil, fake, DefaultUploadConfig)

	job := waitScan(t, s, s.Start(pagePhoto(t), "Icons", "", "tester"))
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
	job = waitScan(t, s, s.Start(pagePhoto(t), "Secret", "", "tester"))
	if fake.got.PageBody != "" || len(fake.got.Headings) != 2 || job.Rewritten {
		t.Fatalf("secure page request = %+v, job = %+v", fake.got, job)
	}
	if page, _ := store.Load(KindPage, "Secret"); !strings.Contains(page.Content, "{{secure_aes:abc=}}\n\n## Router") {
		t.Errorf("secure page content:\n%s", page.Content)
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

func TestScannerSerializesScansOfOnePage(t *testing.T) {
	store := NewPageStore(filepath.Join(t.TempDir(), "pages"))
	_ = store.Save(KindPage, "List", "# List\n\nStart\n")
	s := NewScanner(store, nil, &slowAnalyzer{}, DefaultUploadConfig)

	a := s.Start(pagePhoto(t), "List", "", "tester")
	b := s.Start(pagePhoto(t), "List", "", "tester")
	waitScan(t, s, a)
	waitScan(t, s, b)
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

	req = httptest.NewRequest(http.MethodGet, "/scan/"+id, nil)
	req.Header.Set("HX-Request", "true")
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	out := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(out, `href="/wiki/Note"`) || strings.Contains(out, "<html") || strings.Contains(out, "hx-trigger") {
		t.Fatalf("status fragment: %d\n%s", rec.Code, out)
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
