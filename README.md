# Gypsum

A lightweight, self-hosted personal wiki built with Go. Pages are stored as plain Markdown files with automatic git versioning.

![image](screenshot.png)

## Features

- Markdown pages with GFM tables, syntax highlighting, and wiki-style `[[links]]`
- Inline encrypted fields (`{{secure:secret}}`) with AES-256-GCM
- Image uploads (paste, drag-and-drop, or image picker) with optional size hints
- Page history with revision diffs
- Full-text search with FTS5 indexing, BM25 ranking, and highlighted context snippets
- Auto git commits with optional remote sync
- Visual table editor
- Interactive link graph
- MediaWiki import
- Skills system for AI-retrievable procedural knowledge (build steps, testing conventions, etc.)
- MCP server for AI assistants (local and OAuth-protected endpoints) with multi-query search and duplicate-check on create
- Public page sharing via secret links
- Header authentication (Authelia, Authentik, etc.)
- Built-in documentation section (serves `docs/` as read-only wiki pages)
- Rekey CLI tool for rotating encryption keys
- Dark/light theme, responsive layout, print-friendly pages
- Docker and Helm chart support
- Health probes for Kubernetes

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
| `GYPSUM_SECRET_KEY` | `change-me-in-production` | AES-256-GCM encryption passphrase. **Set this in production.** |
| `GYPSUM_GIT_PULL_INTERVAL` | `5m` | Pull interval when a git remote is configured |
| `GYPSUM_AUTH_USER_HEADER` | _(empty)_ | Header name to enable reverse proxy auth (e.g. `Remote-User`) |
| `GYPSUM_AUTH_GROUP_HEADER` | `Remote-Group` | Header with comma-separated groups |
| `GYPSUM_AUTH_REQUIRED_GROUP` | _(empty)_ | Require this group or reject with 403 |
| `GYPSUM_PROBE_PORT` | `:9091` | Health probe listen address |
| `GYPSUM_MCP_SECTIONS` | `read,edit,delete,skills` | Comma-separated MCP tool sections to enable |

OAuth and Docker-specific variables are documented in [Configuration](docs/configuration.md).

## Documentation

- [Usage](docs/usage.md) — pages, secure fields, images, search, favorites
- [Configuration](docs/configuration.md) — all environment variables
- [Authentication](docs/authentication.md) — header auth and Authelia setup
- [MCP Server](docs/mcp.md) — connecting AI assistants to your wiki
- [Docker](docs/docker.md) — container build, run, and compose
- [Helm Chart](docs/helm.md) — Kubernetes deployment

## Data Directory

```
data/
├── gypsum.db           # SQLite database (shares, OAuth tokens, FTS search index)
└── repo/               # git working directory
    ├── pages/           # markdown files
    ├── skills/          # skill pages (procedural knowledge for AI)
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
