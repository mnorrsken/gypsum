package wiki

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// listDocPages returns a sorted list of documentation pages from docsDir.
func (h *Handler) listDocPages() []PageLink {
	entries, err := os.ReadDir(h.docsDir)
	if err != nil {
		return nil
	}
	var pages []PageLink
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		slug := strings.TrimSuffix(e.Name(), ".md")
		title := docTitleFromSlug(slug)
		pages = append(pages, PageLink{Slug: slug, Title: title})
	}
	return pages
}

// docTitleFromSlug converts "authentication" to "Authentication", "my-guide" to "My Guide".
func docTitleFromSlug(slug string) string {
	words := strings.Fields(strings.ReplaceAll(slug, "-", " "))
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

func (h *Handler) handleDocsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h.render(w, r, "docs", TemplateData{
		Title: "Documentation",
	})
}

func (h *Handler) handleDocs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	slug := strings.TrimPrefix(r.URL.Path, "/docs/")
	if slug == "" {
		http.Redirect(w, r, "/docs", http.StatusFound)
		return
	}

	path := filepath.Join(h.docsDir, slug+".md")
	content, err := os.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	source := string(content)
	displayTitle := docTitleFromSlug(slug)
	renderSource := source
	if h1, rest := ExtractH1Title(source); h1 != "" {
		displayTitle = h1
		renderSource = rest
	}

	html, err := h.renderer.RenderPublic(renderSource)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.render(w, r, "doc", TemplateData{
		Title:        displayTitle,
		Page:         &Page{Slug: slug, Title: displayTitle},
		RenderedHTML: html,
	})
}
