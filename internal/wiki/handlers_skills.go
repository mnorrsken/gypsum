package wiki

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func (h *Handler) handleSkillsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	store := h.storeFor(r)
	skills, err := store.ListSkillEntries()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, r, "skills", TemplateData{
		Title:  "Skills",
		Skills: skills,
	})
}

func (h *Handler) handleSkillView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	store := h.storeFor(r)
	prefix := urlPrefix(r)

	slug := strings.TrimPrefix(r.URL.Path, "/skills/")
	if slug == "" {
		http.Redirect(w, r, prefix+"/skills", http.StatusFound)
		return
	}

	skill, err := store.Load(KindSkill, slug)
	if err != nil {
		if errors.Is(err, ErrPageNotFound) {
			http.Redirect(w, r, fmt.Sprintf("%s/edit-skill/%s?title=%s", prefix, slug, url.QueryEscape(TitleFromSlug(slug))), http.StatusFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	displayTitle := skill.Title
	renderSource := skill.Content
	if h1, rest := ExtractH1Title(skill.Content); h1 != "" {
		displayTitle = h1
		renderSource = rest
	}

	html, err := h.renderer.Render(renderSource)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.render(w, r, "skill_view", TemplateData{
		Title:        displayTitle,
		Page:         skill,
		RenderedHTML: html,
		SkillTags:    ExtractTags(skill.Content),
	})
}

func (h *Handler) handleNewSkill(w http.ResponseWriter, r *http.Request) {
	prefix := urlPrefix(r)

	switch r.Method {
	case http.MethodGet:
		h.render(w, r, "skill_edit", TemplateData{
			Title: "New Skill",
			Page:  &Page{Slug: "New_Skill", Title: "New Skill"},
			IsNew: true,
			RawContent: "# Skill Title\n\nBrief description of what this skill covers.\n\n" +
				"## When to Use\n\nDescribe when to apply this skill.\n\n" +
				"## Instructions\n\n1. Step one\n2. Step two\n\n" +
				"Tags: keyword1, keyword2",
		})
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		title := strings.TrimSpace(r.FormValue("title"))
		if title == "" {
			title = "New Skill"
		}
		slug := SlugFromTitle(title)
		http.Redirect(w, r, fmt.Sprintf("%s/edit-skill/%s?title=%s", prefix, slug, url.QueryEscape(title)), http.StatusFound)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleEditSkill(w http.ResponseWriter, r *http.Request) {
	store := h.storeFor(r)
	autoCommit := h.autoCommitFor(r)
	prefix := urlPrefix(r)

	slug := strings.TrimPrefix(r.URL.Path, "/edit-skill/")
	if slug == "" {
		http.Redirect(w, r, prefix+"/skills", http.StatusFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		skill, err := store.Load(KindSkill, slug)
		isNew := false
		if err != nil {
			title := TitleFromSlug(slug)
			if t := r.URL.Query().Get("title"); t != "" {
				title = t
			}
			skill = &Page{Slug: slug, Title: title}
			isNew = true
		}

		rawContent := skill.Content
		if h.crypto != nil {
			rawContent = h.crypto.DecryptForEdit(rawContent)
		}

		h.render(w, r, "skill_edit", TemplateData{
			Title:      skill.Title,
			Page:       skill,
			RawContent: rawContent,
			IsNew:      isNew,
		})

	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		content := r.FormValue("content")

		// For new skills, derive the slug from the H1 title in the content.
		if _, err := store.Load(KindSkill, slug); err != nil {
			if h1, _ := ExtractH1Title(content); h1 != "" {
				slug = SlugFromTitle(h1)
			}
		}

		if h.crypto != nil {
			encrypted, err := h.crypto.EncryptForSave(content)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			content = encrypted
		}

		if err := store.Save(KindSkill, slug, content); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = autoCommit.CommitSave(KindSkill, slug, "")
		http.Redirect(w, r, prefix+"/skills/"+slug, http.StatusFound)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleDeleteSkill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	store := h.storeFor(r)
	autoCommit := h.autoCommitFor(r)
	prefix := urlPrefix(r)

	slug := strings.TrimPrefix(r.URL.Path, "/delete-skill/")
	if slug == "" {
		http.Redirect(w, r, prefix+"/skills", http.StatusFound)
		return
	}
	if err := store.Delete(KindSkill, slug); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = autoCommit.CommitDelete(KindSkill, slug, "")
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", prefix+"/skills")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, prefix+"/skills", http.StatusFound)
}

func (h *Handler) handleSkillHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	autoCommit := h.autoCommitFor(r)
	prefix := urlPrefix(r)

	slug := strings.TrimPrefix(r.URL.Path, "/skill-history/")
	if slug == "" {
		http.Redirect(w, r, prefix+"/skills", http.StatusFound)
		return
	}

	entries, err := autoCommit.DocHistory(KindSkill, slug, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.render(w, r, "history", TemplateData{
		Title:   TitleFromSlug(slug) + " — History",
		Page:    &Page{Slug: slug, Title: TitleFromSlug(slug)},
		History: entries,
	})
}
