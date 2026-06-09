# Configuration

## Application Variables

| Variable | Default | Description |
|---|---|---|
| `GYPSUM_GIT_PULL_INTERVAL` | `5m` | How often to pull from the git remote (Go duration string, e.g. `2m`, `30s`). Only used when a git remote is configured. |
| `GYPSUM_GIT_PUSH_DELAY` | `30s` | Debounce window for pushes — edits within the window coalesce into a single push after the burst settles. Set to `0` to push immediately on every commit. Only used when a git remote is configured. |
| `GYPSUM_AUTH_USER_HEADER` | _(empty)_ | Set to a header name (e.g. `Remote-User`) to enable reverse proxy authentication. The username is used as the git commit author. |
| `GYPSUM_AUTH_GROUP_HEADER` | `Remote-Group` | Header containing comma-separated group names. Only used when `GYPSUM_AUTH_USER_HEADER` is set. |
| `GYPSUM_AUTH_REQUIRED_GROUP` | _(empty)_ | If set, the group header must contain this group or the request is rejected with 403. |
| `GYPSUM_PROBE_PORT` | `:9091` | Listen address for the health-probe server (`/healthz`, `/readyz`). |
| `GYPSUM_MCP_SECTIONS` | `read,edit,delete,skills` | Comma-separated list of MCP tool sections to enable. Omit a section to hide those tools from AI assistants. |
| `GYPSUM_METRICS_PORT` | `:9090` | Listen address for the Prometheus metrics server (`/metrics`). Exposes per-tool MCP call counters. |
| `GYPSUM_SECURE_SALT` | _(auto-generated)_ | Base64-encoded PBKDF2 salt for `{{secure:...}}` fields. If unset, a random salt is generated on first run and persisted in `gypsum.db`. The salt is not secret, but it must stay stable — see [Encryption](#encryption). |

### MCP sections

| Section | Tools |
|---|---|
| `read` | list_pages, get_page, search_pages, list_images, get_recent_pages, get_favorites, page_history, get_page_revision, page_links, what_links_here, link_graph |
| `edit` | create_page, edit_page, create_page_from_mediawiki, edit_page_from_mediawiki |
| `delete` | delete_page, delete_image |
| `skills` | list_skills, get_skill, create_skill, edit_skill, delete_skill, search_skills |

Example — read-only wiki for AI assistants:

```bash
GYPSUM_MCP_SECTIONS=read
```

## Encryption

Secure fields (`{{secure:...}}`) are encrypted and decrypted entirely in the
browser using AES-256-GCM. The passphrase never reaches the server. On first
visit, click the 🔒 icon in the top bar and enter your passphrase; tick
"Remember on this device" to persist the derived key in `localStorage`.

New blocks are written as `{{secure_aes2:...}}` with **PBKDF2-HMAC-SHA256**
(600,000 iterations) over the passphrase and the per-deployment
`GYPSUM_SECURE_SALT`. The salt is not secret — it only ensures two deployments
derive different keys from the same passphrase — but it must stay **stable**:
changing it makes existing `secure_aes2` blocks undecryptable. If you don't set
it, Gypsum generates one on first run and stores it in `gypsum.db`; for
multi-replica or rebuild-from-scratch deployments, set it explicitly (the Helm
chart generates and persists one in a Secret automatically).

Legacy `{{secure_aes:...}}` blocks (including pages from Gypsum ≤ 0.42.x that
used `GYPSUM_SECRET_KEY`) still decrypt with the same passphrase via the old
unsalted SHA-256 KDF — no migration is required. Editing and saving a page
upgrades its secure blocks to `secure_aes2`; to migrate in bulk, use
`gypsum re-encrypt` (see below).

### Migrating secure fields

`gypsum re-encrypt` rotates the passphrase and/or salt and upgrades legacy
blocks to `secure_aes2`:

```bash
gypsum re-encrypt -dir data/repo/pages \
  -old-key <old-passphrase> -new-key <new-passphrase> \
  -new-salt "$GYPSUM_SECURE_SALT" [-old-salt <base64>] [-dry-run]
```

Pass `-old-salt` when the directory already contains `secure_aes2` blocks that
must be decrypted; omit it when migrating only legacy `secure_aes` blocks.

## OAuth Variables

These are required when enabling the OAuth-protected external MCP endpoint.

| Variable | Default | Description |
|---|---|---|
| `GYPSUM_OAUTH_ENABLED` | _(empty)_ | Set to `true` to enable the `/mcp/external` endpoint. |
| `GYPSUM_OAUTH_PASSWORD` | _(required)_ | Single-user password for the OAuth login page. |
| `GYPSUM_EXTERNAL_URL` | _(required)_ | Public base URL with no trailing slash, e.g. `https://wiki.example.com`. Used for OAuth discovery documents. |
| `GYPSUM_OAUTH_TOKEN_TTL` | `24h` | Access token lifetime as a Go duration string (e.g. `2160h` for 90 days). Valid units: `s`, `m`, `h`. Note: `d` is **not** supported — use hours instead. |

## Docker Variables

These are used by the Docker entrypoint script in addition to the variables above.

| Variable | Default | Description |
|---|---|---|
| `GYPSUM_DATA_DIR` | `/app/data` | Path to the data directory inside the container. |
| `GYPSUM_GIT_INIT` | _(empty)_ | Set to `true` to initialize a git repo on startup. |
| `GYPSUM_GIT_REMOTE_NAME` | `origin` | Name of the git remote. |
| `GYPSUM_GIT_REMOTE_URL` | _(empty)_ | URL of a git remote for backup/sync. |
| `GYPSUM_GIT_USERNAME` | _(empty)_ | Git username for HTTPS authentication. |
| `GYPSUM_GIT_PASSWORD` | _(empty)_ | Git password for HTTPS authentication. |
| `GYPSUM_GIT_TOKEN` | _(empty)_ | Git token for HTTPS auth (takes precedence over username/password). |
| `GYPSUM_GIT_COMMIT_NAME` | _(empty)_ | Git commit author name. |
| `GYPSUM_GIT_COMMIT_EMAIL` | _(empty)_ | Git commit author email. |
| `GYPSUM_GIT_PULL_INTERVAL` | `5m` | How often to pull from the remote. |
| `GYPSUM_GIT_PUSH_DELAY` | `30s` | Debounce window for pushes; `0` disables debouncing. |
