package wiki

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"testing"
)

// tinyPNG is a 1x1 transparent PNG used as a stand-in site image.
var tinyPNG = mustBase64("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")

func mustBase64(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// siteImageServer serves an HTML page with the given <head> markup plus a PNG
// at every other path that the test opts into.
func siteImageServer(t *testing.T, head string, imagePaths map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if ct, ok := imagePaths[r.URL.Path]; ok {
			w.Header().Set("Content-Type", ct)
			_, _ = w.Write(tinyPNG)
			return
		}
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><html><head>" + head + "</head><body>hi</body></html>"))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestFetchSiteImagePrefersOpenGraph(t *testing.T) {
	ts := siteImageServer(t,
		`<link rel="icon" href="/favicon.ico">`+
			`<meta property="og:image" content="/og.png">`,
		map[string]string{"/og.png": "image/png", "/favicon.ico": "image/x-icon"},
	)

	img, err := FetchSiteImage(context.Background(), ts.Client(), ts.URL)
	if err != nil {
		t.Fatalf("FetchSiteImage: %v", err)
	}
	if img.Ext != ".png" {
		t.Errorf("ext = %q, want .png", img.Ext)
	}
	if len(img.Data) != len(tinyPNG) {
		t.Errorf("got %d bytes, want %d", len(img.Data), len(tinyPNG))
	}
}

func TestFetchSiteImagePriorityOrder(t *testing.T) {
	// Only the apple-touch-icon actually resolves; og:image 404s, so the
	// fetcher must fall through instead of giving up on the first candidate.
	ts := siteImageServer(t,
		`<meta property="og:image" content="/missing.png">`+
			`<link rel="apple-touch-icon" href="/touch.png">`,
		map[string]string{"/touch.png": "image/png"},
	)
	img, err := FetchSiteImage(context.Background(), ts.Client(), ts.URL)
	if err != nil {
		t.Fatalf("FetchSiteImage: %v", err)
	}
	if img.Ext != ".png" {
		t.Errorf("ext = %q", img.Ext)
	}
}

func TestFetchSiteImageFallsBackToFavicon(t *testing.T) {
	ts := siteImageServer(t, "", map[string]string{"/favicon.ico": "image/x-icon"})
	img, err := FetchSiteImage(context.Background(), ts.Client(), ts.URL)
	if err != nil {
		t.Fatalf("FetchSiteImage: %v", err)
	}
	if img.Ext != ".ico" {
		t.Errorf("ext = %q, want .ico", img.Ext)
	}
}

// A site advertising an HTML page or a text file as its image must be rejected
// rather than stored as a broken image.
func TestFetchSiteImageRejectsNonImage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/notanimage", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("definitely not an image"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<head><meta property="og:image" content="/notanimage"></head>`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	if _, err := FetchSiteImage(context.Background(), ts.Client(), ts.URL); err == nil {
		t.Fatal("expected an error for a non-image response")
	}
}

func TestFetchSiteImageRejectsBadURL(t *testing.T) {
	for _, u := range []string{"", "ftp://example.com/x", "file:///etc/passwd", "not a url", "https://"} {
		if _, err := FetchSiteImage(context.Background(), http.DefaultClient, u); err == nil {
			t.Errorf("FetchSiteImage(%q) succeeded, want error", u)
		}
	}
}

func TestParseSiteImageLinksResolvesRelative(t *testing.T) {
	base := mustParseURL(t, "https://example.com/app/page")
	html := `<head>
	  <meta name="twitter:image" content="../tw.png">
	  <meta property="og:image" content="//cdn.example.com/og.png">
	  <link rel="apple-touch-icon" href="/touch.png">
	  <link rel="icon" href="data:image/png;base64,AAAA">
	</head><body></body>`

	got := parseSiteImageLinks(strings.NewReader(html), base)
	want := []string{
		"https://cdn.example.com/og.png",
		"https://example.com/tw.png",
		"https://example.com/touch.png",
	}
	if len(got) != len(want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("candidate %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNormalizeContentType(t *testing.T) {
	if got := normalizeContentType("Image/PNG; charset=utf-8"); got != "image/png" {
		t.Errorf("normalizeContentType = %q", got)
	}
}

func mustParseURL(t *testing.T, raw string) *neturl.URL {
	t.Helper()
	u, err := neturl.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}
