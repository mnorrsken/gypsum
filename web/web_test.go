package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStaticHandlerCaching(t *testing.T) {
	h := http.StripPrefix("/static/", StaticHandler())
	get := func(url, ifNoneMatch string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", ifNoneMatch)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := get("/static/style.css", "")
	etag := rec.Header().Get("ETag")
	if rec.Code != http.StatusOK || etag == "" || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("unversioned: status %d, ETag %q, Cache-Control %q", rec.Code, etag, rec.Header().Get("Cache-Control"))
	}
	if rec := get("/static/style.css", etag); rec.Code != http.StatusNotModified {
		t.Errorf("revalidation with the ETag: status %d, want 304", rec.Code)
	}

	rec = get("/static/style.css?v="+AssetVersion(), "")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("versioned: status %d, Cache-Control %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if v := AssetVersion(); len(v) != 10 {
		t.Errorf("AssetVersion = %q", v)
	}
}
