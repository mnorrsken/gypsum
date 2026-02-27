# Gypsum

A lightweight, self-hosted personal wiki built with Go. Pages are stored as plain Markdown files in a `data/` directory with automatic git versioning, wiki-style `[[links]]`, inline encrypted fields, image uploads, and a clean modern UI.

![image](screenshot.png)

## Features

- **Markdown pages** — stored as `.md` files, rendered with [goldmark](https://github.com/yuin/goldmark) (GFM tables, syntax highlighting)
- **Wiki links** — `[[Page Title]]` creates inter-page links; clicking a link to a non-existent page opens the editor
- **Inline encryption** — `{{secure:secret}}` in the editor is AES-256-GCM encrypted on save; click the lock icon on a page to temporarily reveal the value (auto-hides after 60 seconds). Supports multiline blocks.
- **Content validation** — malformed or unknown custom tags are rejected on save with clear error messages
- **Page history** — view the git commit history for any page via the History tab
- **Image uploads** — paste from clipboard, drag-and-drop onto the editor, or click **Images** to browse and insert existing uploads. Filenames are derived from the original file name where available. Images auto-scale to fit the content width. Optional size hints: `![alt|500](url)` (px), `![alt|50%](url)` (percent), `![alt|800x400](url)` (width×height)
- **Favorites sidebar** — edit `_favorites.md` to pin pages in the sidebar
- **Full-text search** — searches page titles and content
- **Auto git commits** — every page save and image upload is committed to a git repo inside `data/`
- **Git remote sync** — optional push-after-commit and periodic pull with "ours wins" conflict resolution, configured via environment variables
- **Diff preview** — tick "Show diff" in the editor to review a colorized unified diff of the raw on-disk content before committing; the preference is remembered across sessions
- **Docker-ready** — single-container deployment with optional git remote sync
- **Dark/light theme** — toggle between dark and light mode via the button in the top bar; preference is remembered across sessions
- **Responsive layout** — mobile-friendly with a hamburger menu sidebar on small screens
- **Editor help panel** — expandable markdown and syntax reference panel next to the editor
- **Unicode support** — page slugs support non-ASCII characters (e.g. `Lösenord`)

## Quick Start

### Prerequisites

- Go 1.23+ (for building from source)
- Git (for auto-commit functionality)

### Build and Run

```bash
# Clone the repository
git clone https://github.com/mnorrsken/gypsum.git
cd gypsum

# Build
make build

# Run (listens on :8080)
make run
```

Open [http://localhost:8080](http://localhost:8080) in your browser.

### Environment Variables

| Variable | Default | Description |
|---|---|---|
| `GYPSUM_SECRET_KEY` | `change-me-in-production` | Passphrase used to derive the AES-256-GCM encryption key for secure fields. **Set this in production.** |
| `GYPSUM_GIT_PULL_INTERVAL` | `5m` | How often to pull from the git remote (Go duration string, e.g. `2m`, `30s`). Only used when `GYPSUM_GIT_REMOTE_URL` is set. |

## Usage

### Pages

- All pages live in `data/pages/` as `.md` files.
- Click **+ New Page** in the sidebar to create a page. You'll be prompted for a unique title.
- Use `[[Page Title]]` to link between pages. The title is converted to a slug (`Page_Title`) automatically.
- Each page has **Page**, **Edit**, and **History** tabs for quick navigation.

### Secure Fields

Store sensitive values inline in your markdown:

```
WiFi password: {{secure:my-secret-password}}
```

For multiline secrets, place `{{secure:` and `}}` on their own lines:

```
{{secure:
username: admin
password: s3cret
}}
```

On save, the `{{secure:...}}` block is encrypted with AES-256-GCM:

```
WiFi password: {{secure_aes:BASE64_CIPHERTEXT}}
```

When viewing the page, encrypted fields appear as `🔒****`. Click to decrypt and reveal the value for 60 seconds, then it auto-hides. A clipboard copy button appears next to revealed values. Multiline content renders with proper line breaks.

The editor validates custom tags on save — unknown tags, unclosed blocks, and improperly formatted multiline blocks are rejected with clear error messages.

### Page History

Click the **History** tab on any page to view its git commit log, showing revision hashes, dates, authors, and commit messages.

### Images

There are three ways to upload and insert images into the editor:

- **Paste** — copy an image to the clipboard and paste it into the editor textarea.
- **Drag-and-drop** — drag an image file onto the editor textarea; a dashed border highlights the drop zone.
- **Image picker** — click the **Images** button in the editor toolbar to open a thumbnail gallery of all uploaded images. Click any image to insert it, or use "Upload new…" to upload directly from the picker.

In all cases a markdown image reference (`![alt text](/images/filename.ext)`) is inserted at the cursor. The alt text and filename are derived from the original file name where available (e.g. `freddie-mercury-20260228-a1b2c3d4.jpg`); screenshots and browser-generated names fall back to a date-based name.

Images automatically scale down to fit the content area. You can also add an optional size hint to the alt text:

| Syntax | Effect |
|---|---|
| `![screenshot\|500](/images/foo.png)` | max-width: 500px |
| `![screenshot\|50%](/images/foo.png)` | max-width: 50% |
| `![screenshot\|800x400](/images/foo.png)` | width: 800px, height: 400px |

Manage images at `/images` (linked in the sidebar as **Images**). The index shows each image's thumbnail, filename, file size, and which pages reference it. Unused images can be deleted to free space.

Supported formats: PNG, JPG, JPEG, GIF, WEBP, SVG (max 10 MB).

### Diff Preview

Tick the "Show diff" checkbox in the editor before clicking Save. A colorized unified diff is displayed showing added lines (green) and removed lines (red) with surrounding context. The diff shows the raw on-disk content including `{{secure_aes:...}}` tags — encrypted fields whose plaintext hasn't changed keep stable ciphertext so they don't appear as noise. The checkbox preference is remembered across sessions via `localStorage`. Click **Confirm Save** to commit or **Back to Editor** to continue editing.

### Favorites

Edit the special `_favorites.md` page (linked at the bottom of the Favorites section in the sidebar) to pin pages:

```
[[Home]]
[[Scratch Pad]]
[[My Important Page]]
```

### Search

Use the search bar in the top navigation or visit `/search`. Searches page titles and content (case-insensitive).

## Docker

### Build the Image

```bash
make docker-build
# or
docker build -t gypsum:latest .
```

### Run the Container

```bash
make docker-run
# or
docker run --rm -p 8080:8080 -v $(pwd)/data:/app/data gypsum:latest
```

Mount `/app/data` to persist pages and images across container restarts.

### Docker Environment Variables

All variables from the table above apply, plus these Docker-specific variables used by the entrypoint script:

| Variable | Default | Description |
|---|---|---|
| `GYPSUM_DATA_DIR` | `/app/data` | Path to the data directory inside the container |
| `GYPSUM_GIT_INIT` | _(empty)_ | Set to `true` to initialize a git repo in the data directory on startup |
| `GYPSUM_GIT_REMOTE_NAME` | `origin` | Name of the git remote |
| `GYPSUM_GIT_REMOTE_URL` | _(empty)_ | URL of a git remote to configure (e.g. for backup/sync) |
| `GYPSUM_GIT_USERNAME` | _(empty)_ | Git username for HTTPS authentication |
| `GYPSUM_GIT_PASSWORD` | _(empty)_ | Git password for HTTPS authentication |
| `GYPSUM_GIT_TOKEN` | _(empty)_ | Git token for HTTPS authentication (takes precedence over username/password) |
| `GYPSUM_GIT_COMMIT_NAME` | _(empty)_ | Git commit author name |
| `GYPSUM_GIT_COMMIT_EMAIL` | _(empty)_ | Git commit author email |
| `GYPSUM_GIT_PULL_INTERVAL` | `5m` | How often to pull from the git remote (e.g. `2m`, `30s`) |

### Docker Compose Example

A ready-to-use [`docker-compose.yaml`](docker-compose.yaml) is included in the repository:

```bash
docker compose up -d
```

Adjust environment variables in the file to set your encryption key and optional git remote sync.

## Helm Chart

Gypsum includes a Helm chart for deploying to Kubernetes.

### Install from OCI Registry

```bash
helm install gypsum oci://ghcr.io/mnorrsken/charts/gypsum
```

### Install from Source

```bash
helm install gypsum ./charts/gypsum
```

### Configuration

See all available values:

```bash
helm show values oci://ghcr.io/mnorrsken/charts/gypsum
```

Key values:

| Parameter | Default | Description |
|---|---|---|
| `image.repository` | `ghcr.io/mnorrsken/gypsum` | Container image repository |
| `image.tag` | `""` (uses `appVersion`) | Image tag override |
| `gypsum.secretKey` | `""` | AES-256-GCM encryption key. If empty, a pre-install hook generates one automatically |
| `gypsum.existingSecret` | `""` | Name of an existing Secret (must contain a `secret-key` key) |
| `persistence.enabled` | `true` | Enable persistent storage for wiki data |
| `persistence.size` | `1Gi` | PVC size |
| `persistence.existingClaim` | `""` | Use an existing PVC |
| `ingress.enabled` | `false` | Enable Ingress |
| `git.init` | `true` | Initialize a git repo in the data directory |
| `git.commitName` | `Gypsum` | Git commit author name |
| `git.commitEmail` | `gypsum@local` | Git commit author email |
| `git.remoteUrl` | `""` | Git remote URL for backup/sync |
| `git.pullInterval` | `5m` | How often to pull from the remote (Go duration, e.g. `5m`, `30s`). Only used when `remoteUrl` is set |

### Secret Key Handling

The encryption key for secure fields (`GYPSUM_SECRET_KEY`) is handled in one of three ways:

1. **Auto-generated (default)** — When neither `gypsum.secretKey` nor `gypsum.existingSecret` is set, a pre-install hook runs a Job that generates a random 64-character key and stores it in a Kubernetes Secret. The secret persists across upgrades.
2. **Explicit value** — Set `gypsum.secretKey` in your values to use a specific key.
3. **Existing secret** — Set `gypsum.existingSecret` to reference a Secret you manage externally (it must contain a `secret-key` data key).

### Example: Install with Ingress

```bash
helm install gypsum oci://ghcr.io/mnorrsken/charts/gypsum \
  --set ingress.enabled=true \
  --set ingress.hosts[0].host=wiki.example.com \
  --set ingress.hosts[0].paths[0].path=/ \
  --set ingress.hosts[0].paths[0].pathType=Prefix
```

## Data Directory Structure

```
data/
├── .git/          # auto-initialized git repo
├── pages/         # markdown page files
│   ├── Home.md
│   ├── Scratch_Pad.md
│   └── _favorites.md
└── images/        # uploaded images
    └── my-photo-20260228-a1b2c3d4.png
```

## Development

```bash
make help       # show all targets
make fmt        # format Go source
make tidy       # tidy Go modules
make test       # run unit tests
make build      # build binary to ./bin/gypsum
make run        # run locally on :8080
```

## License

Apache License 2.0 — see [LICENSE](LICENSE).
