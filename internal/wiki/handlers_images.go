package wiki

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (h *Handler) handleImages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	store := h.storeFor(r)
	images, err := store.ListImages()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.render(w, r, "images", TemplateData{
		Title:  "Images",
		Images: images,
	})
}

func (h *Handler) handleImageUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	store := h.storeFor(r)
	autoCommit := h.autoCommitFor(r)
	prefix := urlPrefix(r)

	if err := r.ParseMultipartForm(10 << 20); err != nil { // 10 MB max
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "file too large or invalid"})
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "missing image"})
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowedExt := map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".svg": true}
	if !allowedExt[ext] {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "unsupported image type"})
		return
	}

	// Sniff actual content type from the first 512 bytes.
	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	contentType := http.DetectContentType(buf[:n])
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "failed to read file"})
		return
	}
	allowedMIME := map[string]bool{
		"image/png":     true,
		"image/jpeg":    true,
		"image/gif":     true,
		"image/webp":    true,
		"image/svg+xml": true,
		"text/xml":      true, // SVGs may be detected as text/xml
	}
	if !allowedMIME[contentType] {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "file content does not match an allowed image type"})
		return
	}

	// Generate unique filename: {original-name}-{YYYYMMDD}-{8hexrandom}{ext}
	// or {YYYYMMDD}-{8hexrandom}{ext} when the original name is generic/absent.
	randBytes := make([]byte, 4)
	_, _ = rand.Read(randBytes)
	namePart := sanitizeImageBasename(header.Filename)
	datePart := time.Now().Format("20060102")
	shortRand := hex.EncodeToString(randBytes) // 8 hex chars
	var filename string
	if namePart != "" {
		filename = fmt.Sprintf("%s-%s-%s%s", namePart, datePart, shortRand, ext)
	} else {
		filename = fmt.Sprintf("%s-%s%s", datePart, shortRand, ext)
	}

	dstPath := filepath.Join(store.ImagesDir(), filename)
	dst, err := os.Create(dstPath)
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "failed to save image"})
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "failed to write image"})
		return
	}

	_ = autoCommit.CommitImageSave(filename, UsernameFromRequest(r))

	imgURL := prefix + "/images/" + filename
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "url": imgURL, "filename": filename})
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

	store := h.storeFor(r)
	images, err := store.ListImages()
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

	store := h.storeFor(r)
	autoCommit := h.autoCommitFor(r)
	prefix := urlPrefix(r)

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	filename := strings.TrimSpace(r.FormValue("filename"))
	if filename == "" || strings.Contains(filename, "/") || strings.Contains(filename, "..") {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}

	if err := os.Remove(filepath.Join(store.ImagesDir(), filename)); err != nil {
		http.Error(w, "failed to delete image", http.StatusInternalServerError)
		return
	}

	_ = autoCommit.CommitImageDelete(filename, UsernameFromRequest(r))

	if isHTMX(r) {
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, prefix+"/images", http.StatusFound)
}
