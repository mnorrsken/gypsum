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
			Description: "List all wiki pages (alphabetically sorted). Returns page slugs and titles. " +
				"Prefer search_pages over this tool when looking for specific pages — " +
				"do not list all pages just to find one.",
			InputSchema: mcpSchema("object", nil, nil),
		},
		{
			Name:    "get_page",
			Section: MCPSectionRead,
			Description: "Get the raw markdown content of a wiki page. " +
				"The returned content is the full markdown source including any wiki-specific syntax.",
			InputSchema: mcpSchema("object", map[string]any{
				"slug": mcpPropString("Page slug, e.g. 'Home' or 'My_Page'. Slugs use underscores for spaces."),
			}, []string{"slug"}),
		},
		{
			Name:        "search_pages",
			Section:     MCPSectionRead,
			Description: "Full-text search across all wiki pages. Uses FTS5 indexing for fast, relevant results with BM25 ranking. Each query is split into terms (punctuation ignored); each term is prefix-matched, so 'arch' finds 'architecture'. Results include context snippets showing where terms were found in the page content. Multiple queries can be provided to search for different topics at once.",
			InputSchema: mcpSchema("object", map[string]any{
				"query": mcpPropStringArray("Search queries — each is split into terms on whitespace/punctuation, each prefix-matched independently. Multiple queries search for different topics in one call."),
			}, []string{"query"}),
		},
		{
			Name:        "list_images",
			Section:     MCPSectionRead,
			Description: "List all uploaded images with metadata (name, size, modification time, which pages use them).",
			InputSchema: mcpSchema("object", nil, nil),
		},
		{
			Name:        "get_recent_pages",
			Section:     MCPSectionRead,
			Description: "Get the most recently modified wiki pages.",
			InputSchema: mcpSchema("object", map[string]any{
				"count": map[string]any{"type": "number", "description": "Number of pages to return (default 10)"},
			}, nil),
		},
		{
			Name:        "get_favorites",
			Section:     MCPSectionRead,
			Description: "Get the list of favorite/pinned pages from the wiki sidebar.",
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
			Name:        "page_links",
			Section:     MCPSectionRead,
			Description: "Get all outgoing wiki links from a page. Returns the slugs of pages that this page links to via [[Page Title]] syntax.",
			InputSchema: mcpSchema("object", map[string]any{
				"slug": mcpPropString("Page slug to inspect"),
			}, []string{"slug"}),
		},
		{
			Name:    "what_links_here",
			Section: MCPSectionRead,
			Description: "Find all pages that link to a given page (backlinks/parent pages). " +
				"Every page should be linked from at least one other page to be discoverable in the wiki.",
			InputSchema: mcpSchema("object", map[string]any{
				"slug": mcpPropString("Target page slug to find backlinks for"),
			}, []string{"slug"}),
		},
		{
			Name:    "link_graph",
			Section: MCPSectionRead,
			Description: "Get the full wiki link graph. Returns a map of every page slug to the list of slugs it links to. " +
				"Useful for understanding the overall wiki structure and finding orphaned pages.",
			InputSchema: mcpSchema("object", nil, nil),
		},

		// ── Edit tools ───────────────────────────────────────────────
		{
			Name:    "create_page",
			Section: MCPSectionEdit,
			Description: "Create a new wiki page. Use this when the user says things like 'document on my wiki', 'add a note to my wiki', 'save this to the wiki', or 'create a wiki page'. " +
				"Fails if the page already exists. " +
				"The slug is derived from the title (spaces become underscores, e.g. 'My Page' → 'My_Page'). " +
				"IMPORTANT: After creating a page, always add a [[Page Title]] link to it from at least one parent page (e.g. Home or a relevant category page) so it is discoverable. " +
				wikiFormattingGuide,
			InputSchema: mcpSchema("object", map[string]any{
				"title":   mcpPropString("Page title, e.g. 'My New Page'. This becomes the slug and the display title."),
				"content": mcpPropString("Markdown content for the page. " + wikiContentGuide),
			}, []string{"title", "content"}),
		},
		{
			Name:    "edit_page",
			Section: MCPSectionEdit,
			Description: "Update the content of an existing wiki page. Use this when the user says things like 'update my wiki', 'edit the wiki page', or 'add this to my wiki'. " +
				"Replaces the entire page content. " +
				"Always use get_page first to read the current content before editing. " +
				"When adding [[wiki links]] to new pages, make sure those pages exist or will be created. " +
				wikiFormattingGuide,
			InputSchema: mcpSchema("object", map[string]any{
				"slug":    mcpPropString("Page slug to edit, e.g. 'My_Page'"),
				"content": mcpPropString("New markdown content (replaces entire page). " + wikiContentGuide),
			}, []string{"slug", "content"}),
		},
		{
			Name:    "create_page_from_mediawiki",
			Section: MCPSectionEdit,
			Description: "Create a new wiki page from MediaWiki wikitext. ONLY use this tool when importing content from a MediaWiki source — " +
				"for normal page creation, use create_page with Markdown instead. " +
				"The wikitext is automatically converted to Markdown. " +
				"Handles: '''bold'''/''italic'', == headings ==, <syntaxhighlight>/<source>/<pre>/<nowiki>/<code>, " +
				"* and # lists, {| tables |}, [[wiki links]], [external links], categories, templates, refs, and " +
				"MediaWiki space-prefixed preformatted lines. " +
				"Fails if the page already exists. " +
				"IMPORTANT: After creating a page, always add a [[Page Title]] link to it from at least one parent page.",
			InputSchema: mcpSchema("object", map[string]any{
				"title":    mcpPropString("Page title, e.g. 'My New Page'. This becomes the slug and display title."),
				"wikitext": mcpPropString("MediaWiki wikitext source to convert and save as the page content."),
			}, []string{"title", "wikitext"}),
		},
		{
			Name:    "edit_page_from_mediawiki",
			Section: MCPSectionEdit,
			Description: "Update an existing wiki page from MediaWiki wikitext. ONLY use this tool when importing content from a MediaWiki source — " +
				"for normal page editing, use edit_page with Markdown instead. " +
				"The wikitext is automatically converted to Markdown and replaces the entire page content. " +
				"Handles: '''bold'''/''italic'', == headings ==, <syntaxhighlight>/<source>/<pre>/<nowiki>/<code>, " +
				"* and # lists, {| tables |}, [[wiki links]], [external links], categories, templates, refs, and " +
				"MediaWiki space-prefixed preformatted lines.",
			InputSchema: mcpSchema("object", map[string]any{
				"slug":     mcpPropString("Page slug to edit, e.g. 'My_Page'"),
				"wikitext": mcpPropString("MediaWiki wikitext source to convert and save as the new page content."),
			}, []string{"slug", "wikitext"}),
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
			Description: "Get the raw markdown content of a skill. " +
				"The returned content is the full markdown source.",
			InputSchema: mcpSchema("object", map[string]any{
				"slug": mcpPropString("Skill slug, e.g. 'Go_Testing_Conventions'. Slugs use underscores for spaces."),
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
				"and end with a 'Tags: keyword1, keyword2, ...' line for discoverability. " +
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
				"ONLY use this tool when the user explicitly mentions 'skill' (e.g. 'update the skill', 'edit this skill'). " +
				"Do NOT use for general wiki page edits — use edit_page instead. " +
				"Replaces the entire skill content. Always use get_skill first to read the current content before editing.",
			InputSchema: mcpSchema("object", map[string]any{
				"slug":    mcpPropString("Skill slug to edit, e.g. 'Go_Testing_Conventions'"),
				"content": mcpPropString("New markdown content (replaces entire skill)."),
			}, []string{"slug", "content"}),
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
			}, []string{"query"}),
		},
	}

	var tools []mcpTool
	for _, t := range allTools {
		if m.sections[t.Section] {
			tools = append(tools, t)
		}
	}
	return tools
}
