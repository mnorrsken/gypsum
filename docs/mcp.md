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

| Tool | Description |
|---|---|
| `list_pages` | List all wiki pages |
| `get_page` | Read a page's markdown content (supports `section` and `sections_only` parameters) |
| `create_page` | Create a new page |
| `edit_page` | Update an existing page (see edit modes below) |
| `delete_page` | Delete a page |
| `search_pages` | Relevance-ranked full-text search across pages |
| `list_images` | List uploaded images with metadata |
| `delete_image` | Delete an image |
| `get_recent_pages` | Recently modified pages |
| `get_favorites` | Favorite/pinned pages |
| `page_history` | Git revision history for a page |
| `get_page_revision` | Page content at a specific revision |
| `page_links` | Outgoing wiki links from a page |
| `what_links_here` | Backlinks to a given page |
| `link_graph` | Full wiki link graph |
| `create_page_from_mediawiki` | Create a page from MediaWiki wikitext |
| `edit_page_from_mediawiki` | Update a page from MediaWiki wikitext |
| `list_skills` | List all skills with tags |
| `get_skill` | Read a skill's markdown content (supports `section` and `sections_only` parameters) |
| `create_skill` | Create a new skill |
| `edit_skill` | Update an existing skill (same edit modes as `edit_page`) |
| `delete_skill` | Delete a skill |
| `search_skills` | Tag-boosted search across skills |

Skills tools (`list_skills`, `search_skills`, etc.) are included in the table above. See [Skills](skills.md) for how to create and use skills with LLMs.

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
