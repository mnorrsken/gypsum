# MCP Server

Gypsum has a built-in [MCP](https://modelcontextprotocol.io/) (Model Context Protocol) endpoint using Streamable HTTP transport. This lets AI assistants like Claude interact with your wiki — no separate binary needed.

## Endpoints

| Endpoint | Auth | Notes |
|---|---|---|
| `/mcp` | OAuth 2.0 (PKCE) | Main endpoint |
| `/mcp/external` | OAuth 2.0 (PKCE) | Backwards-compatible alias for `/mcp` |

Both endpoints are identical. `{{secure_aes:...}}` ciphertext is passed through unchanged in both directions, so AI assistants can read and edit pages that contain encrypted fields without corrupting them.

MCP is only exposed when OAuth is configured — if `GYPSUM_OAUTH_ENABLED` is not set, the `/mcp` endpoint is not registered.

## Setup

Enable OAuth by setting the required environment variables (see [Configuration](configuration.md)):

```bash
GYPSUM_OAUTH_ENABLED=true
GYPSUM_OAUTH_PASSWORD=your-password
GYPSUM_EXTERNAL_URL=https://your-wiki.example.com
```

If you're using Authelia, add bypass rules for the OAuth and MCP paths — see [Authentication](authentication.md).

## Connecting from Claude

Add a **remote MCP server** in Claude's settings:

```
URL: https://your-wiki.example.com/mcp
```

Claude will detect the 401 response, follow the OAuth discovery documents, and exchange credentials for a Bearer token automatically. Tokens are valid for 24 hours by default.

## Claude Desktop (stdio proxy)

For local use or when the wiki isn't publicly accessible, use the `mcp-proxy` binary. It bridges stdio to the remote HTTP endpoint.

Build it with `make build`, then obtain a token:

```bash
mcp-proxy auth https://wiki.example.com --password=your-password
```

You can also use the `GYPSUM_PASSWORD` environment variable instead of `--password`, or omit both to be prompted interactively.

Store the token and add it to your Claude Desktop config:

**macOS/Linux:** `~/.config/claude/claude_desktop_config.json`  
**Windows:** `%APPDATA%\Claude\claude_desktop_config.json`

```json
{
  "mcpServers": {
    "gypsum": {
      "command": "/path/to/mcp-proxy",
      "args": ["https://wiki.example.com/mcp"],
      "env": {
        "GYPSUM_TOKEN": "paste-token-here"
      }
    }
  }
}
```

Tokens expire after 24 hours. Re-run `mcp-proxy auth` to get a fresh token and update the config.

## Available Tools

Tools are grouped into sections (read, edit, delete, skills) that can be enabled independently. Several tools were consolidated in v0.47.0 — see the notes below the table.

### Page tools

| Tool | Section | Description |
|---|---|---|
| `list_pages` | read | List wiki pages. `sort: recent` orders by last-modified (adds a `modified` timestamp), `favorites_only: true` returns just pinned pages, `limit` caps results. Prefer `search_pages` to find a specific page. |
| `get_page` | read | Read a page's markdown by exact slug. Supports `section`, `sections_only`, and `include_links` (append outgoing links + backlinks). |
| `search_pages` | read | Relevance-ranked (BM25) full-text search across pages. Accepts multiple `query` strings and a `limit`; results include snippets and link counts. |
| `suggest_page_location` | read | Suggest good parent pages to link a **new** page from — ranked by relevance and link-graph position. Use before `create_page` to pick `link_from`. |
| `list_images` | read | List uploaded images with metadata (name, size, mtime, using pages). |
| `page_history` | read | Git revision history for a page. |
| `get_page_revision` | read | Page content at a specific git revision. |
| `page_links` | read | Wiki links connected to a page. `direction: out`/`in`/`both` (default) for outgoing links, backlinks, or both. |
| `link_graph` | read | Explore link structure. Modes: full `map` (default), `format: tree` outline, `slug`+`depth` neighborhood subgraph, or `orphans_only: true`. |
| `create_page` | edit | Create a new page. `link_from`/`link_section` links it from a parent for discoverability; `format: mediawiki` imports wikitext. |
| `edit_page` | edit | Update an existing page (see edit modes below); `format: mediawiki` on full-replace imports wikitext. |
| `delete_page` | delete | Delete a page permanently. |
| `delete_image` | delete | Delete an uploaded image by filename. |

### Skill tools

| Tool | Section | Description |
|---|---|---|
| `list_skills` | skills | List all skills with slug, title, and tags. |
| `search_skills` | skills | Tag-boosted search across skills; accepts multiple `query` strings and a `limit`. |
| `get_skill` | skills | Read a skill's markdown by exact slug (supports `section` and `sections_only`). |
| `create_skill` | skills | Create a new skill. |
| `edit_skill` | skills | Update an existing skill (same edit modes as `edit_page`). |
| `delete_skill` | skills | Delete a skill. |

See [Skills](skills.md) for how to create and use skills with LLMs.

**Consolidations (v0.47.0):** `list_pages` absorbed the former `get_recent_pages` and `get_favorites` (now `sort: recent` / `favorites_only: true`); `page_links` absorbed `what_links_here` (now `direction: in`); and `create_page`/`edit_page` absorbed the standalone MediaWiki tools (now `format: mediawiki`). The MCP surface is 19 tools.

## Edit Modes for `edit_page` and `edit_skill`

Both tools support four edit modes to minimise the amount of content sent over the wire:

| Mode | Parameters | When to use |
|---|---|---|
| **Search-and-replace** | `old_text` + `new_text` | Small targeted edits. `old_text` must match exactly one location. |
| **Section edit** | `section` + `content` | Replace one named section. Use `sections_only: true` on `get_page` first to discover section names. Heading line is preserved automatically. |
| **Append** | `append: true` + `content` | Add text to the end of the page. |
| **Full replace** | `content` only | Replace the entire page. Fetch the current content with `get_page` first. |

Section names are matched case-insensitively and leading `#` markers are ignored, so `"## My Section"` and `"my section"` both work. If a heading appears more than once, the tool returns an error and suggests using search-and-replace instead.

## Metrics

When `GYPSUM_METRICS_PORT` is set (default `:9090`), Gypsum exposes a `/metrics` endpoint with Prometheus counters for each MCP tool call, labelled by tool name, status, and direction. See [Configuration](configuration.md) for details.
