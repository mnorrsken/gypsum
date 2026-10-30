//go:build uitest

// Package uitest drives the real web UI in headless Chrome: every main page is
// loaded at phone, tablet and desktop widths and checked for sideways
// overflow and JavaScript errors, and a few interactions are exercised.
//
// Run with `make ui-test` (needs Chrome or Chromium). Set UI_SCREENSHOTS to a
// directory to save a full-page screenshot of every page at every width.
package uitest

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/mnorrsken/gypsum/internal/wiki"
	"github.com/mnorrsken/gypsum/web"
)

// widths are the viewport widths every page is checked at.
var widths = []int64{360, 390, 768, 1280}

const widePage = `# Wide Test

| Pin | Function | Voltage | Notes about this pin that are quite long | Default | Alt |
|---|---|---|---|---|---|
| 1 | VCC | 3.3V | Power supply for the board, do not exceed | on | none |

` + "```" + `
a very long code line that does not wrap at all and keeps going and going past the screen edge
` + "```" + `

https://example.com/a/very/long/url/without/any/spaces/that/keeps/going/and/going/forever.html

See [[Home]] and the pump.

` + "```mermaid" + `
flowchart LR
  A["Start the pump"] --> B{"Pressure OK?"} --> C["Open the valve"] --> D["Fill the tank"] --> E["Done"]
` + "```" + `
`

// fakeAnalyzer stands in for Claude so the scan pages are enabled.
type fakeAnalyzer struct{}

func (fakeAnalyzer) Analyze(context.Context, []byte, wiki.ScanRequest) (*wiki.ScanResult, error) {
	return &wiki.ScanResult{Title: "Pump wiring", Markdown: "How the pump is wired.", Keywords: []string{"pump"}}, nil
}

// fixture is a running wiki with seeded content.
type fixture struct {
	srv          *httptest.Server
	fromHash, to string // two revisions of Wide_Test for the diff page
	shareToken   string // public share link of Wide_Test
	scanner      *wiki.Scanner
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	pages := filepath.Join(repo, "pages")
	if err := os.MkdirAll(pages, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := wiki.OpenDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	store := wiki.NewPageStore(pages)
	commit := wiki.NewGitAutoCommitter(repo, nil)
	save := func(slug, content string) {
		t.Helper()
		if err := store.Save(wiki.KindPage, slug, content); err != nil {
			t.Fatal(err)
		}
		if err := commit.CommitSave(wiki.KindPage, slug, "tester"); err != nil {
			t.Fatal(err)
		}
	}
	save("Home", "# Home\n\nWelcome. See [[Wide Test]].\n")
	save("Wide_Test", widePage)
	save("Wide_Test", widePage+"\nedit 1\n")
	save("Wide_Test", widePage+"\nedit 1\nedit 2\n")
	store.SetDB(db)
	if err := store.Save(wiki.KindSkill, "Pump_Check", "# Pump Check\n\nCheck the pump.\n\nTags: pump\n"); err != nil {
		t.Fatal(err)
	}

	// A note, two tagged secrets and uploads, so their cards and buttons render.
	if _, err := store.CreateNote("Shopping list\nmilk, bread"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSecret(wiki.SecretEntry{Title: "Home router", URL: "https://router.example", Description: "Admin login", Tags: []string{"home", "network"}}, "{{secure_aes:QUJDREVGR0g=}}"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSecret(wiki.SecretEntry{Title: "Mail", Tags: []string{"work"}}, "{{secure_aes:QUJDREVGR0g=}}"); err != nil {
		t.Fatal(err)
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, image.NewRGBA(image.Rect(0, 0, 40, 30))); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveUpload("diagram.png", pngBuf.Bytes(), wiki.DefaultUploadConfig, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveUpload("manual.txt", []byte("pump manual"), wiki.DefaultUploadConfig, ""); err != nil {
		t.Fatal(err)
	}

	h := wiki.NewHandler(store, wiki.NewMarkdownRenderer(), web.Templates(), commit, nil, db, wiki.AllMCPSections)
	h.SetDocsDir(filepath.Join("..", "..", "docs"))
	scanner := wiki.NewScanner(store, commit, fakeAnalyzer{}, wiki.DefaultUploadConfig)
	h.SetScanner(scanner)

	mux := http.NewServeMux()
	mux.Handle("/", h.Routes())
	wiki.SetAssetVersion(web.AssetVersion())
	mux.Handle("/static/", http.StripPrefix("/static/", web.StaticHandler()))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	out, err := exec.Command("git", "-C", repo, "log", "--format=%h", "--", "pages/Wide_Test.md").Output()
	if err != nil {
		t.Fatal(err)
	}
	hashes := strings.Fields(string(out))
	share, err := db.CreateShare(wiki.KindPage, "Wide_Test")
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{srv: srv, fromHash: hashes[len(hashes)-1], to: hashes[0], shareToken: share.Token, scanner: scanner}
}

// browser starts headless Chrome and collects JavaScript errors per page.
type browser struct {
	ctx    context.Context
	mu     sync.Mutex
	errors []string
}

func newBrowser(t *testing.T) *browser {
	t.Helper()
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.WindowSize(1280, 900))
	inCI := os.Getenv("CI") != ""
	if inCI {
		// GitHub's Ubuntu runners do not allow Chrome's user-namespace sandbox.
		opts = append(opts, chromedp.NoSandbox)
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancel := chromedp.NewContext(allocCtx)
	t.Cleanup(func() { cancel(); cancelAlloc() })
	if err := chromedp.Do(ctx); err != nil {
		if inCI {
			t.Fatalf("Chrome not available in CI: %v", err)
		}
		t.Skipf("Chrome not available: %v", err)
	}
	b := &browser{ctx: ctx}
	exceptions := chromedp.Events(ctx, runtime.ExceptionThrown)
	console := chromedp.Events(ctx, runtime.ConsoleAPICalled)
	go func() {
		for ev, err := range exceptions {
			if err != nil {
				return
			}
			msg := ev.ExceptionDetails.Text
			if ex := ev.ExceptionDetails.Exception; ex != nil && ex.Description != "" {
				msg = ex.Description
			}
			b.record("exception: " + msg)
		}
	}()
	go func() {
		for ev, err := range console {
			if err != nil {
				return
			}
			if ev.Type == "error" {
				var parts []string
				for _, a := range ev.Args {
					parts = append(parts, strings.Trim(string(a.Value), `"`))
				}
				b.record("console.error: " + strings.Join(parts, " "))
			}
		}
	}()
	return b
}

func (b *browser) record(msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.errors = append(b.errors, msg)
}

// takeErrors returns and clears the errors seen since the last call.
func (b *browser) takeErrors() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	errs := b.errors
	b.errors = nil
	return errs
}

// open loads url at the given viewport width (phone widths emulate a touch
// device) in the light color scheme and waits for the page to settle.
func (b *browser) open(t *testing.T, url string, width int64) {
	t.Helper()
	b.openScheme(t, url, width, "light")
}

// openScheme is open with an explicit prefers-color-scheme ("light"/"dark").
func (b *browser) openScheme(t *testing.T, url string, width int64, scheme string) {
	t.Helper()
	b.openSized(t, url, width, 844, scheme)
}

// openSized loads url in a width×height viewport.
func (b *browser) openSized(t *testing.T, url string, width, height int64, scheme string) {
	t.Helper()
	opts := []chromedp.EmulateViewportOption{}
	if width < 768 {
		opts = append(opts, chromedp.EmulateMobile)
	}
	ctx, cancel := context.WithTimeout(b.ctx, 20*time.Second)
	defer cancel()
	if _, err := chromedp.Call(ctx, emulation.SetEmulatedMedia, emulation.SetEmulatedMediaParams{
		Features: []*emulation.MediaFeature{{Name: "prefers-color-scheme", Value: scheme}},
	}); err != nil {
		t.Fatalf("set color scheme: %v", err)
	}
	if err := chromedp.Do(ctx,
		chromedp.EmulateViewport(width, height, opts...),
		chromedp.Navigate(url),
		chromedp.WaitReady(chromedp.CSS("body")),
		chromedp.Sleep(300*time.Millisecond), // let Alpine, htmx and mermaid run
	); err != nil {
		t.Fatalf("open %s at %dpx: %v", url, width, err)
	}
}

// eval runs a JavaScript expression and decodes its result.
func eval[T any](t *testing.T, b *browser, expr string) T {
	t.Helper()
	v, err := chromedp.Run(b.ctx, chromedp.Evaluate[T](expr, chromedp.EvalAwaitPromise))
	if err != nil {
		t.Fatalf("eval %q: %v", expr, err)
	}
	return v
}

// overflowJS reports the page width and the elements that stick out past vw
// pixels outside any scroll container. vw must be the emulated width, not
// window.innerWidth: on a phone Chrome zooms out to fit wide content, which
// grows innerWidth along with the page.
const overflowJS = `((vw) => {
  const clipped = el => {
    for (let p = el.parentElement; p && p !== document.body; p = p.parentElement) {
      if (/(auto|scroll|hidden|clip)/.test(getComputedStyle(p).overflowX)) return true;
    }
    return false;
  };
  const over = [...document.querySelectorAll('body *')].filter(e => {
    if (clipped(e) || getComputedStyle(e).position === 'fixed') return false;
    const r = e.getBoundingClientRect();
    if (r.width === 0) return false;
    // Sticks out itself, or its text runs past its own box.
    return r.right > vw + 1 || (getComputedStyle(e).overflowX === 'visible' && e.scrollWidth > e.clientWidth + 1 && r.left + e.scrollWidth > vw + 1);
  }).slice(0, 6).map(e => e.tagName.toLowerCase() + (typeof e.className === 'string' && e.className.trim() ? '.' + e.className.trim().split(/\s+/).join('.') : '') + ' right=' + Math.round(e.getBoundingClientRect().right));
  return {scrollWidth: document.documentElement.scrollWidth, viewport: vw, over};
})(%d)`

type overflowResult struct {
	ScrollWidth int      `json:"scrollWidth"`
	Viewport    int      `json:"viewport"`
	Over        []string `json:"over"`
}

func (f *fixture) pages() []string {
	return []string{
		"/wiki/Home",
		"/wiki/Wide_Test",
		"/edit/Wide_Test",
		"/edit/Brand_New_Page",
		"/history/Wide_Test",
		fmt.Sprintf("/history-diff/Wide_Test?from=%s&to=%s", f.fromHash, f.to),
		"/share/Wide_Test",
		"/search?q=pump",
		"/pages",
		"/recent-edits",
		"/images",
		"/graph",
		"/notes",
		"/secrets",
		"/skills",
		"/skills/Pump_Check",
		"/share-skill/Pump_Check",
		"/docs/usage",
		"/scan",
		"/scan?page=Wide_Test",
		"/public/" + f.shareToken,
		"/upload/not-a-valid-token",
	}
}

func TestPagesFitAndHaveNoErrors(t *testing.T) {
	f := newFixture(t)
	b := newBrowser(t)
	shots := os.Getenv("UI_SCREENSHOTS")
	if shots != "" {
		if err := os.MkdirAll(shots, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range f.pages() {
		for _, w := range widths {
			t.Run(fmt.Sprintf("%s@%d", path, w), func(t *testing.T) {
				b.takeErrors()
				b.open(t, f.srv.URL+path, w)
				res := eval[overflowResult](t, b, fmt.Sprintf(overflowJS, w))
				if res.ScrollWidth > res.Viewport {
					t.Errorf("page is %dpx wide in a %dpx viewport; sticking out: %v", res.ScrollWidth, res.Viewport, res.Over)
				}
				if errs := b.takeErrors(); len(errs) > 0 {
					t.Errorf("JavaScript errors: %v", errs)
				}
				if shots != "" {
					name := strings.NewReplacer("/", "_", "?", "_", "&", "_", "=", "-").Replace(strings.TrimPrefix(path, "/"))
					b.screenshot(t, filepath.Join(shots, fmt.Sprintf("%s@%d.jpg", name, w)))
					if w == 390 || w == 1280 {
						b.openScheme(t, f.srv.URL+path, w, "dark")
						b.screenshot(t, filepath.Join(shots, fmt.Sprintf("%s@%d-dark.jpg", name, w)))
					}
				}
			})
		}
	}
}

// screenshot saves a full-page JPEG of the current page.
func (b *browser) screenshot(t *testing.T, path string) {
	t.Helper()
	img, err := chromedp.Run(b.ctx, chromedp.FullScreenshot(90))
	if err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, img, 0o644); err != nil {
		t.Fatal(err)
	}
}

// click clicks the first element matching a CSS selector from JavaScript.
func click(t *testing.T, b *browser, selector string) {
	t.Helper()
	ok := eval[bool](t, b, fmt.Sprintf(`(() => { const el = document.querySelector(%q); if (!el) return false; el.click(); return true; })()`, selector))
	if !ok {
		t.Fatalf("no element %q to click", selector)
	}
	if err := chromedp.Do(b.ctx, chromedp.Sleep(350*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
}

type box struct {
	Display string  `json:"display"`
	Left    float64 `json:"left"`
	Right   float64 `json:"right"`
	Width   float64 `json:"width"`
}

// boxOf returns the computed display and the on-screen box of an element.
func boxOf(t *testing.T, b *browser, selector string) box {
	t.Helper()
	return eval[box](t, b, fmt.Sprintf(`(() => { const el = document.querySelector(%q); if (!el) return {display: "missing"}; const r = el.getBoundingClientRect(); return {display: getComputedStyle(el).display, left: r.left, right: r.right, width: r.width}; })()`, selector))
}

func TestPhoneDrawerAndBottomNav(t *testing.T) {
	f := newFixture(t)
	b := newBrowser(t)

	b.open(t, f.srv.URL+"/notes", 390)
	if nav := boxOf(t, b, ".bottom-nav"); nav.Display == "none" || nav.Display == "missing" {
		t.Fatalf("bottom nav not shown on a phone: %+v", nav)
	}
	if cur := eval[string](t, b, `document.querySelector('.bottom-nav a[aria-current="page"]')?.textContent.trim() || ""`); cur != "Notes" {
		t.Errorf("active bottom-nav item = %q, want Notes", cur)
	}
	if s := boxOf(t, b, ".sidebar"); s.Right > 1 {
		t.Fatalf("drawer visible before opening: %+v", s)
	}
	click(t, b, ".sidebar-toggle")
	if s := boxOf(t, b, ".sidebar"); s.Left < 0 || s.Width < 200 {
		t.Fatalf("drawer did not open: %+v", s)
	}
	if o := boxOf(t, b, ".sidebar-overlay"); o.Display == "none" {
		t.Error("overlay not shown with the drawer open")
	}
	eval[bool](t, b, `window.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape'})) || true`)
	chromedp.Do(b.ctx, chromedp.Sleep(400*time.Millisecond))
	if s := boxOf(t, b, ".sidebar"); s.Right > 1 {
		t.Errorf("Escape did not close the drawer: %+v", s)
	}

	b.open(t, f.srv.URL+"/notes", 1280)
	if nav := boxOf(t, b, ".bottom-nav"); nav.Display != "none" {
		t.Errorf("bottom nav shown on desktop: %+v", nav)
	}
	if s := boxOf(t, b, ".sidebar"); s.Left < 0 || s.Width < 200 {
		t.Errorf("sidebar not visible on desktop: %+v", s)
	}
	if errs := b.takeErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %v", errs)
	}
}

func TestThemeToggleRemembersChoice(t *testing.T) {
	f := newFixture(t)
	b := newBrowser(t)
	theme := func() string {
		return eval[string](t, b, `document.documentElement.getAttribute('data-theme') || ''`)
	}

	b.open(t, f.srv.URL+"/wiki/Home", 1280)
	eval[bool](t, b, `localStorage.removeItem('theme') || true`)
	b.open(t, f.srv.URL+"/wiki/Home", 1280)
	if got := theme(); got == "dark" {
		t.Fatalf("light system scheme starts dark")
	}
	click(t, b, ".topbar .theme-toggle")
	if got := theme(); got != "dark" {
		t.Fatalf("theme after toggle = %q, want dark", got)
	}
	b.open(t, f.srv.URL+"/pages", 1280)
	if got := theme(); got != "dark" {
		t.Errorf("dark choice not kept across pages: %q", got)
	}
	click(t, b, ".topbar .theme-toggle")
	if got := theme(); got != "light" {
		t.Errorf("theme after second toggle = %q, want light", got)
	}

	// Without a saved choice the system setting wins.
	eval[bool](t, b, `localStorage.removeItem('theme') || true`)
	b.openScheme(t, f.srv.URL+"/wiki/Home", 1280, "dark")
	if got := theme(); got != "dark" {
		t.Errorf("dark system scheme gives theme %q", got)
	}
}

func TestPageTabsAndHistoryCompare(t *testing.T) {
	f := newFixture(t)
	b := newBrowser(t)
	tabs := func() string {
		return eval[string](t, b, `[...document.querySelectorAll('.page-tabs a')].map(a => a.textContent.trim()).join(',')`)
	}

	b.open(t, f.srv.URL+"/edit/Brand_New_Page", 390)
	if got := tabs(); got != "Page,Edit" {
		t.Errorf("tabs on a new page = %q, want Page,Edit", got)
	}
	b.open(t, f.srv.URL+"/wiki/Wide_Test", 390)
	if got := tabs(); !strings.Contains(got, "Share") || !strings.Contains(got, "History") {
		t.Errorf("tabs on an existing page = %q", got)
	}

	b.open(t, f.srv.URL+"/history/Wide_Test", 390)
	if disabled := eval[bool](t, b, `document.getElementById('compare-btn').disabled`); disabled {
		t.Fatal("compare button disabled with two revisions selected")
	}
	eval[bool](t, b, `(() => { const to = document.querySelector('input[name="to"]:checked'); const from = document.querySelector('input[name="from"][value="' + to.value + '"]'); from.click(); return true; })()`)
	if disabled := eval[bool](t, b, `document.getElementById('compare-btn').disabled`); !disabled {
		t.Error("compare button enabled with the same revision on both sides")
	}
}

func TestLiveSearch(t *testing.T) {
	f := newFixture(t)
	b := newBrowser(t)
	b.open(t, f.srv.URL+"/search", 390)
	eval[bool](t, b, `(() => { const i = document.querySelector('.search input[type="search"]'); i.value = 'pump'; i.dispatchEvent(new Event('input', {bubbles: true})); return true; })()`)
	if err := chromedp.Do(b.ctx, chromedp.Sleep(1200*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if got := eval[string](t, b, `document.getElementById('search-results').textContent`); !strings.Contains(got, "Wide Test") {
		t.Errorf("live search results do not list the page: %q", strings.TrimSpace(got))
	}
	if errs := b.takeErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %v", errs)
	}
}

func TestRestoreFromHistory(t *testing.T) {
	f := newFixture(t)
	b := newBrowser(t)

	b.open(t, f.srv.URL+"/history/Wide_Test", 390)
	if got := eval[string](t, b, `document.querySelector('.history-restore')?.textContent.trim() || ''`); got != "Current" {
		t.Errorf("newest revision cell = %q, want Current", got)
	}
	// Accept the confirmation, then restore the second-newest revision ("edit 1").
	eval[bool](t, b, `(window.confirm = () => true, true)`)
	click(t, b, ".history-restore-btn")
	if err := chromedp.Do(b.ctx, chromedp.Sleep(800*time.Millisecond), chromedp.WaitReady(chromedp.CSS("body"))); err != nil {
		t.Fatal(err)
	}
	if path := eval[string](t, b, `location.pathname`); path != "/wiki/Wide_Test" {
		t.Fatalf("after restore the browser is on %q, want the page", path)
	}
	text := eval[string](t, b, `document.querySelector('.wiki-content').textContent`)
	if !strings.Contains(text, "edit 1") || strings.Contains(text, "edit 2") {
		t.Errorf("page was not restored to the previous version: %q", text[max(0, len(text)-40):])
	}

	b.open(t, f.srv.URL+"/history/Wide_Test", 1280)
	if msg := eval[string](t, b, `document.querySelector('.history-message').firstChild.textContent.trim()`); !strings.HasPrefix(msg, "wiki: restore page Wide_Test to ") {
		t.Errorf("newest history entry = %q, want the restore", msg)
	}
	if errs := b.takeErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %v", errs)
	}
}

// onScreen reports whether an element is fully inside the viewport and is the
// topmost element at its centre (not hidden behind something else).
func onScreen(t *testing.T, b *browser, selector string) bool {
	t.Helper()
	return eval[bool](t, b, fmt.Sprintf(`(() => {
  const el = document.querySelector(%q);
  if (!el) return false;
  const r = el.getBoundingClientRect();
  if (r.width === 0 || r.top < 0 || r.left < 0 || r.bottom > innerHeight + 1 || r.right > innerWidth + 1) return false;
  const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
  return !!hit && (hit === el || el.contains(hit));
})()`, selector))
}

func TestDialogsFitTheScreen(t *testing.T) {
	f := newFixture(t)
	b := newBrowser(t)

	type dialog struct {
		name, page, open string // open: JavaScript that opens the dialog
		buttons            []string
		lastField          string // must be reachable by scrolling the dialog
	}
	dialogs := []dialog{
		{"new secret", "/secrets", `document.getElementById('secret-add-btn').click()`,
			[]string{"#secret-submit", "#secret-cancel", "#secret-dialog-close"}, "#secret-field-description"},
		{"encryption", "/wiki/Home", `document.getElementById('secure-lock-btn').click()`,
			[]string{"#secure-lock-submit", "#secure-lock-cancel", "#secure-lock-close"}, ""},
		{"image picker", "/edit/Wide_Test", `window.dispatchEvent(new CustomEvent('image-picker-open'))`,
			[]string{".image-picker-close"}, ""},
		{"table editor", "/edit/Wide_Test", `document.getElementById('table-editor-toggle').click()`,
			[]string{"#table-editor-apply", "#table-editor-cancel", "#table-editor-close"}, ""},
		{"mediawiki import", "/edit/Wide_Test", `document.getElementById('mediawiki-import-toggle').click()`,
			[]string{"#mw-import-convert", "#mw-import-cancel", "#mw-import-close"}, ""},
	}
	sizes := []struct{ w, h int64 }{{390, 640}, {360, 560}, {1280, 700}}
	for _, d := range dialogs {
		for _, sz := range sizes {
			t.Run(fmt.Sprintf("%s@%dx%d", d.name, sz.w, sz.h), func(t *testing.T) {
				b.takeErrors()
				b.openSized(t, f.srv.URL+d.page, sz.w, sz.h, "light")
				eval[bool](t, b, d.open+`, true`)
				if err := chromedp.Do(b.ctx, chromedp.Sleep(400*time.Millisecond)); err != nil {
					t.Fatal(err)
				}
				for _, sel := range d.buttons {
					if !onScreen(t, b, sel) {
						t.Errorf("%s is not fully visible", sel)
					}
				}
				if d.lastField != "" {
					eval[bool](t, b, fmt.Sprintf(`(document.querySelector(%q).scrollIntoView({block: 'nearest'}), true)`, d.lastField))
					if err := chromedp.Do(b.ctx, chromedp.Sleep(200*time.Millisecond)); err != nil {
						t.Fatal(err)
					}
					if !onScreen(t, b, d.lastField) {
						t.Errorf("%s cannot be scrolled into view", d.lastField)
					}
				}
				if errs := b.takeErrors(); len(errs) > 0 {
					t.Errorf("JavaScript errors: %v", errs)
				}
				if dir := os.Getenv("UI_SCREENSHOTS"); dir != "" {
					img, err := chromedp.Run(b.ctx, chromedp.CaptureScreenshot())
					if err != nil {
						t.Fatal(err)
					}
					name := strings.ReplaceAll(d.name, " ", "-")
					if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("dialog-%s@%dx%d.png", name, sz.w, sz.h)), img, 0o644); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestCardActionsVisibleOnPhones(t *testing.T) {
	f := newFixture(t)
	b := newBrowser(t)
	visible := func(selector string) bool {
		return eval[bool](t, b, fmt.Sprintf(`(() => { const el = document.querySelector(%q); if (!el) return false; let o = 1; for (let e = el; e; e = e.parentElement) o *= parseFloat(getComputedStyle(e).opacity); return o > 0.9 && el.getBoundingClientRect().width > 0; })()`, selector))
	}
	b.open(t, f.srv.URL+"/notes", 390)
	for _, sel := range []string{".note-card .note-archive", ".note-card .note-delete"} {
		if !visible(sel) {
			t.Errorf("%s not visible on a phone", sel)
		}
	}
	b.open(t, f.srv.URL+"/secrets", 390)
	for _, sel := range []string{".secret-reveal", ".secret-copy", ".secret-edit", ".secret-delete"} {
		if !visible(sel) {
			t.Errorf("%s not visible on a phone", sel)
		}
	}
	// No emoji or text glyphs left as button faces.
	for _, page := range []string{"/notes", "/secrets", "/images", "/share/Wide_Test"} {
		b.open(t, f.srv.URL+page, 390)
		if bad := eval[string](t, b, `[...document.querySelectorAll('button')].map(x => x.textContent.trim()).filter(s => /[\u{1F300}-\u{1FAFF}✕✎×]/u.test(s)).join(' ')`); bad != "" {
			t.Errorf("%s: emoji button faces left: %q", page, bad)
		}
	}
}

func TestSecretTagFilter(t *testing.T) {
	f := newFixture(t)
	b := newBrowser(t)
	b.open(t, f.srv.URL+"/secrets", 1280)
	shown := func() string {
		return eval[string](t, b, `[...document.querySelectorAll('.secret-card:not([hidden]) .secret-title')].map(e => e.textContent).join(',')`)
	}
	if got := eval[string](t, b, `[...document.querySelectorAll('#secret-tag-bar .secret-tag')].map(e => e.textContent).join(',')`); got != "home,network,work" {
		t.Fatalf("tag bar = %q", got)
	}
	click(t, b, `#secret-tag-bar .secret-tag[data-tag="work"]`)
	if got := shown(); got != "Mail" {
		t.Errorf("after selecting work, shown = %q", got)
	}
	// A tag on a card toggles the same filter; work and network together match nothing.
	click(t, b, `.secret-card .secret-tag[data-tag="work"]`)
	click(t, b, `#secret-tag-bar .secret-tag[data-tag="network"]`)
	if got := shown(); got != "Home router" {
		t.Errorf("after swapping to network, shown = %q", got)
	}
	click(t, b, `#secret-tag-bar .secret-tag[data-tag="work"]`)
	if got := shown(); got != "" || !eval[bool](t, b, `!document.getElementById('secrets-empty').hidden`) {
		t.Errorf("work+network shown = %q, want none and the empty message", got)
	}

	// Editing tags while locked saves them and refreshes the tag bar.
	click(t, b, `#secret-tag-bar .secret-tag[data-tag="work"]`)
	click(t, b, `#secret-tag-bar .secret-tag[data-tag="network"]`)
	click(t, b, `.secret-card[data-title="Mail"] .secret-edit`)
	eval[bool](t, b, `(() => { document.getElementById('secret-field-tags').value = 'Work, Mail Server'; return true; })()`)
	click(t, b, `#secret-submit`)
	if got := eval[string](t, b, `[...document.querySelectorAll('#secret-tag-bar .secret-tag')].map(e => e.textContent).join(',')`); got != "home,mail-server,network,work" {
		t.Errorf("tag bar after edit = %q", got)
	}
	if errs := b.takeErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %v", errs)
	}
}

func TestScanResultOffersParents(t *testing.T) {
	f := newFixture(t)
	b := newBrowser(t)

	var photo bytes.Buffer
	if err := png.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 64, 48))); err != nil {
		t.Fatal(err)
	}
	id := f.scanner.Start(photo.Bytes(), "", "", "tester")
	for i := 0; i < 100; i++ {
		if job, _ := f.scanner.Job(id); job.Status != "running" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The preview comes first; nothing is saved until Save.
	b.open(t, f.srv.URL+"/scan/"+id, 390)
	if !eval[bool](t, b, `!!document.querySelector('.scan-draft p')`) {
		t.Fatal("the scan preview is not shown")
	}
	if res := eval[overflowResult](t, b, fmt.Sprintf(overflowJS, 390)); res.ScrollWidth > 390 {
		t.Errorf("preview page is %dpx wide: %v", res.ScrollWidth, res.Over)
	}
	if dir := os.Getenv("UI_SCREENSHOTS"); dir != "" {
		b.screenshot(t, filepath.Join(dir, "scan-preview@390.jpg"))
	}
	click(t, b, `form[action$="/save"] button`)
	if err := chromedp.Do(b.ctx, chromedp.Sleep(500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	offered := eval[[]string](t, b, `[...document.querySelectorAll('.scan-parent')].map(b => b.value)`)
	if len(offered) == 0 || offered[0] != "Wide_Test" {
		t.Fatalf("offered parents = %v, want Wide_Test (it mentions the pump) first", offered)
	}
	if res := eval[overflowResult](t, b, fmt.Sprintf(overflowJS, 390)); res.ScrollWidth > 390 {
		t.Errorf("result page is %dpx wide: %v", res.ScrollWidth, res.Over)
	}
	if dir := os.Getenv("UI_SCREENSHOTS"); dir != "" {
		b.screenshot(t, filepath.Join(dir, "scan-result@390.jpg"))
	}
	click(t, b, `.scan-parent[value="Wide_Test"]`)
	if err := chromedp.Do(b.ctx, chromedp.Sleep(500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if got := eval[string](t, b, `document.querySelector('.scan-linked')?.textContent.trim() || ''`); !strings.Contains(got, "Wide_Test") {
		t.Errorf("after choosing, the page says %q", got)
	}
	b.open(t, f.srv.URL+"/wiki/Wide_Test", 1280)
	if !eval[bool](t, b, `!!document.querySelector('.wiki-content a[href^="/wiki/Pump_wiring"]')`) {
		t.Error("Wide_Test does not link the scanned page")
	}
	if errs := b.takeErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %v", errs)
	}
}
