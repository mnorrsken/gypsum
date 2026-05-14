// Package web embeds the wiki's HTML templates and static assets into the
// binary, so the server can run without the web/ directory present on disk.
package web

import (
	"embed"
	"io/fs"
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
