package wiki

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (h *Handler) handleNewPage(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.render(w, "new", TemplateData{
			Title: "New Page",
		})
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		title := strings.TrimSpace(r.FormValue("title"))
		if title == "" {
			h.render(w, "new", TemplateData{
				Title: "New Page",
				Query: "Please enter a page title.",
			})
			return
		}
		slug := SlugFromTitle(title)
		_, err := h.store.Load(KindPage, slug)
		if err == nil {
			// page already exists
			h.render(w, "new", TemplateData{
				Title: "New Page",
				Query: fmt.Sprintf("A page named \"%s\" already exists.", title),
			})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/edit/%s?title=%s", slug, url.QueryEscape(title)), http.StatusFound)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	slug := strings.TrimPrefix(r.URL.Path, "/wiki/")
	if slug == "" {
		http.Redirect(w, r, "/wiki/Home", http.StatusFound)
		return
	}

	page, err := h.store.Load(KindPage, slug)
	if err != nil {
		if errors.Is(err, ErrPageNotFound) {
			editURL := fmt.Sprintf("/edit/%s", slug)
			if t := r.URL.Query().Get("title"); t != "" {
				editURL += "?title=" + url.QueryEscape(t)
			}
			http.Redirect(w, r, editURL, http.StatusFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// If the page starts with a level-1 heading, use that as the display
	// title and render only the rest of the content (the heading is shown
	// by the page-title-area in the template, so rendering it again would
	// duplicate it).
	displayTitle := page.Title
	renderSource := page.Content
	if h1, rest := ExtractH1Title(page.Content); h1 != "" {
		displayTitle = h1
		renderSource = rest
	}

	html, err := h.renderer.Render(renderSource)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.render(w, "view", TemplateData{
		Title:        displayTitle,
		Page:         &Page{Slug: page.Slug, Title: displayTitle, Content: page.Content},
		RenderedHTML: html,
	})
}

func (h *Handler) handleEdit(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/edit/")
	if slug == "" {
		http.Error(w, "missing page slug", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		page, err := h.store.Load(KindPage, slug)
		if err != nil && !errors.Is(err, ErrPageNotFound) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		raw := ""
		title := TitleFromSlug(slug)
		isNew := true
		if page != nil {
			raw = page.Content
			title = page.Title
			isNew = false
		}

		// For new pages, pre-populate with a level-1 heading so the proper
		// title (including characters stripped from the slug) is preserved.
		if isNew && raw == "" {
			prettyTitle := title
			if t := r.URL.Query().Get("title"); t != "" {
				prettyTitle = t
			}
			raw = "# " + prettyTitle + "\n\n"
		}

		prefix := "Edit: "
		if isNew {
			prefix = "New page: "
		}

		h.render(w, "edit", TemplateData{
			Title:      prefix + title,
			Page:       &Page{Slug: slug, Title: title},
			RawContent: h.crypto.DecryptForEdit(raw),
			IsNew:      isNew,
		})
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		content := strings.ReplaceAll(r.FormValue("content"), "\r\n", "\n")

		// For new pages, derive the slug from the H1 title in the content.
		if _, err := h.store.Load(KindPage, slug); errors.Is(err, ErrPageNotFound) {
			if h1, _ := ExtractH1Title(content); h1 != "" {
				slug = SlugFromTitle(h1)
			}
		}

		// Validate custom tags before saving
		if validationErr := ValidateContent(content); validationErr != "" {
			title := TitleFromSlug(slug)
			page, _ := h.store.Load(KindPage, slug)
			isNew := page == nil
			prefix := "Edit: "
			if isNew {
				prefix = "New page: "
			}
			h.render(w, "edit", TemplateData{
				Title:      prefix + title,
				Page:       &Page{Slug: slug, Title: title},
				RawContent: content,
				IsNew:      isNew,
				Query:      validationErr,
			})
			return
		}

		// Show diff preview if requested
		if r.FormValue("showdiff") == "1" {
			title := TitleFromSlug(slug)
			oldContent := ""
			if page, _ := h.store.Load(KindPage, slug); page != nil {
				oldContent = page.Content
			}
			// Encrypt the incoming editor content, preserving original
			// ciphertext for unchanged blocks so the diff is clean.
			newEncrypted, err := h.crypto.EncryptForSavePreserving(content, oldContent)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			diffHTML := RenderUnifiedDiff(oldContent, newEncrypted, slug)
			h.render(w, "diff", TemplateData{
				Title:      "Diff: " + title,
				Page:       &Page{Slug: slug, Title: title},
				RawContent: content,
				DiffHTML:   diffHTML,
			})
			return
		}

		encrypted, err := h.crypto.EncryptForSave(content)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := h.store.Save(KindPage, slug, encrypted); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := h.autoCommit.CommitSave(KindPage, slug, UsernameFromRequest(r)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/wiki/%s", slug), http.StatusFound)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	results, err := h.store.Search(KindPage, query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := TemplateData{
		Title:   "Search",
		Query:   query,
		Results: results,
	}
	if isHTMX(r) {
		h.renderFragment(w, "search", data)
		return
	}
	h.render(w, "search", data)
}

func (h *Handler) handlePages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	allPages, err := h.store.List(KindPage)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.render(w, "pages", TemplateData{
		Title:    "All Pages",
		AllPages: allPages,
	})
}

func (h *Handler) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	slug := strings.TrimPrefix(r.URL.Path, "/history/")
	if slug == "" {
		http.Error(w, "missing page slug", http.StatusBadRequest)
		return
	}

	title := TitleFromSlug(slug)
	entries, _ := h.autoCommit.DocHistory(KindPage, slug, 50)

	h.render(w, "history", TemplateData{
		Title:   "History: " + title,
		Page:    &Page{Slug: slug, Title: title},
		History: entries,
	})
}

const recentEditsPerPage = 50

func (h *Handler) handleRecentEdits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 1 {
		page = p
	}

	skip := (page - 1) * recentEditsPerPage
	// Fetch one extra to determine if there's a next page.
	entries, _ := h.autoCommit.GlobalHistory(skip, recentEditsPerPage+1)

	totalPages := page
	if len(entries) > recentEditsPerPage {
		totalPages = page + 1
		entries = entries[:recentEditsPerPage]
	}

	h.render(w, "recent_edits", TemplateData{
		Title:       "Recent Edits",
		GlobalEdits: entries,
		CurrentPage: page,
		TotalPages:  totalPages,
	})
}

func (h *Handler) handleHistoryDiff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	slug := strings.TrimPrefix(r.URL.Path, "/history-diff/")
	if slug == "" {
		http.Error(w, "missing page slug", http.StatusBadRequest)
		return
	}

	fromHash := r.URL.Query().Get("from")
	toHash := r.URL.Query().Get("to")
	if fromHash == "" || toHash == "" {
		http.Error(w, "missing from or to parameter", http.StatusBadRequest)
		return
	}

	oldContent, err := h.autoCommit.DocContentAtRevision(KindPage, slug, fromHash)
	if err != nil {
		http.Error(w, "could not load old revision: "+err.Error(), http.StatusNotFound)
		return
	}
	newContent, err := h.autoCommit.DocContentAtRevision(KindPage, slug, toHash)
	if err != nil {
		http.Error(w, "could not load new revision: "+err.Error(), http.StatusNotFound)
		return
	}

	diffHTML := RenderUnifiedDiff(oldContent, newContent, slug)
	title := TitleFromSlug(slug)

	h.render(w, "history_diff", TemplateData{
		Title:    "Diff: " + title,
		Page:     &Page{Slug: slug, Title: title},
		DiffHTML: diffHTML,
		Query:    fmt.Sprintf("%s...%s", fromHash[:minLen(len(fromHash), 7)], toHash[:minLen(len(toHash), 7)]),
	})
}

func (h *Handler) handleGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	graph, err := h.store.LinkGraph()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data, err := json.Marshal(graph)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.render(w, "graph", TemplateData{
		Title:     "Link Graph",
		GraphJSON: template.JS(data),
	})
}

func minLen(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (h *Handler) handleDeletePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	slug := strings.TrimPrefix(r.URL.Path, "/delete/")
	if slug == "" {
		http.Error(w, "missing page slug", http.StatusBadRequest)
		return
	}

	if _, err := h.store.Load(KindPage, slug); err != nil {
		if errors.Is(err, ErrPageNotFound) {
			http.Error(w, "page not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.store.Delete(KindPage, slug); err != nil {
		http.Error(w, "failed to delete page", http.StatusInternalServerError)
		return
	}
	_ = h.autoCommit.CommitDelete(KindPage, slug, UsernameFromRequest(r))
	// Clean up any share link for this page.
	if h.db != nil {
		_ = h.db.DeleteShare(slug)
	}
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", "/pages")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/pages", http.StatusFound)
}

func (h *Handler) handleConvertMediaWiki(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid form"})
		return
	}
	wikitext := r.FormValue("wikitext")
	if wikitext == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "empty wikitext"})
		return
	}
	markdown := ConvertMediaWikiToMarkdown(wikitext)
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "markdown": markdown})
}
