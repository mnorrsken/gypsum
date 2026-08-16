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
| `GYPSUM_MCP_SECTIONS` | `read,edit,delete,skills,notes` | Comma-separated list of MCP tool sections to enable. Omit a section to hide those tools from AI assistants. |
| `GYPSUM_MCP_ALLOWED_ORIGINS` | _(empty)_ | Extra browser origins permitted to call `/mcp`, comma-separated (e.g. `https://app.example.com`). `GYPSUM_EXTERNAL_URL` and loopback are always allowed. Set to `*` to disable Origin checking. See [Origin validation](#origin-validation). |
| `GYPSUM_METRICS_PORT` | `:9090` | Listen address for the Prometheus metrics server (`/metrics`). Exposes per-tool MCP call counters. |
| `GYPSUM_SECURE_SALT` | _(auto-generated)_ | Base64-encoded PBKDF2 salt for `{{secure:...}}` fields. If unset, a random salt is generated on first run and persisted in `gypsum.db`. The salt is not secret, but it must stay stable — see [Encryption](#encryption). |

### MCP sections

| Section | Tools |
|---|---|
| `read` | list_pages, get_page, search_pages, suggest_page_location, list_images, page_history, get_page_revision, page_links, link_graph |
| `edit` | create_page, edit_page |
| `delete` | delete_page, delete_image |
| `skills` | list_skills, get_skill, search_skills, create_skill, edit_skill, delete_skill |
| `notes` | list_notes, get_note, create_note, edit_note, archive_note, delete_note |

Example — read-only wiki for AI assistants:

```bash
GYPSUM_MCP_SECTIONS=read
```

### Origin validation

The MCP transport spec requires servers to validate the `Origin` header to
prevent DNS rebinding — without it, any web page your browser visits could drive
your wiki's MCP endpoint from inside your network. Gypsum rejects a request
carrying a disallowed `Origin` with HTTP 403.

Allowed by default:

- the origin of `GYPSUM_EXTERNAL_URL`
- loopback (`localhost`, `127.0.0.1`, `::1`) on any port and scheme

Requests with **no** `Origin` header are always allowed. Non-browser clients —
Claude's remote connector, `mcp-proxy`, curl — do not send one, so this check is
invisible to them.

Only browser-based MCP clients served from some other origin need widening:

```bash
GYPSUM_MCP_ALLOWED_ORIGINS=https://app.example.com,https://staging.example.com
```

Setting `GYPSUM_MCP_ALLOWED_ORIGINS=*` disables the check entirely and restores
the earlier permissive behavior. This is not recommended on a publicly reachable
wiki.

## Git Execution

Gypsum drives git by running the `git` binary. Every invocation is made
non-interactive and bounded so a wedged git process can never accumulate:

- **No prompts.** `GIT_TERMINAL_PROMPT=0` is forced and any `GIT_ASKPASS` /
  `SSH_ASKPASS` in the environment is stripped, so git fails fast on missing or
  bad credentials instead of blocking on a prompt.
- **Batch-mode SSH.** If you don't set `GIT_SSH_COMMAND` yourself, Gypsum uses
  `ssh -o BatchMode=yes -o ConnectTimeout=10`. Set your own `GIT_SSH_COMMAND`
  (e.g. to point at a deploy key) and it is used unchanged.
- **Stalled HTTP transfers abort.** `GIT_HTTP_LOW_SPEED_LIMIT=1000` /
  `GIT_HTTP_LOW_SPEED_TIME=30` — a transfer stuck below 1 KB/s for 30s fails.
- **Timeouts.** Network operations (fetch, push) are capped at 2 minutes, local
  ones (status, log, show, commit) at 30 seconds. On timeout the whole git
  process group is killed, including helpers such as `git-remote-https` and
  `ssh`, so nothing survives the command that started it.
- **Bounded concurrency.** Read-only commands behind history, diff and revision
  requests (HTTP and MCP) are limited to 4 concurrent git processes; write
  operations are serialized. A request burst can no longer fork an unbounded
  number of processes.

See also [Docker → Process Model](docker.md) for why the container image runs an
init process as PID 1.

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
