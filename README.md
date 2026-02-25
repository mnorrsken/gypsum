# Gypsum

A lightweight, self-hosted personal wiki built with Go. Pages are stored as plain Markdown files in a `data/` directory with automatic git versioning, wiki-style `[[links]]`, inline encrypted fields, image uploads, and a clean modern UI.

## Features

- **Markdown pages** — stored as `.md` files, rendered with [goldmark](https://github.com/yuin/goldmark) (GFM tables, syntax highlighting)
- **Wiki links** — `[[Page Title]]` creates inter-page links; clicking a link to a non-existent page opens the editor
- **Inline encryption** — `{{plain:secret}}` in the editor is AES-256-GCM encrypted on save; click the lock icon on a page to temporarily reveal the value (auto-hides after 60 seconds). Supports multiline blocks.
- **Content validation** — malformed or unknown custom tags are rejected on save with clear error messages
- **Page history** — view the git commit history for any page via the History tab
- **Image uploads** — paste images directly into the editor; managed via an image index page
- **Favorites sidebar** — edit `_favorites.md` to pin pages in the sidebar
- **Full-text search** — searches page titles and content
- **Auto git commits** — every page save and image upload is committed to a git repo inside `data/`
- **Docker-ready** — single-container deployment with optional git remote sync
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

## Usage

### Pages

- All pages live in `data/pages/` as `.md` files.
- Click **+ New Page** in the sidebar to create a page. You'll be prompted for a unique title.
- Use `[[Page Title]]` to link between pages. The title is converted to a slug (`Page_Title`) automatically.
- Each page has **Page**, **Edit**, and **History** tabs for quick navigation.

### Secure Fields

Store sensitive values inline in your markdown:

```
WiFi password: {{plain:my-secret-password}}
```

For multiline secrets, place `{{plain:` and `}}` on their own lines:

```
{{plain:
username: admin
password: s3cret
}}
```

On save, the `{{plain:...}}` block is encrypted with AES-256-GCM:

```
WiFi password: {{secure:BASE64_CIPHERTEXT}}
```

When viewing the page, encrypted fields appear as `🔒****`. Click to decrypt and reveal the value for 60 seconds, then it auto-hides. A clipboard copy button appears next to revealed values. Multiline content renders with proper line breaks.

The editor validates custom tags on save — unknown tags, unclosed blocks, and improperly formatted multiline blocks are rejected with clear error messages.

### Page History

Click the **History** tab on any page to view its git commit log, showing revision hashes, dates, authors, and commit messages.

### Images

Paste an image from the clipboard directly into the editor textarea. The image is uploaded and a markdown image reference (`![image](/uploads/filename.ext)`) is inserted at the cursor.

Manage images at `/images` (linked in the sidebar as **Images**). The index shows each image's thumbnail, filename, file size, and which pages reference it. Unused images can be deleted to free space.

Supported formats: PNG, JPG, JPEG, GIF, WEBP, SVG (max 10 MB).

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

### Docker Compose Example

```yaml
services:
  wiki:
    build: .
    ports:
      - "8080:8080"
    volumes:
      - wiki-data:/app/data
    environment:
      GYPSUM_SECRET_KEY: "your-strong-secret-key"
      GYPSUM_GIT_INIT: "true"
      GYPSUM_GIT_COMMIT_NAME: "Gypsum"
      GYPSUM_GIT_COMMIT_EMAIL: "gypsum@local"

volumes:
  wiki-data:
```

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
    └── 20260223-142301-a1b2c3d4.png
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
