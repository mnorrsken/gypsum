# Configuration

## Application Variables

| Variable | Default | Description |
|---|---|---|
| `GYPSUM_SECRET_KEY` | `change-me-in-production` | Passphrase used to derive the AES-256-GCM encryption key for secure fields. **Set this in production.** |
| `GYPSUM_GIT_PULL_INTERVAL` | `5m` | How often to pull from the git remote (Go duration string, e.g. `2m`, `30s`). Only used when a git remote is configured. |
| `GYPSUM_AUTH_USER_HEADER` | _(empty)_ | Set to a header name (e.g. `Remote-User`) to enable reverse proxy authentication. The username is used as the git commit author. |
| `GYPSUM_AUTH_GROUP_HEADER` | `Remote-Group` | Header containing comma-separated group names. Only used when `GYPSUM_AUTH_USER_HEADER` is set. |
| `GYPSUM_AUTH_REQUIRED_GROUP` | _(empty)_ | If set, the group header must contain this group or the request is rejected with 403. |
| `GYPSUM_PROBE_PORT` | `:9091` | Listen address for the health-probe server (`/healthz`, `/readyz`). |

## OAuth Variables

These are required when enabling the OAuth-protected external MCP endpoint.

| Variable | Default | Description |
|---|---|---|
| `GYPSUM_OAUTH_ENABLED` | _(empty)_ | Set to `true` to enable the `/mcp/external` endpoint. |
| `GYPSUM_OAUTH_PASSWORD` | _(required)_ | Single-user password for the OAuth login page. |
| `GYPSUM_EXTERNAL_URL` | _(required)_ | Public base URL with no trailing slash, e.g. `https://wiki.example.com`. Used for OAuth discovery documents. |
| `GYPSUM_OAUTH_TOKEN_TTL` | `24h` | Access token lifetime (Go duration string). |

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
