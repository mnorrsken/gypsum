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
	skills, err := h.store.ListSkillEntries()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, "skills", TemplateData{
		Title:  "Skills",
		Skills: skills,
	})
}

func (h *Handler) handleSkillView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	slug := strings.TrimPrefix(r.URL.Path, "/skills/")
	if slug == "" {
		http.Redirect(w, r, "/skills", http.StatusFound)
		return
	}

	skill, err := h.store.Load(KindSkill, slug)
	if err != nil {
		if errors.Is(err, ErrPageNotFound) {
			http.Redirect(w, r, fmt.Sprintf("/edit-skill/%s?title=%s", slug, url.QueryEscape(TitleFromSlug(slug))), http.StatusFound)
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

	h.render(w, "skill_view", TemplateData{
		Title:        displayTitle,
		Page:         skill,
		RenderedHTML: html,
		SkillTags:    ExtractTags(skill.Content),
	})
}

func (h *Handler) handleNewSkill(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.render(w, "skill_edit", TemplateData{
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
		http.Redirect(w, r, fmt.Sprintf("/edit-skill/%s?title=%s", slug, url.QueryEscape(title)), http.StatusFound)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleEditSkill(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/edit-skill/")
	if slug == "" {
		http.Redirect(w, r, "/skills", http.StatusFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		skill, err := h.store.Load(KindSkill, slug)
		isNew := false
		if err != nil {
			title := TitleFromSlug(slug)
			if t := r.URL.Query().Get("title"); t != "" {
				title = t
			}
			skill = &Page{Slug: slug, Title: title}
			isNew = true
		}

		h.render(w, "skill_edit", TemplateData{
			Title:      skill.Title,
			Page:       skill,
			RawContent: skill.Content,
			IsNew:      isNew,
		})

	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		content := r.FormValue("content")

		// For new skills, derive the slug from the H1 title in the content.
		if _, err := h.store.Load(KindSkill, slug); err != nil {
			if h1, _ := ExtractH1Title(content); h1 != "" {
				slug = SlugFromTitle(h1)
			}
		}

		if err := h.store.Save(KindSkill, slug, content); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = h.autoCommit.CommitSave(KindSkill, slug, "")
		http.Redirect(w, r, "/skills/"+slug, http.StatusFound)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleDeleteSkill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	slug := strings.TrimPrefix(r.URL.Path, "/delete-skill/")
	if slug == "" {
		http.Redirect(w, r, "/skills", http.StatusFound)
		return
	}
	if err := h.store.Delete(KindSkill, slug); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.autoCommit.CommitDelete(KindSkill, slug, "")
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", "/skills")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/skills", http.StatusFound)
}

func (h *Handler) handleSkillHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	slug := strings.TrimPrefix(r.URL.Path, "/skill-history/")
	if slug == "" {
		http.Redirect(w, r, "/skills", http.StatusFound)
		return
	}

	entries, err := h.autoCommit.DocHistory(KindSkill, slug, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.render(w, "history", TemplateData{
		Title:   TitleFromSlug(slug) + " — History",
		Page:    &Page{Slug: slug, Title: TitleFromSlug(slug)},
		History: entries,
	})
}
