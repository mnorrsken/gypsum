package wiki

import (
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

// scanImageExts are the photo formats the scanner decodes.
var scanImageExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}

// SetScanner enables the photo scan pages; nil leaves them disabled.
func (h *Handler) SetScanner(s *Scanner) {
	h.scanner = s
}

// handleScanForm serves GET /scan: the capture screen. ?page=Slug adds the
// scan to an existing page.
func (h *Handler) handleScanForm(w http.ResponseWriter, r *http.Request) {
	data := TemplateData{Title: "Scan a photo"}
	if slug := strings.TrimSpace(r.URL.Query().Get("page")); slug != "" {
		page, err := h.store.Load(KindPage, slug)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		page.Title = pageDisplayTitle(page)
		data.Page = page
	}
	h.render(w, "scan", data)
}

// handleScanStart serves POST /scan: it stores nothing yet, only starts a
// background job and redirects to its progress page.
func (h *Handler) handleScanStart(w http.ResponseWriter, r *http.Request) {
	if h.scanner == nil {
		http.Error(w, "scanning is not configured", http.StatusNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.uploadCfg.MaxBytes+1<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "photo is too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid upload form", http.StatusBadRequest)
		return
	}

	var photo []byte
	for _, fh := range r.MultipartForm.File["photo"] {
		if fh.Size == 0 {
			continue // the unused one of the two file inputs
		}
		ext := strings.ToLower(filepath.Ext(fh.Filename))
		if ext == ".heic" || ext == ".heif" {
			http.Error(w, "HEIC photos are not supported; convert to JPEG first", http.StatusBadRequest)
			return
		}
		if !scanImageExts[ext] {
			http.Error(w, "unsupported photo type "+ext, http.StatusBadRequest)
			return
		}
		f, err := fh.Open()
		if err != nil {
			http.Error(w, "failed to read photo", http.StatusInternalServerError)
			return
		}
		photo, err = readUpload(f, h.uploadCfg.MaxBytes)
		f.Close()
		if err != nil {
			http.Error(w, err.Error(), uploadStatus(err))
			return
		}
		break
	}
	if photo == nil {
		http.Error(w, "no photo in upload", http.StatusBadRequest)
		return
	}

	page := strings.TrimSpace(r.FormValue("page"))
	if page != "" {
		if _, err := h.store.Load(KindPage, page); err != nil {
			http.Error(w, "page not found: "+page, http.StatusBadRequest)
			return
		}
	}
	id := h.scanner.Start(photo, page, r.FormValue("note"), UsernameFromRequest(r))
	http.Redirect(w, r, "/scan/"+id, http.StatusSeeOther)
}

// handleScanJob serves GET /scan/{id}: the progress and result of a scan.
// htmx polls it and gets only the status fragment.
func (h *Handler) handleScanJob(w http.ResponseWriter, r *http.Request) {
	if h.scanner == nil {
		http.NotFound(w, r)
		return
	}
	job, ok := h.scanner.Job(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	data := TemplateData{Title: "Scan", ScanJob: &job, FormError: r.URL.Query().Get("error")}
	if job.Status == scanDone && !job.Appended && job.LinkedFrom == "" {
		data.AllPages, _ = h.store.List(KindPage) // for typing another parent page
	}
	if isHTMX(r) {
		tmpl := h.tmplCache["scan_job"]
		if tmpl == nil {
			http.Error(w, "template not found: scan_job", http.StatusInternalServerError)
			return
		}
		if err := tmpl.ExecuteTemplate(w, "scan_status", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	h.render(w, "scan_job", data)
}

// handleScanLink serves POST /scan/{id}/link: link the scanned page from the
// page in the "parent" field (slug or title), then show the result again.
func (h *Handler) handleScanLink(w http.ResponseWriter, r *http.Request) {
	if h.scanner == nil {
		http.NotFound(w, r)
		return
	}
	id := r.PathValue("id")
	if err := h.scanner.LinkFrom(id, r.FormValue("parent")); err != nil {
		http.Redirect(w, r, "/scan/"+id+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/scan/"+id, http.StatusSeeOther)
}
