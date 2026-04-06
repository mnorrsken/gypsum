package wiki

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// toolSectionMap maps tool names to their section for dispatch-time checks.
var toolSectionMap = map[string]MCPSection{
	"list_pages": MCPSectionRead, "get_page": MCPSectionRead, "search_pages": MCPSectionRead,
	"list_images": MCPSectionRead, "get_recent_pages": MCPSectionRead, "get_favorites": MCPSectionRead,
	"page_history": MCPSectionRead, "get_page_revision": MCPSectionRead,
	"page_links": MCPSectionRead, "what_links_here": MCPSectionRead, "link_graph": MCPSectionRead,
	"create_page": MCPSectionEdit, "edit_page": MCPSectionEdit,
	"create_page_from_mediawiki": MCPSectionEdit, "edit_page_from_mediawiki": MCPSectionEdit,
	"delete_page": MCPSectionDelete, "delete_image": MCPSectionDelete,
	"list_skills": MCPSectionSkills, "get_skill": MCPSectionSkills,
	"create_skill": MCPSectionSkills, "edit_skill": MCPSectionSkills,
	"delete_skill": MCPSectionSkills, "search_skills": MCPSectionSkills,
}

// ── Tool dispatch ───────────────────────────────────────────────────────

func (m *MCPHandler) callTool(params mcpToolCallParams) mcpCallToolResult {
	if sec, ok := toolSectionMap[params.Name]; ok && !m.sections[sec] {
		return mcpError("tool not available: " + params.Name)
	}
	switch params.Name {
	case "list_pages":
		return m.toolListPages()
	case "get_page":
		return m.toolGetPage(params.Arguments)
	case "create_page":
		return m.toolCreatePage(params.Arguments)
	case "edit_page":
		return m.toolEditPage(params.Arguments)
	case "delete_page":
		return m.toolDeletePage(params.Arguments)
	case "search_pages":
		return m.toolSearchPages(params.Arguments)
	case "list_images":
		return m.toolListImages()
	case "delete_image":
		return m.toolDeleteImage(params.Arguments)
	case "get_recent_pages":
		return m.toolGetRecentPages(params.Arguments)
	case "get_favorites":
		return m.toolGetFavorites()
	case "page_history":
		return m.toolPageHistory(params.Arguments)
	case "get_page_revision":
		return m.toolGetPageRevision(params.Arguments)
	case "page_links":
		return m.toolPageLinks(params.Arguments)
	case "what_links_here":
		return m.toolWhatLinksHere(params.Arguments)
	case "link_graph":
		return m.toolLinkGraph()
	case "create_page_from_mediawiki":
		return m.toolCreatePageFromMediaWiki(params.Arguments)
	case "edit_page_from_mediawiki":
		return m.toolEditPageFromMediaWiki(params.Arguments)
	case "list_skills":
		return m.toolListSkills()
	case "get_skill":
		return m.toolGetSkill(params.Arguments)
	case "create_skill":
		return m.toolCreateSkill(params.Arguments)
	case "edit_skill":
		return m.toolEditSkill(params.Arguments)
	case "delete_skill":
		return m.toolDeleteSkill(params.Arguments)
	case "search_skills":
		return m.toolSearchSkills(params.Arguments)
	default:
		return mcpError("unknown tool: " + params.Name)
	}
}

// ── Tool implementations ────────────────────────────────────────────────

func (m *MCPHandler) toolListPages() mcpCallToolResult {
	pages, err := m.store.List(KindPage)
	if err != nil {
		return mcpError("failed to list pages: " + err.Error())
	}
	return mcpJSON(pages)
}

func (m *MCPHandler) toolGetPage(args map[string]any) mcpCallToolResult {
	slug, ok := mcpArgString(args, "slug")
	if !ok {
		return mcpError("missing required argument: slug")
	}
	page, err := m.store.Load(KindPage, slug)
	if err != nil {
		return mcpError("page not found: " + slug)
	}
	return getDocResult(page.Content, args)
}

func (m *MCPHandler) toolCreatePage(args map[string]any) mcpCallToolResult {
	title, ok := mcpArgString(args, "title")
	if !ok {
		return mcpError("missing required argument: title")
	}
	content, ok := mcpArgString(args, "content")
	if !ok {
		return mcpError("missing required argument: content")
	}

	slug := SlugFromTitle(title)
	if _, err := m.store.Load(KindPage, slug); err == nil {
		return mcpError("page already exists: " + slug)
	}

	if err := m.store.Save(KindPage, slug, content); err != nil {
		return mcpError("failed to save page: " + err.Error())
	}
	_ = m.autoCommit.CommitSave(KindPage, slug, "")
	return mcpText(fmt.Sprintf("Created page '%s' (slug: %s)", title, slug))
}

func (m *MCPHandler) toolEditPage(args map[string]any) mcpCallToolResult {
	slug, ok := mcpArgString(args, "slug")
	if !ok {
		return mcpError("missing required argument: slug")
	}
	page, err := m.store.Load(KindPage, slug)
	if err != nil {
		return mcpError("page not found: " + slug)
	}
	finalContent, res := applyEditMode(page.Content, args)
	if res != nil {
		return *res
	}
	if err := m.store.Save(KindPage, slug, finalContent); err != nil {
		return mcpError("failed to save page: " + err.Error())
	}
	_ = m.autoCommit.CommitSave(KindPage, slug, "")
	return mcpText(fmt.Sprintf("Updated page '%s'", slug))
}

func (m *MCPHandler) toolDeletePage(args map[string]any) mcpCallToolResult {
	slug, ok := mcpArgString(args, "slug")
	if !ok {
		return mcpError("missing required argument: slug")
	}

	if _, err := m.store.Load(KindPage, slug); err != nil {
		return mcpError("page not found: " + slug)
	}

	if err := m.store.Delete(KindPage, slug); err != nil {
		return mcpError("failed to delete page: " + err.Error())
	}
	_ = m.autoCommit.CommitDelete(KindPage, slug, "")
	return mcpText(fmt.Sprintf("Deleted page '%s'", slug))
}

func (m *MCPHandler) toolSearchPages(args map[string]any) mcpCallToolResult {
	queries, ok := mcpArgStringArray(args, "query")
	if !ok {
		return mcpError("missing required argument: query")
	}

	text, found := m.runMultiSearch(KindPage, queries)
	if !found {
		return mcpText("No results found for: " + strings.Join(queries, ", "))
	}
	return mcpText(text)
}

func (m *MCPHandler) toolListImages() mcpCallToolResult {
	images, err := m.store.ListImages()
	if err != nil {
		return mcpError("failed to list images: " + err.Error())
	}
	if len(images) == 0 {
		return mcpText("No images uploaded.")
	}
	return mcpJSON(images)
}

func (m *MCPHandler) toolDeleteImage(args map[string]any) mcpCallToolResult {
	filename, ok := mcpArgString(args, "filename")
	if !ok {
		return mcpError("missing required argument: filename")
	}
	if strings.Contains(filename, "/") || strings.Contains(filename, "..") {
		return mcpError("invalid filename")
	}

	imgPath := m.store.ImagePath(filename)
	if err := os.Remove(imgPath); err != nil {
		return mcpError("image not found: " + filename)
	}
	_ = m.autoCommit.CommitImageDelete(filename, "")
	return mcpText(fmt.Sprintf("Deleted image '%s'", filename))
}

func (m *MCPHandler) toolGetRecentPages(args map[string]any) mcpCallToolResult {
	count := 10
	if n, ok := mcpArgNumber(args, "count"); ok && n > 0 {
		count = int(n)
	}
	pages, err := m.store.RecentPages(count)
	if err != nil {
		return mcpError("failed to get recent pages: " + err.Error())
	}
	return mcpJSON(pages)
}

func (m *MCPHandler) toolGetFavorites() mcpCallToolResult {
	favs, err := m.store.LoadFavorites()
	if err != nil {
		return mcpError("failed to load favorites: " + err.Error())
	}
	if len(favs) == 0 {
		return mcpText("No favorites set.")
	}
	return mcpJSON(favs)
}

func (m *MCPHandler) toolPageHistory(args map[string]any) mcpCallToolResult {
	slug, ok := mcpArgString(args, "slug")
	if !ok {
		return mcpError("missing required argument: slug")
	}
	count := 20
	if n, ok := mcpArgNumber(args, "count"); ok && n > 0 {
		count = int(n)
	}

	entries, err := m.autoCommit.DocHistory(KindPage, slug, count)
	if err != nil {
		return mcpError("failed to get history: " + err.Error())
	}
	if len(entries) == 0 {
		return mcpText("No history available for: " + slug)
	}
	return mcpJSON(entries)
}

func (m *MCPHandler) toolGetPageRevision(args map[string]any) mcpCallToolResult {
	slug, ok := mcpArgString(args, "slug")
	if !ok {
		return mcpError("missing required argument: slug")
	}
	hash, ok := mcpArgString(args, "hash")
	if !ok {
		return mcpError("missing required argument: hash")
	}

	content, err := m.autoCommit.DocContentAtRevision(KindPage, slug, hash)
	if err != nil {
		return mcpError("failed to get revision: " + err.Error())
	}
	return mcpText(content)
}

func (m *MCPHandler) toolPageLinks(args map[string]any) mcpCallToolResult {
	slug, ok := mcpArgString(args, "slug")
	if !ok {
		return mcpError("missing required argument: slug")
	}
	page, err := m.store.Load(KindPage, slug)
	if err != nil {
		return mcpError("page not found: " + slug)
	}
	links := ExtractWikiLinks(page.Content)
	if len(links) == 0 {
		return mcpText("Page '" + slug + "' has no outgoing wiki links.")
	}
	return mcpJSON(links)
}

func (m *MCPHandler) toolWhatLinksHere(args map[string]any) mcpCallToolResult {
	slug, ok := mcpArgString(args, "slug")
	if !ok {
		return mcpError("missing required argument: slug")
	}
	backlinks, err := m.store.BackLinks(slug)
	if err != nil {
		return mcpError("failed to compute backlinks: " + err.Error())
	}
	if len(backlinks) == 0 {
		return mcpText("No pages link to '" + slug + "'. This page is orphaned — consider linking it from a parent page.")
	}
	return mcpJSON(backlinks)
}

func (m *MCPHandler) toolCreatePageFromMediaWiki(args map[string]any) mcpCallToolResult {
	title, ok := mcpArgString(args, "title")
	if !ok {
		return mcpError("missing required argument: title")
	}
	wikitext, ok := mcpArgString(args, "wikitext")
	if !ok {
		return mcpError("missing required argument: wikitext")
	}

	slug := SlugFromTitle(title)
	if _, err := m.store.Load(KindPage, slug); err == nil {
		return mcpError("page already exists: " + slug)
	}

	content := ConvertMediaWikiToMarkdown(wikitext)
	if err := m.store.Save(KindPage, slug, content); err != nil {
		return mcpError("failed to save page: " + err.Error())
	}
	_ = m.autoCommit.CommitSave(KindPage, slug, "")
	return mcpText(fmt.Sprintf("Created page '%s' (slug: %s) from MediaWiki source", title, slug))
}

func (m *MCPHandler) toolEditPageFromMediaWiki(args map[string]any) mcpCallToolResult {
	slug, ok := mcpArgString(args, "slug")
	if !ok {
		return mcpError("missing required argument: slug")
	}
	wikitext, ok := mcpArgString(args, "wikitext")
	if !ok {
		return mcpError("missing required argument: wikitext")
	}

	if _, err := m.store.Load(KindPage, slug); err != nil {
		return mcpError("page not found: " + slug)
	}

	content := ConvertMediaWikiToMarkdown(wikitext)
	if err := m.store.Save(KindPage, slug, content); err != nil {
		return mcpError("failed to save page: " + err.Error())
	}
	_ = m.autoCommit.CommitSave(KindPage, slug, "")
	return mcpText(fmt.Sprintf("Updated page '%s' from MediaWiki source", slug))
}

func (m *MCPHandler) toolLinkGraph() mcpCallToolResult {
	graph, err := m.store.LinkGraph()
	if err != nil {
		return mcpError("failed to build link graph: " + err.Error())
	}
	return mcpJSON(graph)
}

// ── Multi-query search helper ──────────────────────────────────────────

func (m *MCPHandler) runMultiSearch(kind DocKind, queries []string) (string, bool) {
	var sb strings.Builder
	found := false
	for _, q := range queries {
		results, err := m.store.Search(kind, q)
		if err != nil || len(results) == 0 {
			continue
		}
		found = true
		if len(queries) > 1 {
			fmt.Fprintf(&sb, "### Results for: %s\n\n", q)
		}
		for _, r := range results {
			fmt.Fprintf(&sb, "## %s\n**Slug:** %s\n", r.Title, r.Slug)
			if len(r.Snippets) > 0 {
				for _, snip := range r.Snippets {
					fmt.Fprintf(&sb, "> %s\n", snip)
				}
			} else if r.Excerpt != "" {
				fmt.Fprintf(&sb, "> %s\n", r.Excerpt)
			}
			sb.WriteString("\n")
		}
	}
	return sb.String(), found
}

// ── Skill tool implementations ─────────────────────────────────────────

func (m *MCPHandler) toolListSkills() mcpCallToolResult {
	skills, err := m.store.ListSkillEntries()
	if err != nil {
		return mcpError("failed to list skills: " + err.Error())
	}
	if len(skills) == 0 {
		return mcpText("No skills created yet.")
	}
	return mcpJSON(skills)
}

func (m *MCPHandler) toolGetSkill(args map[string]any) mcpCallToolResult {
	slug, ok := mcpArgString(args, "slug")
	if !ok {
		return mcpError("missing required argument: slug")
	}
	skill, err := m.store.Load(KindSkill, slug)
	if err != nil {
		return mcpError("skill not found: " + slug)
	}
	return getDocResult(skill.Content, args)
}

func (m *MCPHandler) toolCreateSkill(args map[string]any) mcpCallToolResult {
	title, ok := mcpArgString(args, "title")
	if !ok {
		return mcpError("missing required argument: title")
	}
	content, ok := mcpArgString(args, "content")
	if !ok {
		return mcpError("missing required argument: content")
	}

	slug := SlugFromTitle(title)
	if _, err := m.store.Load(KindSkill, slug); err == nil {
		return mcpError("skill already exists: " + slug)
	}

	if err := m.store.Save(KindSkill, slug, content); err != nil {
		return mcpError("failed to save skill: " + err.Error())
	}
	_ = m.autoCommit.CommitSave(KindSkill, slug, "")
	return mcpText(fmt.Sprintf("Created skill '%s' (slug: %s)", title, slug))
}

func (m *MCPHandler) toolEditSkill(args map[string]any) mcpCallToolResult {
	slug, ok := mcpArgString(args, "slug")
	if !ok {
		return mcpError("missing required argument: slug")
	}
	skill, err := m.store.Load(KindSkill, slug)
	if err != nil {
		return mcpError("skill not found: " + slug)
	}
	finalContent, res := applyEditMode(skill.Content, args)
	if res != nil {
		return *res
	}
	if err := m.store.Save(KindSkill, slug, finalContent); err != nil {
		return mcpError("failed to save skill: " + err.Error())
	}
	_ = m.autoCommit.CommitSave(KindSkill, slug, "")
	return mcpText(fmt.Sprintf("Updated skill '%s'", slug))
}

func (m *MCPHandler) toolDeleteSkill(args map[string]any) mcpCallToolResult {
	slug, ok := mcpArgString(args, "slug")
	if !ok {
		return mcpError("missing required argument: slug")
	}

	if _, err := m.store.Load(KindSkill, slug); err != nil {
		return mcpError("skill not found: " + slug)
	}

	if err := m.store.Delete(KindSkill, slug); err != nil {
		return mcpError("failed to delete skill: " + err.Error())
	}
	_ = m.autoCommit.CommitDelete(KindSkill, slug, "")
	return mcpText(fmt.Sprintf("Deleted skill '%s'", slug))
}

func (m *MCPHandler) toolSearchSkills(args map[string]any) mcpCallToolResult {
	queries, ok := mcpArgStringArray(args, "query")
	if !ok {
		return mcpError("missing required argument: query")
	}

	// Collect unique slugs across all queries.
	seen := map[string]struct{}{}
	var uniqueSlugs []string
	for _, q := range queries {
		results, err := m.store.Search(KindSkill, q)
		if err != nil {
			continue
		}
		for _, r := range results {
			if _, dup := seen[r.Slug]; !dup {
				seen[r.Slug] = struct{}{}
				uniqueSlugs = append(uniqueSlugs, r.Slug)
			}
		}
	}

	if len(uniqueSlugs) == 0 {
		return mcpText("No skills found for: " + strings.Join(queries, ", "))
	}

	// Single match — return the full skill content.
	if len(uniqueSlugs) == 1 {
		skill, err := m.store.Load(KindSkill, uniqueSlugs[0])
		if err != nil {
			return mcpError("failed to load skill: " + err.Error())
		}
		return mcpText(skill.Content)
	}

	text, _ := m.runMultiSearch(KindSkill, queries)
	return mcpText(text)
}

// ── Helpers ─────────────────────────────────────────────────────────────

// applyEditMode resolves the edit mode from args and returns the new content.
// If the returned *mcpCallToolResult is non-nil, it is an error to return.
func applyEditMode(current string, args map[string]any) (string, *mcpCallToolResult) {
	oldText, hasOldText := mcpArgString(args, "old_text")
	newText, hasNewText := mcpArgString(args, "new_text")
	sectionName, hasSection := mcpArgString(args, "section")
	appendMode, _ := mcpArgBool(args, "append")
	content, hasContent := mcpArgString(args, "content")

	switch {
	case hasOldText || hasNewText:
		// Search-and-replace mode.
		if !hasOldText || !hasNewText {
			e := mcpError("both 'old_text' and 'new_text' must be provided together")
			return "", &e
		}
		if oldText == "" {
			e := mcpError("'old_text' must not be empty")
			return "", &e
		}
		count := strings.Count(current, oldText)
		if count == 0 {
			e := mcpError("old_text not found in page content")
			return "", &e
		}
		if count > 1 {
			e := mcpError(fmt.Sprintf("old_text matches %d locations — must be unique. Include more surrounding context to disambiguate.", count))
			return "", &e
		}
		return strings.Replace(current, oldText, newText, 1), nil

	case hasSection:
		if !hasContent {
			e := mcpError("'content' is required when using 'section'")
			return "", &e
		}
		result, err := ReplaceSection(current, sectionName, content)
		if err != nil {
			e := mcpError(err.Error())
			return "", &e
		}
		return result, nil

	case appendMode:
		if !hasContent {
			e := mcpError("'content' is required when using 'append'")
			return "", &e
		}
		sep := "\n\n"
		if strings.HasSuffix(current, "\n") {
			sep = "\n"
		}
		return current + sep + content, nil

	default:
		// Full replace (backward compatible).
		if !hasContent {
			e := mcpError("missing required argument: content (or use old_text/new_text, section, or append mode)")
			return "", &e
		}
		return content, nil
	}
}

// getDocResult handles the optional section/sections_only parameters for
// get_page and get_skill, returning the appropriate subset of content.
func getDocResult(content string, args map[string]any) mcpCallToolResult {
	sectionsOnly, _ := mcpArgBool(args, "sections_only")
	sectionName, hasSection := mcpArgString(args, "section")

	if sectionsOnly && hasSection {
		return mcpError("cannot use both 'section' and 'sections_only'")
	}
	if sectionsOnly {
		headings := ListSectionHeadings(content)
		if len(headings) == 0 {
			return mcpText("Page has no sections (no headings found).")
		}
		return mcpJSON(headings)
	}
	if hasSection {
		body, err := GetSection(content, sectionName)
		if err != nil {
			return mcpError(err.Error())
		}
		return mcpText(body)
	}
	return mcpText(content)
}

func mcpArgBool(args map[string]any, key string) (bool, bool) {
	v, ok := args[key]
	if !ok {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

func mcpArgString(args map[string]any, key string) (string, bool) {
	v, ok := args[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func mcpArgNumber(args map[string]any, key string) (float64, bool) {
	v, ok := args[key]
	if !ok {
		return 0, false
	}
	n, ok := v.(float64)
	return n, ok
}

func mcpText(text string) mcpCallToolResult {
	return mcpCallToolResult{
		Content: []mcpContent{{Type: "text", Text: text}},
	}
}

func mcpJSON(v any) mcpCallToolResult {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcpError("failed to marshal result: " + err.Error())
	}
	return mcpCallToolResult{
		Content: []mcpContent{{Type: "text", Text: string(data)}},
	}
}

func mcpError(msg string) mcpCallToolResult {
	return mcpCallToolResult{
		Content: []mcpContent{{Type: "text", Text: msg}},
		IsError: true,
	}
}

func mcpSchema(typ string, properties map[string]any, required []string) map[string]any {
	schema := map[string]any{"type": typ}
	if properties != nil {
		schema["properties"] = properties
	}
	if required != nil {
		schema["required"] = required
	}
	return schema
}

func mcpPropString(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func mcpPropStringArray(desc string) map[string]any {
	return map[string]any{
		"type":        "array",
		"items":       map[string]any{"type": "string"},
		"description": desc,
	}
}

func mcpArgStringArray(args map[string]any, key string) ([]string, bool) {
	v, ok := args[key]
	if !ok {
		return nil, false
	}
	// Handle single string (backward compat)
	if s, ok := v.(string); ok {
		return []string{s}, true
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	strs := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			strs = append(strs, s)
		}
	}
	if len(strs) == 0 {
		return nil, false
	}
	return strs, true
}
