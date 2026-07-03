package wiki

// toolDefinitions returns the list of MCP tools available, filtered to the
// enabled sections. Schema helpers (mcpSchema, mcpPropString, etc.) are in
// mcp_tool_impls.go.
func (m *MCPHandler) toolDefinitions() []mcpTool {
	allTools := []mcpTool{
		// ── Read tools ───────────────────────────────────────────────
		{
			Name:    "list_pages",
			Section: MCPSectionRead,
			Description: "List wiki pages. By default returns every page (slug + title) sorted alphabetically. " +
				"Pass 'sort: recent' to order by last-modified (each entry then includes a 'modified' timestamp), " +
				"'favorites_only: true' to return just the pinned/favorite sidebar pages, " +
				"or 'limit' to cap the number of results. " +
				"Prefer search_pages over this tool when looking for specific pages — do not list all pages just to find one.",
			InputSchema: mcpSchema("object", map[string]any{
				"sort":           mcpPropString("Ordering: 'title' (default, alphabetical) or 'recent' (most recently modified first, adds a 'modified' timestamp)."),
				"limit":          map[string]any{"type": "number", "description": "Maximum number of pages to return (0/omitted = all)."},
				"favorites_only": map[string]any{"type": "boolean", "description": "If true, return only favorite/pinned pages from the sidebar."},
			}, nil),
		},
		{
			Name:    "get_page",
			Section: MCPSectionRead,
			Description: "Get the raw markdown content of a wiki page by its exact slug. " +
				"ONLY use this when you already know the exact slug. " +
				"If you are not sure of the exact slug, use search_pages first to find it. " +
				"Do NOT guess slugs — search instead. " +
				"Optional: pass 'section' to get only a specific section (matched by heading name, any level), " +
				"'sections_only: true' to list just the section headings (shown with # prefix to indicate level), " +
				"or 'include_links: true' to append this page's outgoing links and backlinks (useful for understanding where it sits in the wiki).",
			InputSchema: mcpSchema("object", map[string]any{
				"slug":          mcpPropString("Exact page slug, e.g. 'Home' or 'My_Page'. Slugs use underscores for spaces. Must be exact — use search_pages if unsure."),
				"section":       mcpPropString("Return only the named section (matched by # heading name, case-insensitive). Omit to get full page."),
				"sections_only": map[string]any{"type": "boolean", "description": "If true, return only the list of # section headings (not content). Useful for discovering page structure before a section edit."},
				"include_links": map[string]any{"type": "boolean", "description": "If true, append the page's outgoing wiki links and backlinks after the content."},
			}, []string{"slug"}),
		},
		{
			Name:        "search_pages",
			Section:     MCPSectionRead,
			Description: "Full-text search across all wiki pages. Uses FTS5 indexing for fast, relevant results with BM25 ranking. Each query is split into terms (punctuation ignored); each term is prefix-matched, so 'arch' finds 'architecture'. Results include context snippets and each page's outgoing/backlink counts (so you can spot hub pages). Multiple queries can be provided to search for different topics at once.",
			InputSchema: mcpSchema("object", map[string]any{
				"query": mcpPropStringArray("Search queries — each is split into terms on whitespace/punctuation, each prefix-matched independently. Multiple queries search for different topics in one call."),
				"limit": map[string]any{"type": "number", "description": "Maximum number of results to return across all queries (default 10)."},
			}, []string{"query"}),
		},
		{
			Name:    "suggest_page_location",
			Section: MCPSectionRead,
			Description: "Suggest where a NEW page should live: returns a ranked list of existing pages that would make good parents to link it from. " +
				"Use this before create_page to decide the 'link_from' target, instead of manually listing and reading candidate pages. " +
				"Ranking combines full-text relevance to the title/keywords with each candidate's position in the link graph (hub/index pages are favored). " +
				"For each suggestion it returns the slug, outgoing/backlink counts, a reason, the headings that already contain links (good insertion points), and a few sample existing links for context.",
			InputSchema: mcpSchema("object", map[string]any{
				"title":    mcpPropString("Working title of the new page, e.g. 'Kubernetes Deployment'."),
				"keywords": mcpPropStringArray("Optional extra topic keywords to widen the relevance match, e.g. ['deploy', 'infrastructure']."),
				"limit":    map[string]any{"type": "number", "description": "Maximum number of suggestions to return (default 5)."},
			}, []string{"title"}),
		},
		{
			Name:        "list_images",
			Section:     MCPSectionRead,
			Description: "List all uploaded images with metadata (name, size, modification time, which pages use them).",
			InputSchema: mcpSchema("object", nil, nil),
		},
		{
			Name:        "page_history",
			Section:     MCPSectionRead,
			Description: "Get the git revision history for a wiki page.",
			InputSchema: mcpSchema("object", map[string]any{
				"slug":  mcpPropString("Page slug"),
				"count": map[string]any{"type": "number", "description": "Max entries to return (default 20)"},
			}, []string{"slug"}),
		},
		{
			Name:        "get_page_revision",
			Section:     MCPSectionRead,
			Description: "Get the content of a wiki page at a specific git revision.",
			InputSchema: mcpSchema("object", map[string]any{
				"slug": mcpPropString("Page slug"),
				"hash": mcpPropString("Git commit hash"),
			}, []string{"slug", "hash"}),
		},
		{
			Name:    "page_links",
			Section: MCPSectionRead,
			Description: "Inspect the [[wiki links]] connected to a page. " +
				"By default returns both outgoing links (pages this page links to) and incoming links/backlinks (pages that link here). " +
				"Pass 'direction: out' or 'direction: in' to get just one side. " +
				"Every page should have at least one backlink to be discoverable — a page with no backlinks is orphaned.",
			InputSchema: mcpSchema("object", map[string]any{
				"slug":      mcpPropString("Page slug to inspect"),
				"direction": mcpPropString("Which links to return: 'out' (outgoing), 'in' (backlinks), or 'both' (default)."),
			}, []string{"slug"}),
		},
		{
			Name:    "link_graph",
			Section: MCPSectionRead,
			Description: "Explore the wiki link structure. Modes:\n" +
				"(1) Default: returns the full map of every page slug to the slugs it links to.\n" +
				"(2) 'format: tree': an indented text outline of the wiki starting from favorites (or Home), showing how pages nest — the best way to see overall structure.\n" +
				"(3) 'slug' + optional 'depth': the local neighborhood subgraph within N link hops of a page (default depth 1).\n" +
				"(4) 'orphans_only: true': just the pages that nothing links to (candidates that need a parent link).\n" +
				"Prefer the scoped modes over the full map on large wikis.",
			InputSchema: mcpSchema("object", map[string]any{
				"format":       mcpPropString("'map' (default, slug→links object) or 'tree' (indented outline from favorites/Home)."),
				"slug":         mcpPropString("If set, return only the neighborhood subgraph around this page."),
				"depth":        map[string]any{"type": "number", "description": "Neighborhood radius in link hops when 'slug' is set (default 1)."},
				"orphans_only": map[string]any{"type": "boolean", "description": "If true, return only pages with no backlinks."},
			}, nil),
		},

		// ── Edit tools ───────────────────────────────────────────────
		{
			Name:    "create_page",
			Section: MCPSectionEdit,
			Description: "Create a new wiki page. Use this when the user says things like 'document on my wiki', 'add a note to my wiki', 'save this to the wiki', or 'create a wiki page'. " +
				"Fails if the page already exists. " +
				"The slug is derived from the title (spaces become underscores, e.g. 'My Page' → 'My_Page'). " +
				"IMPORTANT: pass 'link_from' with a parent page (e.g. 'Home' or a relevant category page) so the new page is discoverable — every page should be linked from at least one other page. Use suggest_page_location first if unsure which parent to use. " +
				"To import MediaWiki wikitext instead of Markdown, pass 'format: mediawiki' and put the wikitext in 'content'. " +
				wikiFormattingGuide,
			InputSchema: mcpSchema("object", map[string]any{
				"title":        mcpPropString("Page title, e.g. 'My New Page'. This becomes the slug and the display title."),
				"content":      mcpPropString("Page content. Markdown by default, or MediaWiki wikitext when format=mediawiki. " + wikiContentGuide),
				"format":       mcpPropString("'markdown' (default) or 'mediawiki' (content is wikitext and will be converted to Markdown)."),
				"link_from":    mcpPropString("Slug or title of an existing page to add a [[link]] to this new page from, making it discoverable. Strongly recommended."),
				"link_section": mcpPropString("Optional heading name within the link_from page under which to add the link (e.g. 'Pages'). Defaults to the end of that page."),
			}, []string{"title", "content"}),
		},
		{
			Name:    "edit_page",
			Section: MCPSectionEdit,
			Description: "Update the content of an existing wiki page. Supports several modes:\n" +
				"(1) SEARCH-AND-REPLACE (preferred for small edits): pass 'old_text' and 'new_text' to find and replace text. old_text must match exactly one location.\n" +
				"(2) SECTION EDIT: pass 'section' (a # heading name) and 'content' to replace just that section's body. Use get_page with sections_only=true to discover section names.\n" +
				"(3) APPEND: pass 'append: true' and 'content' to add text to the end of the page.\n" +
				"(4) FULL REPLACE: pass only 'content' to replace the entire page (use get_page first).\n" +
				"To replace the whole page from MediaWiki wikitext, pass 'format: mediawiki' with 'content' (converted to Markdown; other modes ignore format). " +
				"If you do not know the exact slug, use search_pages first — do NOT guess slugs. " +
				"When adding [[wiki links]] to new pages, make sure those pages exist or will be created. " +
				wikiFormattingGuide,
			InputSchema: mcpSchema("object", map[string]any{
				"slug":     mcpPropString("Exact page slug to edit, e.g. 'My_Page'. Use search_pages first if unsure."),
				"content":  mcpPropString("New content. For full replace: entire page. For section edit: section body (# heading line is preserved). For append: text to add at end. " + wikiContentGuide),
				"old_text": mcpPropString("Text to find in the page (search-and-replace mode). Must match exactly one location. Provide with new_text."),
				"new_text": mcpPropString("Replacement text (search-and-replace mode). Provide with old_text. Can be empty to delete matched text."),
				"section":  mcpPropString("Heading name of the section to replace (case-insensitive, any level — ## works too). Use with 'content'. The heading line is preserved."),
				"append":   map[string]any{"type": "boolean", "description": "If true, append 'content' to the end of the page instead of replacing."},
				"format":   mcpPropString("'markdown' (default) or 'mediawiki' (full-replace only: 'content' is wikitext, converted to Markdown)."),
			}, []string{"slug"}),
		},

		// ── Delete tools ─────────────────────────────────────────────
		{
			Name:        "delete_page",
			Section:     MCPSectionDelete,
			Description: "Delete a wiki page permanently.",
			InputSchema: mcpSchema("object", map[string]any{
				"slug": mcpPropString("Page slug to delete"),
			}, []string{"slug"}),
		},
		{
			Name:        "delete_image",
			Section:     MCPSectionDelete,
			Description: "Delete an uploaded image by filename.",
			InputSchema: mcpSchema("object", map[string]any{
				"filename": mcpPropString("Image filename, e.g. 'photo-20250101-ab12cd34.png'"),
			}, []string{"filename"}),
		},

		// ── Skill tools ──────────────────────────────────────────────
		{
			Name:    "list_skills",
			Section: MCPSectionSkills,
			Description: "List all skills (procedural knowledge pages for AI retrieval). " +
				"Returns each skill's slug, title, and tags. " +
				"Skills document how to perform tasks — build processes, testing conventions, deployment steps, coding patterns. " +
				"Prefer search_skills over this tool when looking for specific skills — " +
				"do not list all skills just to find one.",
			InputSchema: mcpSchema("object", nil, nil),
		},
		{
			Name:    "get_skill",
			Section: MCPSectionSkills,
			Description: "Get the raw markdown content of a skill by its exact slug. " +
				"ONLY use this when you already know the exact slug. " +
				"If you are not sure of the exact slug, use search_skills first to find it. " +
				"Do NOT guess slugs — search instead. " +
				"Optional: pass 'section' to get only a specific section (matched by heading name, any level), " +
				"or 'sections_only: true' to list just the section headings (shown with # prefix to indicate level).",
			InputSchema: mcpSchema("object", map[string]any{
				"slug":          mcpPropString("Exact skill slug, e.g. 'Go_Testing_Conventions'. Slugs use underscores for spaces. Must be exact — use search_skills if unsure."),
				"section":       mcpPropString("Return only the named section (matched by # heading name, case-insensitive). Omit to get full content."),
				"sections_only": map[string]any{"type": "boolean", "description": "If true, return only the list of # section headings (not content)."},
			}, []string{"slug"}),
		},
		{
			Name:    "create_skill",
			Section: MCPSectionSkills,
			Description: "Create a new skill (procedural knowledge for AI retrieval). " +
				"ONLY use this tool when the user explicitly mentions 'skill' (e.g. 'add a skill', 'create a skill', 'save this as a skill'). " +
				"Do NOT use for general wiki notes or documentation — use create_page instead. " +
				"Skills document how to perform tasks — build processes, testing conventions, deployment steps, coding patterns. " +
				"Recommended structure: start with '# Title', then a brief description of what this skill covers, " +
				"a '## When to Use' section describing when to apply it, " +
				"a '## Instructions' section with the actual steps, " +
				"and end with a 'Tags: keyword1, keyword2, ...' line for discoverability (skills are found by tags/search, not links). " +
				"The slug is derived from the title (spaces become underscores). " +
				"Fails if the skill already exists.",
			InputSchema: mcpSchema("object", map[string]any{
				"title":   mcpPropString("Skill title, e.g. 'Go Testing Conventions'. This becomes the slug and display title."),
				"content": mcpPropString("Markdown content for the skill. Start with '# Title' as the first line."),
			}, []string{"title", "content"}),
		},
		{
			Name:    "edit_skill",
			Section: MCPSectionSkills,
			Description: "Update the content of an existing skill. " +
				"ONLY use this tool when the user explicitly mentions 'skill'. " +
				"Do NOT use for general wiki page edits — use edit_page instead. " +
				"If you do not know the exact slug, use search_skills first — do NOT guess slugs. " +
				"Supports the same edit modes as edit_page:\n" +
				"(1) SEARCH-AND-REPLACE: pass 'old_text' and 'new_text'.\n" +
				"(2) SECTION EDIT: pass 'section' and 'content'.\n" +
				"(3) APPEND: pass 'append: true' and 'content'.\n" +
				"(4) FULL REPLACE: pass only 'content'.",
			InputSchema: mcpSchema("object", map[string]any{
				"slug":     mcpPropString("Exact skill slug to edit, e.g. 'Go_Testing_Conventions'. Use search_skills first if unsure."),
				"content":  mcpPropString("New markdown content. For full replace: entire skill. For section edit: section body. For append: text to add."),
				"old_text": mcpPropString("Text to find (search-and-replace mode). Must match exactly one location. Provide with new_text."),
				"new_text": mcpPropString("Replacement text (search-and-replace mode). Provide with old_text."),
				"section":  mcpPropString("Heading name of the section to replace (case-insensitive, any level — ## works too). Use with 'content'."),
				"append":   map[string]any{"type": "boolean", "description": "If true, append 'content' to end of skill."},
			}, []string{"slug"}),
		},
		{
			Name:        "delete_skill",
			Section:     MCPSectionSkills,
			Description: "Delete a skill permanently. ONLY use when the user explicitly asks to delete a skill.",
			InputSchema: mcpSchema("object", map[string]any{
				"slug": mcpPropString("Skill slug to delete"),
			}, []string{"slug"}),
		},
		{
			Name:    "search_skills",
			Section: MCPSectionSkills,
			Description: "Search for procedural skills/instructions by keyword. " +
				"Searches across skill titles, tags, and content with tag matches ranked highest. " +
				"Use this before starting implementation tasks (writing code, tests, builds, deployments) " +
				"to find relevant conventions and instructions. " +
				"Example: search for 'go testing' before writing Go tests. " +
				"Multiple queries can be provided to search for different topics at once. " +
				"Returns the full skill content when exactly one match is found.",
			InputSchema: mcpSchema("object", map[string]any{
				"query": mcpPropStringArray("Search queries — each is split into terms, each prefix-matched. E.g. ['go testing', 'deploy kubernetes']. Multiple queries search for different topics in one call."),
				"limit": map[string]any{"type": "number", "description": "Maximum number of results to return when multiple match (default 10)."},
			}, []string{"query"}),
		},
	}

	var tools []mcpTool
	for _, t := range allTools {
		if m.sections[t.Section] {
			if t.Annotations == nil {
				t.Annotations = sectionAnnotations(t.Section, t.Name)
			}
			tools = append(tools, t)
		}
	}
	return tools
}
