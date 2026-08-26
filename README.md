# Gypsum

A self-hosted personal wiki built for LLM-driven knowledge management. Pages are Markdown files in a git repo, and a built-in MCP server gives AI assistants full read/write access — so your LLM can build and maintain the wiki, not just read it.

![image](screenshot.png)

## Why

The most useful thing you can do with an LLM isn't generating text — it's building up a personal knowledge base over time. Point it at source material and it will produce a structured, interlinked wiki. Ask it questions and it will research across that wiki and write up the answers. File those answers back into the wiki and it gets smarter. Gypsum is the storage layer for that loop: plain Markdown files, full MCP tooling, and a skills system so the LLM knows your conventions without being told repeatedly.

## LLM Integration

### MCP Server

Gypsum has a built-in [MCP](https://modelcontextprotocol.io/) endpoint (Streamable HTTP). AI assistants like Claude can create pages, edit them, search across the wiki, follow backlinks, and traverse the link graph — no separate binary or plugin needed.

The endpoint is dual-era: it serves the current stateless `2026-07-28` revision and the older handshake-based revisions (`2025-11-25` back to `2024-11-05`) side by side, picking the era from how each client connects. Nothing to configure.

Both `/mcp` and `/mcp/external` require OAuth 2.0 (PKCE) when OAuth is configured — if OAuth is not configured, the endpoints are not exposed. For Claude Desktop or CI use, the `mcp-proxy` binary bridges stdio to the HTTP endpoint; run `mcp-proxy auth <url>` to obtain a token non-interactively.

Available tools: `list_pages`, `get_page`, `create_page`, `edit_page`, `delete_page`, `search_pages`, `suggest_page_location`, `page_links`, `link_graph`, `page_history`, `get_page_revision`, `list_images`, `delete_image`, and more. Editing tools support search-and-replace, section-scoped edits, and append mode for token-efficient updates. See [MCP Server](docs/mcp.md) for setup.

### Skills

Skills are procedural knowledge pages that teach LLMs **how** to do things — build steps, testing conventions, deployment patterns, coding standards. They live in a dedicated `skills/` directory with their own tag-boosted search.

The key idea: add a line to your project's `CLAUDE.md` (or any LLM's system prompt) telling it to search for relevant skills before starting a task. When you say "write tests for this Go package", the LLM calls `search_skills("go testing")`, finds your conventions, and follows them automatically. Corrections you make get saved as skills so the LLM doesn't repeat mistakes.

Skills tools: `list_skills`, `search_skills`, `get_skill`, `create_skill`, `edit_skill`, `delete_skill`. See [Skills](docs/skills.md).

### Prometheus Metrics

MCP tool usage is tracked via Prometheus — call counts, errors, and characters sent/received per tool. Useful for understanding how your LLM interacts with the wiki. Default endpoint: `:9090/metrics`.

## Wiki Features

- Markdown with GFM tables, syntax highlighting, `[[wiki links]]`, and interactive task-list checkboxes
- Full-text search with FTS5 indexing, BM25 ranking, and highlighted snippets
- Quick Notes — a whiteboard-style board of always-editable sticky notes with autosave, title-hashed colors, and archive; stored as plain markdown in git
- Secrets vault — a searchable list of credentials with click-to-reveal (60s) and copy-without-showing; tiles use the linked site's own picture or a title-hashed two-letter mnemonic. Encrypted in the browser, stored as markdown in git, never exposed over MCP
- Interactive link graph
- Page history with revision diffs
- Image uploads (paste, drag-and-drop, or picker) with size hints
- Inline encrypted fields (`{{secure:secret}}`) with AES-256-GCM and PBKDF2 key derivation, encrypted and decrypted entirely in the browser — the server never sees plaintext or the key
- Visual table editor
- MediaWiki import
- Public page sharing via secret links — shared pages are HTML-sanitized for anonymous viewers
- Git-backed storage with optional remote sync and a top-bar status indicator (green/amber/red) that surfaces fetch/push failures; git runs non-interactively with bounded timeouts and process-group cleanup (see [Git Execution](docs/configuration.md))
- Dark/light theme, responsive layout, print-friendly pages

## Quick Start

```bash
git clone https://github.com/mnorrsken/gypsum.git
cd gypsum
make build
make run
```

Open [http://localhost:8080](http://localhost:8080). See [Docker](docs/docker.md) or [Helm](docs/helm.md) for container deployment.

## Configuration

| Variable | Default | Description |
|---|---|---|
| `GYPSUM_GIT_PULL_INTERVAL` | `5m` | Pull interval when a git remote is configured |
| `GYPSUM_GIT_PUSH_DELAY` | `30s` | Debounce window coalescing rapid edits into one push; `0` pushes immediately |
| `GYPSUM_AUTH_USER_HEADER` | _(empty)_ | Header for reverse proxy auth (e.g. `Remote-User`) |
| `GYPSUM_METRICS_PORT` | `:9090` | Prometheus metrics server listen address |
| `GYPSUM_PROBE_PORT` | `:9091` | Health probe listen address |
| `GYPSUM_MCP_SECTIONS` | `read,edit,delete,skills,notes` | Comma-separated MCP tool sections to enable |
| `GYPSUM_MCP_ALLOWED_ORIGINS` | _(empty)_ | Extra browser origins allowed to call `/mcp`; external URL and loopback always allowed, `*` disables the check |
| `GYPSUM_SECURE_SALT` | _(auto-generated)_ | Base64 PBKDF2 salt for `{{secure:...}}` fields; auto-generated and persisted if unset |

OAuth, auth, and Docker-specific variables are documented in [Configuration](docs/configuration.md).

## Documentation

- [Usage](docs/usage.md) — pages, secure fields, images, search, favorites
- [Configuration](docs/configuration.md) — all environment variables
- [Authentication](docs/authentication.md) — header auth and Authelia setup
- [MCP Server](docs/mcp.md) — connecting AI assistants to your wiki
- [Skills](docs/skills.md) — procedural knowledge for LLMs
- [Quick Notes](docs/notes.md) — the sticky-note board
- [Secrets](docs/secrets.md) — the credential vault
- [Docker](docs/docker.md) — container build, run, and compose
- [Helm Chart](docs/helm.md) — Kubernetes deployment

## Data Directory

```
data/
├── gypsum.db           # SQLite database (shares, OAuth tokens, FTS search index)
└── repo/               # git working directory
    ├── pages/           # markdown files
    ├── skills/          # procedural knowledge for LLMs
    ├── notes/           # quick notes
    ├── secrets/         # encrypted secrets vault
    └── images/          # uploaded images
```

## Development

```bash
make help       # show all targets
make fmt        # format Go source
make test       # run unit tests
make build      # build binary
make run        # run on :8080
```

## License

Apache License 2.0 — see [LICENSE](LICENSE).
