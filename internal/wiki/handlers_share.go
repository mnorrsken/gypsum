package wiki

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// ogDescriptionMaxRunes caps the text shown in a link preview.
const ogDescriptionMaxRunes = 200

func (h *Handler) handleShare(w http.ResponseWriter, r *http.Request) {
	h.serveShare(w, r, KindPage, "/share/")
}

func (h *Handler) handleShareSkill(w http.ResponseWriter, r *http.Request) {
	h.serveShare(w, r, KindSkill, "/share-skill/")
}

// serveShare shows and changes the share link of the document named by the
// part of the URL path after prefix.
func (h *Handler) serveShare(w http.ResponseWriter, r *http.Request, kind DocKind, prefix string) {
	slug := strings.TrimPrefix(r.URL.Path, prefix)
	if slug == "" {
		http.Error(w, "missing "+kind.Label()+" slug", http.StatusBadRequest)
		return
	}

	page, err := h.store.Load(kind, slug)
	if err != nil {
		if errors.Is(err, ErrPageNotFound) {
			http.Error(w, kind.Label()+" not found", http.StatusNotFound)
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
			Kind:  kind,
		}
		if h.db != nil {
			if share, err := h.db.GetShare(kind, slug); err == nil && share != nil {
				data.ShareToken = share.Token
				data.ShareURL = buildShareURL(r, share.Token)
				data.ShareCreated = share.CreatedAt.Format("2006-01-02 15:04")
			}
		}
		h.render(w, "share", data)

	case http.MethodPost:
		if h.db == nil {
			http.Error(w, "sharing not available", http.StatusInternalServerError)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch r.FormValue("action") {
		case "create", "regenerate":
			if _, err := h.db.CreateShare(kind, slug); err != nil {
				http.Error(w, "failed to create share link", http.StatusInternalServerError)
				return
			}
		case "revoke":
			if err := h.db.DeleteShare(kind, slug); err != nil {
				http.Error(w, "failed to revoke share link", http.StatusInternalServerError)
				return
			}
		}
		http.Redirect(w, r, prefix+slug, http.StatusFound)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handlePublic serves /public/<token>, the shared document, and
// /public/<token>/<image>, an image that document shows.
func (h *Handler) handlePublic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token, image, isImage := strings.Cut(strings.TrimPrefix(r.URL.Path, "/public/"), "/")
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

	page, err := h.store.Load(share.Kind, share.Slug)
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

	content := preparePublicHTML(rendered, "/public/"+token)
	if isImage {
		h.servePublicImage(w, r, content.images, image)
		return
	}

	tmpl := h.tmplCache["public"]
	if tmpl == nil {
		http.Error(w, "template not found: public", http.StatusInternalServerError)
		return
	}

	shareURL := buildShareURL(r, token)
	data := TemplateData{
		Title:         displayTitle,
		RenderedHTML:  content.html,
		OGURL:         shareURL,
		OGDescription: content.description,
	}
	// Link previews do not show SVG, so use the first image they can show.
	for _, name := range content.images {
		if !strings.EqualFold(filepath.Ext(name), ".svg") {
			data.OGImage = shareURL + "/" + url.PathEscape(name)
			break
		}
	}
	if err := tmpl.ExecuteTemplate(w, "public", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// servePublicImage serves an image to anonymous viewers, but only one of the
// images the shared document shows. The rest of /images/ stays behind login.
func (h *Handler) servePublicImage(w http.ResponseWriter, r *http.Request, allowed []string, name string) {
	if !slices.Contains(allowed, name) {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(h.store.ImagesDir(), name)
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	// Revalidate so a revoked link stops working at once. The sandbox stops
	// scripts in an SVG that is opened directly from running on this origin.
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'")
	http.ServeFile(w, r, path)
}

// publicContent is a rendered public document, ready to show to anonymous viewers.
type publicContent struct {
	html        template.HTML // images point below the share link
	images      []string      // names of the uploaded images it shows
	description string        // plain text start of the document, for link previews
}

// preparePublicHTML points the document's uploaded images at base and collects
// the image names and preview text. It works on the output of RenderPublic, so
// images in code blocks do not count and secure fields cannot reach the preview.
func preparePublicHTML(rendered template.HTML, base string) publicContent {
	var content publicContent
	var out, text strings.Builder
	preDepth := 0
	z := html.NewTokenizer(strings.NewReader(string(rendered)))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		raw := string(z.Raw())
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			if tok.Data == "pre" && tt == html.StartTagToken {
				preDepth++
			}
			if tok.Data == "img" {
				for i, attr := range tok.Attr {
					if name, ok := uploadedImageName(attr.Val); ok && attr.Key == "src" {
						tok.Attr[i].Val = base + "/" + url.PathEscape(name)
						content.images = append(content.images, name)
						raw = tok.String()
					}
				}
			}
		case html.EndTagToken:
			if tok := z.Token(); tok.Data == "pre" && preDepth > 0 {
				preDepth--
			}
		case html.TextToken:
			if preDepth == 0 {
				text.WriteString(html.UnescapeString(raw))
			}
		}
		out.WriteString(raw)
	}
	content.html = template.HTML(out.String())
	content.description = previewText(text.String())
	return content
}

// uploadedImageName returns the filename of a /images/<name> URL on this wiki.
func uploadedImageName(src string) (string, bool) {
	u, err := url.Parse(src)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "", false
	}
	name, ok := strings.CutPrefix(u.Path, "/images/")
	return name, ok && validUploadName(name)
}

// previewText collapses whitespace and cuts s at a word, if it is too long.
func previewText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= ogDescriptionMaxRunes {
		return s
	}
	cut := string([]rune(s)[:ogDescriptionMaxRunes])
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return cut + "…"
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
