package wiki

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (h *Handler) handleImages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	images, err := h.store.ListImages()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	files, err := h.store.ListFiles()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.render(w, "images", TemplateData{
		Title:  "Files",
		Images: images,
		Files:  files,
	})
}

func (h *Handler) handleImageUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := h.parseUploadForm(w, r); err != nil {
		h.writeJSON(w, uploadStatus(err), map[string]any{"ok": false, "error": err.Error()})
		return
	}
	saved, err := h.saveUploads(r, "image", "", "", UsernameFromRequest(r), true)
	if err != nil {
		h.writeJSON(w, uploadStatus(err), map[string]any{"ok": false, "error": err.Error()})
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "url": saved[0].URL, "filename": saved[0].Name})
}

// sanitizeImageBasename converts an original filename (without extension) into
// a lowercase hyphen-separated slug suitable for use in a stored filename.
// Returns "" for generic names like "image" or "pasted-image".
func sanitizeImageBasename(filename string) string {
	// Strip extension
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	base = strings.ToLower(base)

	var sb strings.Builder
	prevHyphen := false
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
			prevHyphen = false
		} else if sb.Len() > 0 && !prevHyphen {
			sb.WriteRune('-')
			prevHyphen = true
		}
	}
	result := strings.TrimRight(sb.String(), "-")

	// Ignore generic fallback names produced by browsers / our own JS
	switch result {
	case "", "image", "pasted-image", "screenshot":
		return ""
	}

	const maxLen = 48
	if len(result) > maxLen {
		result = strings.TrimRight(result[:maxLen], "-")
	}
	return result
}

func (h *Handler) handleImageList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}

	images, err := h.store.ListImages()
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	if isHTMX(r) {
		tmpl := h.tmplCache["partial_image_grid"]
		if tmpl == nil {
			http.Error(w, "template not found", http.StatusInternalServerError)
			return
		}
		if err := tmpl.ExecuteTemplate(w, "image_grid", TemplateData{Images: images}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	type imgItem struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	items := make([]imgItem, len(images))
	for i, img := range images {
		items[i] = imgItem{Name: img.Name, URL: img.URL}
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "images": items})
}

func (h *Handler) handleImageDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	filename := strings.TrimSpace(r.FormValue("filename"))
	if filename == "" || strings.Contains(filename, "/") || strings.Contains(filename, "..") {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}

	if err := os.Remove(filepath.Join(h.store.ImagesDir(), filename)); err != nil {
		http.Error(w, "failed to delete image", http.StatusInternalServerError)
		return
	}

	_ = h.autoCommit.CommitImageDelete(filename, UsernameFromRequest(r))

	if isHTMX(r) {
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/images", http.StatusFound)
}
