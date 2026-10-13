// Package web embeds the wiki's HTML templates and static assets into the
// binary, so the server can run without the web/ directory present on disk.
package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
)

//go:embed static templates
var files embed.FS

// Static returns the embedded /static/ tree (CSS, JS).
func Static() fs.FS {
	sub, err := fs.Sub(files, "static")
	if err != nil {
		panic(err)
	}
	return sub
}

// Templates returns the embedded /templates/ tree (HTML templates).
func Templates() fs.FS {
	sub, err := fs.Sub(files, "templates")
	if err != nil {
		panic(err)
	}
	return sub
}

var (
	hashOnce   sync.Once
	assetVer   string            // hash over all static files
	assetETags map[string]string // static path → quoted ETag
)

// hashStatic computes a content hash per static file and one over all of them.
func hashStatic() {
	assetETags = make(map[string]string)
	var names []string
	_ = fs.WalkDir(Static(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(Static(), p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		assetETags[p] = `"` + hex.EncodeToString(sum[:8]) + `"`
		names = append(names, p)
		return nil
	})
	sort.Strings(names)
	all := sha256.New()
	for _, n := range names {
		all.Write([]byte(n + assetETags[n]))
	}
	assetVer = hex.EncodeToString(all.Sum(nil))[:10]
}

// AssetVersion is a short hash of all static files. It changes whenever any
// of them does, so URLs carrying it never serve a stale cached copy.
func AssetVersion() string {
	hashOnce.Do(hashStatic)
	return assetVer
}

// StaticHandler serves the static files; mount it under /static/ with
// http.StripPrefix. Requests with a ?v= version are cached for a year, since
// their content never changes; others must be revalidated and get an ETag,
// so an unchanged file costs only a 304.
func StaticHandler() http.Handler {
	hashOnce.Do(hashStatic)
	files := http.FileServerFS(Static())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tag, ok := assetETags[strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")]; ok {
			w.Header().Set("ETag", tag)
		}
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
