package wiki

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

func (h *Handler) handleShare(w http.ResponseWriter, r *http.Request) {
	store := h.storeFor(r)
	db := h.dbFor(r)
	prefix := urlPrefix(r)

	slug := strings.TrimPrefix(r.URL.Path, "/share/")
	if slug == "" {
		http.Error(w, "missing page slug", http.StatusBadRequest)
		return
	}

	page, err := store.Load(KindPage, slug)
	if err != nil {
		if errors.Is(err, ErrPageNotFound) {
			http.Error(w, "page not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	title := page.Title
	if h1, _ := ExtractH1Title(page.Content); h1 != "" {
		title = h1
	}

	switch r.Method {
	case http.MethodGet:
		data := TemplateData{
			Title: "Share: " + title,
			Page:  &Page{Slug: slug, Title: title},
		}
		if db != nil {
			if share, err := db.GetShare(slug); err == nil && share != nil {
				data.ShareToken = share.Token
				data.ShareURL = buildShareURL(r, share.Token)
				data.ShareCreated = share.CreatedAt.Format("2006-01-02 15:04")
			}
		}
		h.render(w, r, "share", data)

	case http.MethodPost:
		if db == nil {
			http.Error(w, "sharing not available", http.StatusInternalServerError)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch r.FormValue("action") {
		case "create", "regenerate":
			if _, err := db.CreateShare(slug); err != nil {
				http.Error(w, "failed to create share link", http.StatusInternalServerError)
				return
			}
		case "revoke":
			if err := db.DeleteShare(slug); err != nil {
				http.Error(w, "failed to revoke share link", http.StatusInternalServerError)
				return
			}
		}
		http.Redirect(w, r, prefix+"/share/"+slug, http.StatusFound)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handlePublic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := strings.TrimPrefix(r.URL.Path, "/public/")
	if token == "" {
		http.NotFound(w, r)
		return
	}

	if h.db == nil {
		http.NotFound(w, r)
		return
	}

	share, err := h.db.GetShareByToken(token)
	if err != nil || share == nil {
		http.NotFound(w, r)
		return
	}

	page, err := h.store.Load(KindPage, share.Slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	displayTitle := page.Title
	renderSource := page.Content
	if h1, rest := ExtractH1Title(page.Content); h1 != "" {
		displayTitle = h1
		renderSource = rest
	}

	rendered, err := h.renderer.RenderPublic(renderSource)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	tmpl := h.tmplCache["public"]
	if tmpl == nil {
		http.Error(w, "template not found: public", http.StatusInternalServerError)
		return
	}

	data := TemplateData{
		Title:        displayTitle,
		RenderedHTML: rendered,
	}
	if err := tmpl.ExecuteTemplate(w, "public", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// buildShareURL constructs the full public URL for a share token from the request.
func buildShareURL(r *http.Request, token string) string {
	scheme := "http"
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	} else if r.TLS != nil {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/public/%s", scheme, r.Host, token)
}
