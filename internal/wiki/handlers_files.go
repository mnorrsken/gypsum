package wiki

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// maxFilesPerUpload caps how many files one upload request may carry.
const maxFilesPerUpload = 10

// SetUploadConfig sets upload limits and photo resizing.
func (h *Handler) SetUploadConfig(cfg UploadConfig) {
	h.uploadCfg = cfg
}

// parseUploadForm reads a multipart upload body, bounded so a request can
// carry at most maxFilesPerUpload files of the configured size.
func (h *Handler) parseUploadForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, h.uploadCfg.MaxBytes*maxFilesPerUpload+1<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return uploadErrorf("upload is too large")
		}
		return uploadErrorf("invalid upload form")
	}
	return nil
}

// saveUploads stores every file in the multipart field, commits them, and
// when page is set appends their Markdown to that page. Files saved before
// an error are kept and still added to the page.
func (h *Handler) saveUploads(r *http.Request, field, page, caption, author string, imagesOnly bool) ([]StoredFile, error) {
	headers := r.MultipartForm.File[field]
	if len(headers) == 0 {
		return nil, uploadErrorf("no file in upload")
	}
	if len(headers) > maxFilesPerUpload {
		return nil, uploadErrorf("at most %d files per upload", maxFilesPerUpload)
	}
	if page != "" {
		if _, err := h.store.Load(KindPage, page); err != nil {
			return nil, uploadErrorf("page not found: %s", page)
		}
	}

	var saved []StoredFile
	var saveErr error
	for _, fh := range headers {
		if imagesOnly && !uploadKinds[strings.ToLower(filepath.Ext(fh.Filename))].image {
			saveErr = uploadErrorf("unsupported image type")
			break
		}
		f, err := fh.Open()
		if err != nil {
			saveErr = fmt.Errorf("opening upload: %w", err)
			break
		}
		data, err := readUpload(f, h.uploadCfg.MaxBytes)
		f.Close()
		if err != nil {
			saveErr = err
			break
		}
		sf, err := h.store.SaveUpload(fh.Filename, data, h.uploadCfg, caption)
		if err != nil {
			saveErr = fmt.Errorf("%s: %w", fh.Filename, err)
			break
		}
		if sf.Kind == "image" {
			_ = h.autoCommit.CommitImageSave(sf.Name, author)
		} else {
			_ = h.autoCommit.CommitFileSave(sf.Name, author)
			go h.store.IndexFile(sf.Name)
		}
		saved = append(saved, sf)
	}

	if page != "" && len(saved) > 0 {
		snippets := make([]string, len(saved))
		for i, sf := range saved {
			snippets[i] = sf.Markdown
		}
		if err := h.store.AppendToPage(page, strings.Join(snippets, "\n\n")); err != nil {
			return saved, fmt.Errorf("adding uploads to page %s: %w", page, err)
		}
		_ = h.autoCommit.CommitSave(KindPage, page, author)
	}
	return saved, saveErr
}

// uploadStatus maps an upload error to an HTTP status.
func uploadStatus(err error) int {
	var ue *UploadError
	if errors.As(err, &ue) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// writeUploadResult answers a JSON upload request.
func (h *Handler) writeUploadResult(w http.ResponseWriter, saved []StoredFile, err error) {
	if err != nil {
		if uploadStatus(err) == http.StatusInternalServerError {
			log.Printf("upload: %v", err)
		}
		h.writeJSON(w, uploadStatus(err), map[string]any{"ok": false, "error": err.Error(), "files": saved})
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": saved})
}

// handleFileUpload serves POST /files/upload for the web editor: one or more
// files in the "file" field.
func (h *Handler) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	if err := h.parseUploadForm(w, r); err != nil {
		h.writeUploadResult(w, nil, err)
		return
	}
	saved, err := h.saveUploads(r, "file", "", "", UsernameFromRequest(r), false)
	h.writeUploadResult(w, saved, err)
}

// handleFileDelete serves POST /files/delete for the web file list.
func (h *Handler) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	filename := strings.TrimSpace(r.FormValue("filename"))
	if !validUploadName(filename) {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}
	if err := os.Remove(filepath.Join(h.store.FilesDir(), filename)); err != nil {
		http.Error(w, "failed to delete file", http.StatusInternalServerError)
		return
	}
	_ = h.autoCommit.CommitFileDelete(filename, UsernameFromRequest(r))
	h.store.RemoveFileIndex(filename)

	if isHTMX(r) {
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/images", http.StatusFound)
}

// handleFileServe serves GET /files/{name}.
func (h *Handler) handleFileServe(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validUploadName(name) {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(h.store.FilesDir(), name)
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	serveUploadedFile(w, r, path)
}

// uploadPageData feeds the standalone upload page.
type uploadPageData struct {
	Page    string
	Expires string
	Accept  string
	MaxMB   int64
	Resize  bool // photos are scaled and stripped of EXIF
	Error   string
	Saved   []StoredFile
	Invalid bool // token unknown, used or expired
}

// handleUploadLink serves GET and POST /upload/{token}: a phone-friendly
// form for an upload link minted by the create_upload_link MCP tool.
func (h *Handler) handleUploadLink(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	data := uploadPageData{Accept: uploadAccept(), MaxMB: h.uploadCfg.MaxBytes >> 20, Resize: h.uploadCfg.ImageMaxPx > 0}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")

	switch r.Method {
	case http.MethodGet:
		link, ok := h.uploads.Get(token)
		if !ok {
			data.Invalid = true
			h.renderUploadPage(w, http.StatusNotFound, data)
			return
		}
		data.Page = link.Page
		data.Expires = link.Expires.Format("15:04")
		h.renderUploadPage(w, http.StatusOK, data)

	case http.MethodPost:
		link, ok := h.uploads.Consume(token)
		if !ok {
			data.Invalid = true
			h.renderUploadPage(w, http.StatusNotFound, data)
			return
		}
		data.Page = link.Page
		data.Expires = link.Expires.Format("15:04")
		if err := h.parseUploadForm(w, r); err != nil {
			h.uploads.Restore(token, link)
			data.Error = err.Error()
			h.renderUploadPage(w, uploadStatus(err), data)
			return
		}
		saved, err := h.saveUploads(r, "file", link.Page, r.FormValue("caption"), link.Author, false)
		data.Saved = saved
		if err != nil {
			if len(saved) == 0 && time.Now().Before(link.Expires) {
				h.uploads.Restore(token, link)
			}
			data.Error = err.Error()
			h.renderUploadPage(w, uploadStatus(err), data)
			return
		}
		h.renderUploadPage(w, http.StatusOK, data)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) renderUploadPage(w http.ResponseWriter, status int, data uploadPageData) {
	tmpl := h.tmplCache["upload"]
	if tmpl == nil {
		http.Error(w, "template not found: upload", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := tmpl.ExecuteTemplate(w, "upload", data); err != nil {
		log.Printf("upload page: %v", err)
	}
}

// uploadAccept lists accepted extensions for a file input's accept attribute.
func uploadAccept() string {
	var exts []string
	for ext, k := range uploadKinds {
		if !k.image {
			exts = append(exts, ext)
		}
	}
	sort.Strings(exts)
	return strings.Join(append([]string{"image/*"}, exts...), ",")
}
