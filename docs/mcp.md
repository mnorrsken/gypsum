# MCP Server

Gypsum has a built-in [MCP](https://modelcontextprotocol.io/) (Model Context Protocol) endpoint using Streamable HTTP transport. This lets AI assistants like Claude interact with your wiki — no separate binary needed.

## Endpoints

| Endpoint | Auth | Secure fields | Use case |
|---|---|---|---|
| `/mcp` | None (rely on reverse proxy) | Visible as ciphertext | Local / trusted network |
| `/mcp/external` | OAuth 2.0 (built-in, PKCE) | Redacted as `[encrypted field]` | Internet-facing |

## Connecting from Claude (Internal)

Add a **remote MCP server** in Claude's settings:

```
URL: https://your-wiki.example.com/mcp
```

Protect this endpoint with your reverse proxy (e.g. Authelia) so it is not world-accessible.

## Connecting from Claude (OAuth)

Enable OAuth by setting the required environment variables (see [Configuration](configuration.md)):

```bash
GYPSUM_OAUTH_ENABLED=true
GYPSUM_OAUTH_PASSWORD=your-password
GYPSUM_EXTERNAL_URL=https://your-wiki.example.com
```

Then add a remote MCP connector in Claude pointing at:

```
URL: https://your-wiki.example.com/mcp/external
```

Claude will detect the 401 response, follow the OAuth discovery documents, redirect you to the login page, and exchange the code for a Bearer token automatically. Tokens are valid for 24 hours by default.

If you're using Authelia, add bypass rules for the OAuth and MCP paths — see [Authentication](authentication.md).

## Claude Desktop (stdio proxy)

For local use or when the wiki isn't publicly accessible, use the `mcp-proxy` binary. It bridges stdio to the remote HTTP endpoint.

Build it with `make build`, then configure Claude Desktop:

**macOS/Linux:** `~/.config/claude/claude_desktop_config.json`
**Windows:** `%APPDATA%\Claude\claude_desktop_config.json`

```json
{
  "mcpServers": {
    "gypsum": {
      "command": "/path/to/mcp-proxy",
      "args": ["https://wiki.example.com/mcp"]
    }
  }
}
```

## Available Tools

| Tool | Description |
|---|---|
| `list_pages` | List all wiki pages |
| `get_page` | Read a page's markdown content |
| `create_page` | Create a new page |
| `edit_page` | Update an existing page |
| `delete_page` | Delete a page |
| `search_pages` | Relevance-ranked search across pages |
| `list_images` | List uploaded images with metadata |
| `delete_image` | Delete an image |
| `upload_image` | Upload a base64-encoded image |
| `get_recent_pages` | Recently modified pages |
| `get_favorites` | Favorite/pinned pages |
| `page_history` | Git revision history for a page |
| `get_page_revision` | Page content at a specific revision |
| `page_links` | Outgoing wiki links from a page |
| `what_links_here` | Backlinks to a given page |
| `link_graph` | Full wiki link graph |
| `create_page_from_mediawiki` | Create a page from MediaWiki wikitext |
| `edit_page_from_mediawiki` | Update a page from MediaWiki wikitext |
