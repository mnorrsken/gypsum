# Changelog

All notable changes to Gypsum are documented in this file.

## v0.30.1

### Added
- **Rekey CLI tool** — `rekey` command re-encrypts all `{{secure_aes:...}}` fields when rotating `GYPSUM_SECRET_KEY`. Supports `-dry-run` to preview changes.

## v0.30.0

### Added
- **Built-in documentation section** — markdown files in `docs/` are served read-only at `/docs/` with the same wiki styling. A "Documentation" section appears in the sidebar when docs are present. Secure fields are stripped from rendered output.

## v0.29.0

### Added
- **Print-friendly pages** — browser print renders only the page title and content, hiding the topbar, sidebar, tabs, and encrypted fields.
- **Documentation** — moved detailed docs (usage, authentication, MCP, Docker, Helm, configuration) into a `docs/` folder; README trimmed to a concise overview.

## v0.28.3

### Fixed
- **Checkbox rendering** — task list checkboxes (`- [x]` / `- [ ]`) no longer stretch to full width; the global `input { width: 100% }` rule now excludes checkboxes, and task list items get proper inline styling.

## v0.28.2

### Changed
- **Dependency updates** — bumped Alpine base image from 3.21 to 3.23, goldmark from 1.7.8 to 1.8.1, and GitHub Actions (`actions/checkout` v6, `docker/login-action` v4, `docker/metadata-action` v6, `docker/setup-buildx-action` v4, `azure/setup-helm` v5).

## v0.28.1

### Fixed
- **Build compatibility** — upgrade to Go 1.25 for modernc.org/sqlite compatibility.
- **Public page static assets** — bypass authentication for CSS/JS on public shared pages so they render correctly for anonymous visitors.

## v0.28.0

### Added
- **Public page sharing** — share any page publicly via a secret link. A new **Share** tab on every page lets you enable/disable sharing, copy the link, regenerate it, or revoke access. Public pages are rendered in a minimal read-only layout without sidebars, editing capabilities, or navigation links. Wiki links appear as plain text and encrypted `{{secure_aes:...}}` fields are stripped entirely.
- **SQLite database** — a `gypsum.db` SQLite database in `data/` stores share links and OAuth tokens. Replaces the previous `oauth_tokens.json` file (existing tokens are automatically migrated on first startup).

### Changed
- **OAuth token storage** — tokens are now persisted in SQLite instead of a JSON file. The migration is automatic and transparent.

## v0.27.0

### Changed
- **Data directory restructure** — the git repository now lives in `data/repo/` instead of directly in `data/`. Pages and images are stored under `data/repo/pages/` and `data/repo/images/`, while `oauth_tokens.json` remains in `data/` outside the git working tree. This fixes a bug where `git stash --include-untracked` during remote sync could remove or corrupt the OAuth tokens file, causing authenticated sessions to be lost.

### Fixed
- **OAuth tokens lost after git sync** — `oauth_tokens.json` was inside the git working directory and could be stashed/removed during `pullRebase()` operations. Moving it outside the repo directory prevents git from interfering with token persistence.

### Migration
- The Helm chart includes an init container that automatically migrates existing installations from the flat `data/` layout to the new `data/repo/` layout. No manual steps required.

## v0.26.0

### Added
- **Dedicated health-probe server** — a separate HTTP server on port 9091 (configurable via `GYPSUM_PROBE_PORT`) serves `/healthz` and `/readyz` endpoints without authentication middleware. Kubernetes liveness and readiness probes now work correctly when header-based authentication is enabled.

## v0.25.0

### Added
- **Header-based authentication** — optional reverse proxy auth via `GYPSUM_AUTH_USER_HEADER` (e.g. `Remote-User`). When enabled, all requests except OAuth endpoints require the header to be present. An optional `GYPSUM_AUTH_REQUIRED_GROUP` enforces group membership via a configurable group header. The authenticated username is used as the git commit author.

## v0.24.0

### Changed
- **Removed MCP `upload_image` tool** — MCP's JSON-only transport makes image upload impractical for AI assistants (Claude can't produce base64 data, and URL fetching is unreliable). Use the wiki UI for image uploads instead.

## v0.23.0

### Changed
- **MCP `upload_image` supports URL download** — the tool now accepts a `url` parameter as an alternative to `data` (base64). When given a URL, the server downloads the image directly, which is more reliable for AI assistants like Claude that struggle with large base64 payloads.

## v0.22.1

### Added
- **Helm: `git.existingSecret`** — reference a pre-existing Kubernetes Secret for git credentials instead of putting passwords/tokens in values. Keys in the secret (`password`, `token`) are optional when using `existingSecret`.

## v0.22.0

### Added
- **MCP tool: `upload_image`** — upload images to the wiki via MCP by providing base64-encoded data and a filename. Validates file extension and MIME type, generates unique filenames, and returns the markdown reference for use in pages. Supports PNG, JPG, JPEG, GIF, WEBP, and SVG (max 10 MB).

## v0.21.0

### Added
- **Recent Edits page** — new `/recent-edits` route showing all wiki edits in reverse chronological order, sourced from git history. Displays page name, revision, date, author, and commit message. Paginated at 50 entries per page. Accessible from the sidebar navigation.

## v0.20.0

### Added
- **Graceful shutdown** — the server now handles SIGINT/SIGTERM signals, drains in-flight requests (10 s timeout), and stops background goroutines (periodic git pull) before exiting.
- **Rate limiting** — per-IP token-bucket rate limiting on MCP endpoints (30 req/s) and OAuth endpoints (5 req/s) to prevent brute-force and abuse.
- **Access logging** — every HTTP request is logged with method, path, status code, and duration.
- **Image upload MIME validation** — uploaded files are now verified with `http.DetectContentType` in addition to the file-extension check, preventing non-image files from being stored with image extensions.

### Fixed
- **Empty `handlers_test.go`** — removed the placeholder file.
- **MediaWiki regex recompilation** — all 24 regexes in `ConvertMediaWikiToMarkdown` are now compiled once at package level instead of on every call.
- **Duplicate `secureAesMacro` regex** — `markdown.go` now shares the single regex defined in `secure.go`.
- **Git credential URL encoding** — `injectAuth` now URL-encodes tokens and passwords so special characters (`@`, `/`, etc.) don't break the remote URL.
- **Dead code in `ValidateContent`** — removed unused `unknownTagRe`, `anyDoubleBraceClose` regexes and a no-op code block.

## v0.19.2

### Fixed
- **Markdown table styling** — tables in wiki content now render with borders, left-aligned headers, and proper padding instead of the browser's unstyled default.

## v0.19.1

### Fixed
- **OAuth token persistence** — access tokens are now persisted to `data/oauth_tokens.json` so they survive server restarts. Previously all tokens were in-memory only, causing MCP clients like Claude to lose their connection on restart and fail to reconnect.

## v0.19.0

### Added
- **Relevance-ranked search** — search queries are split into individual terms (stripping punctuation like `&`), each matched independently against page titles and content. Prefix matching works, so "lösen" finds "lösenord" and "kube" finds "kubernetes". Results are scored by relevance: title matches rank higher than content matches, and pages matching all terms get a bonus. Results are sorted by score instead of alphabetically.

### Fixed
- **Git rebase with unstaged changes** — `pullRebase` now stashes uncommitted changes before rebasing and restores them after, preventing the "cannot rebase: You have unstaged changes" error during periodic pulls or sync.

## v0.18.4

### Added
- **Dynamic Client Registration** — added `POST /oauth/register` endpoint (RFC 7591) so MCP clients like Claude can register automatically during the OAuth discovery flow.

### Changed
- **Removed `client_id` validation** — the authorize and token endpoints no longer validate `client_id`. Since all access is gated by the password login and PKCE, the static `GYPSUM_OAUTH_CLIENT_ID` env var and Helm `oauth.clientId` value have been removed.

### Fixed
- **OAuth flow with Claude** — Claude's MCP connector requires Dynamic Client Registration to complete the OAuth handshake. Without `/oauth/register`, the flow failed before reaching the login page.

## v0.18.2

### Added
- **Auto-generated OAuth password** — when `oauth.enabled` is true and no `oauth.password` or `oauth.existingSecret` is set, the pre-install hook generates a random 32-character password automatically (same pattern as the encryption key).

## v0.18.1

### Added
- **Helm chart OAuth support** — added `oauth.*` values for configuring the OAuth-protected `/mcp/external` endpoint via the Helm chart (`oauth.enabled`, `oauth.password`, `oauth.externalUrl`, `oauth.clientId`, `oauth.tokenTtl`, `oauth.existingSecret`). OAuth password is stored in a Kubernetes Secret.

### Changed
- **Helm chart appVersion** — updated from `0.1.1` to `0.18.0` to match the current application version; chart version bumped to `0.2.0`.

## v0.18.0

### Added
- **External MCP endpoint with OAuth** — new `/mcp/external` route protected by a built-in OAuth 2.0 Authorization Server (Authorization Code + PKCE, S256). Enables secure internet-facing access from Claude's remote MCP connector without relying solely on a reverse proxy like Authelia. Enabled via `GYPSUM_OAUTH_ENABLED=true` with `GYPSUM_OAUTH_PASSWORD` and `GYPSUM_EXTERNAL_URL`.
- **Secure field redaction on external endpoint** — `{{secure_aes:...}}` encrypted fields are replaced with `[encrypted field]` in all read results (`get_page`, `get_page_revision`, `search_pages`) from `/mcp/external`. Pages that contain encrypted fields cannot be edited via the external endpoint; edits must go through the local wiki UI or the internal `/mcp` endpoint.
- **OAuth discovery documents** — `GET /.well-known/oauth-protected-resource` and `GET /.well-known/oauth-authorization-server` served when OAuth is enabled, following RFC 8414 and the MCP 2025-03-26 spec.

## v0.17.2

### Changed
- **Secure field copy button** — the clipboard copy button now appears permanently next to encrypted fields, allowing copying the plaintext without revealing it visually.

## v0.17.1

### Added
- **Delete page button** — edit page now has a "Delete" button (with confirmation prompt) to delete pages from the web UI.

### Fixed
- **Page deletion not committed to git** — deleting a page (via MCP or web UI) now properly commits the removal to git, keeping the repository in sync.

## v0.17.0

### Added
- **MediaWiki import** — paste MediaWiki wikitext into the editor via the "Import MediaWiki" button; converts headings, bold/italic, `<syntaxhighlight>`/`<source>`/`<pre>`/`<nowiki>`/`<code>` tags, lists, tables, wiki links, external links, and space-prefixed preformatted lines to Markdown automatically.
- **MCP tool: `create_page_from_mediawiki`** — create a wiki page from MediaWiki wikitext via MCP (for bulk import workflows).
- **MCP tool: `edit_page_from_mediawiki`** — update an existing page from MediaWiki wikitext via MCP.

## v0.16.1

### Fixed
- **Link graph empty page** — fixed null JSON arrays for pages without links, added loading indicator, CDN error fallback, and empty-state message.

## v0.16.0

### Added
- **Link graph page** — interactive force-directed graph visualization at `/graph` (sidebar link: "Link Graph") showing how all wiki pages connect via `[[wiki links]]`. Uses vis-network. Missing pages shown in red; double-click a node to navigate.
- **MCP tool: `page_links`** — get all outgoing wiki links from a page.
- **MCP tool: `what_links_here`** — find all pages that link to a given page (backlinks). Flags orphaned pages with no parents.
- **MCP tool: `link_graph`** — get the full wiki link graph as a page→links map.

### Changed
- **MCP descriptions** — `create_page` now instructs to always link new pages from a parent page; `edit_page` reminds to ensure linked pages exist.

## v0.15.1

### Fixed
- **MCP proxy double newline** — fixed duplicate trailing newline in proxied responses that caused "Unexpected end of JSON input" errors in Claude Desktop.

## v0.15.0

### Added
- **MCP proxy binary** — standalone `mcp-proxy` executable that bridges stdio to a remote Gypsum `/mcp` endpoint, enabling Claude Desktop integration via the `command` connector. Windows binary built by default.

### Changed
- **Windows build** — `make build` now also produces `gypsum.exe` and `mcp-proxy.exe` for Windows/amd64.
- **Removed embedded stdio MCP** — the `mcp-stdio` subcommand is replaced by the separate `mcp-proxy` binary.

## v0.14.1

### Fixed
- **MCP CORS headers** — added CORS support and OPTIONS preflight handling so Claude's remote MCP connector can connect successfully.

## v0.14.0

### Added
- **MCP server** — built-in MCP (Model Context Protocol) endpoint at `/mcp` using Streamable HTTP transport, allowing AI assistants like Claude to list, read, create, edit, delete, and search pages, manage images, browse favorites, and view page history. Connect from Claude using a remote MCP custom connector pointed at your wiki URL.

## v0.13.0

### Added
- **Revision comparison** — the History tab now has "Old" and "New" radio buttons for selecting two revisions; click **Compare Selected** to view a colorized unified diff between them, using the same diff viewer as the editor preview.

## v0.12.0

### Added
- **H1 title as page title** — if a page's content begins with a level-1 heading (`# My Title`), that heading is used as the browser tab title and the page-title area instead of the slug-derived name. The heading is not rendered a second time in the page body.
- **New pages pre-populated with H1 heading** — when a new page is created (either from a `[[wiki link]]` or the **+ New Page** form), the editor is pre-populated with a `# Proper Title` heading that preserves characters stripped from the URL slug (e.g. `# Tokens & Lösenord` for a link written as `[[ Tokens & Lösenord ]]`).

## v0.11.0

### Added
- **Table editor** — a new **Insert Table** / **Edit Table** button in the editor toolbar opens a visual table editor modal. When the cursor is inside an existing GFM table the button reads "Edit Table" and pre-populates the editor with the current table; otherwise it inserts a new table. Columns support left/center/right alignment, and rows/columns can be added or deleted. Applying serializes the table with aligned columns back into the editor.

## v0.10.0

### Added
- **Image picker** — a new **Images** button in the editor toolbar opens a modal showing all uploaded images as thumbnails; click any image to insert it at the cursor position. The modal also has an "Upload new…" button for uploading directly from the picker.
- **Drag-and-drop image upload** — drag an image file onto the editor textarea to upload and insert it. A dashed-border highlight appears while dragging.
- **Filename-aware image names** — uploaded images are stored with a slug derived from the original filename (e.g. `freddie-mercury-20260228-a1b2c3d4.jpg`); screenshots and generic pastes fall back to a date-only name. The markdown alt text is also derived from the original filename.

## v0.9.0

### Added
- **Dark/light theme** — a toggle button in the top bar switches between dark and light mode; the preference is persisted in `localStorage`.
- **Dark-mode code highlighting** — syntax highlighting now uses the github (light) and github-dark themes via CSS classes, switching automatically with the theme toggle. Previously hardcoded inline styles caused code blocks to always render with a white background.

### Changed
- **Compact topbar** — the search field and theme toggle are grouped together on the right side of the navigation bar, and the bar height has been reduced for a tighter look.

## v0.8.0

### Added
- **Image auto-scaling** — images in wiki pages now automatically scale down to fit the content width (`max-width: 100%`) so large images never overflow the layout.
- **Image size hints** — an optional size specifier can be added to the alt text of any image tag: `![alt|500](/images/foo.png)` (max-width in px), `![alt|50%](/images/foo.png)` (max-width in %), or `![alt|800x400](/images/foo.png)` (explicit width × height). Standard `![alt](url)` syntax continues to work unchanged.

## v0.7.2

### Changed
- **Async git sync** — git pull and push are now performed in a background goroutine after each commit, so page saves and image uploads return instantly without waiting for network I/O.

## v0.7.1

### Fixed
- **Cancel button sizing** — the Cancel link in the editor now renders at the same size as the Save and Help buttons, fixing a mismatch caused by anchors not inheriting button font and line-height styles.

## v0.7.0

### Changed
- **Diff shows on-disk content** — the diff preview now compares the raw on-disk files (with `{{secure_aes:...}}` tags) instead of decrypted plaintext, so encrypted fields are never exposed in the diff.
- **Stable ciphertext for unchanged blocks** — secure blocks whose plaintext hasn't changed keep their original ciphertext in the diff, preventing spurious noise from AES-GCM's random nonces.
- **Diff checkbox remembers preference** — the "Show diff" checkbox state is persisted in `localStorage` so it survives page navigations and browser restarts.
- **Editor button layout** — the diff checkbox now renders inline with the action buttons on a single line, and the label is shortened to "Show diff".

## v0.6.0

### Added
- **Diff preview** — a "Show diff before saving" checkbox in the editor lets you review a colorized unified diff of your changes before committing. Added lines are highlighted green and removed lines red, with surrounding context. Click "Confirm Save" to proceed or "Back to Editor" to keep editing.
- **Git remote sync in Go** — remote configuration, periodic pull, and push-after-commit are now handled entirely by the Go `GitAutoCommitter`, replacing the shell-based setup in `docker-entrypoint.sh`. Features include:
  - Automatic remote configuration on startup
  - Rebase-based pull before every commit
  - Background push after every commit
  - Periodic pull on a configurable interval (`GYPSUM_GIT_PULL_INTERVAL`, default 5 m)
  - "Ours wins" conflict resolution — if a rebase fails, the local state is force-pushed
  - Mutex-serialised git operations to prevent concurrent conflicts
  - Configurable commit author via `GYPSUM_GIT_COMMIT_NAME` / `GYPSUM_GIT_COMMIT_EMAIL`
  - Credential-sanitised URL logging

### Changed
- **Simplified `docker-entrypoint.sh`** — the entrypoint now only handles `git init` and `safe.directory` configuration. All remote setup, pulling, and pushing logic has moved to Go.

## v0.5.0

### Changed
- **Image URL path** — image URLs changed from `/uploads/filename.png` to `/images/filename.png`, matching the storage directory and management page. Pasted images now produce cleaner markdown tags.
- **Multi-arch Docker build** — the builder stage now uses `--platform=$BUILDPLATFORM` with Go cross-compilation (`TARGETOS`/`TARGETARCH`), avoiding slow QEMU emulation during builds.

## v0.4.0

### Changed
- **Tag rename** — the editor tag for sensitive fields is now `{{secure:...}}` (was `{{plain:...}}`). The on-disk encrypted tag is now `{{secure_aes:...}}` (was `{{secure:...}}`). This makes the naming more intuitive: what you mark as "secure" in the editor is stored with the algorithm-specific `secure_aes` tag.

### Fixed
- **CRLF line endings** — multiline `{{secure:...}}` blocks no longer accumulate extra blank lines on each save. Browser-submitted `\r\n` line endings are now normalised to `\n` before processing.

## v0.3.0

### Fixed
- **Git safe.directory in Kubernetes** — git operations no longer fail with "not in a git directory" when the data volume is owned by a different user (e.g. PVC mounts). Both the Go auto-committer and the Docker entrypoint now configure `safe.directory` before any git operations.
- **Dockerfile user UID** — the `app` user is now created with explicit UID/GID 1000 to match the Helm chart's `securityContext`.
- **Legacy secure directory removed** — the unused `data/secure` directory is no longer created by the Dockerfile or entrypoint.

## v0.2.0

### Added
- **Helm chart** — full Kubernetes Helm chart in `charts/gypsum/` with Deployment, Service, PVC, Ingress, and ServiceAccount templates.
- **Auto-generated encryption secret** — a pre-install/pre-upgrade Helm hook generates a random `GYPSUM_SECRET_KEY` and stores it in a Kubernetes Secret when no key or existing secret is provided.
- **Helm chart CI** — GitHub Actions workflow (`.github/workflows/helm.yml`) that lints the chart on PRs and packages/pushes it to the GHCR OCI registry on version tags.

### Fixed
- **Favorites git commit** — `_favorites.md` is now force-added during git commits (`git add -f`) so it is tracked even when matched by `.gitignore` patterns.

## v0.1.1

### Fixed
- **Multiline secure block linebreaks** — multiline `{{secure:\n...\n}}` blocks now strip the leading and trailing linebreaks before encrypting, so the stored content matches what the user typed. `DecryptForEdit` re-wraps multiline content with the surrounding linebreaks for correct round-tripping.
- **Unlocked secure field styling** — revealed secure fields now render as `inline-block` with padding so the grey background forms a continuous area instead of breaking across lines.

## v0.1.0

### Added
- **Responsive layout** — mobile-friendly design with media queries at 768px and 480px breakpoints. On small screens the sidebar becomes a slide-out drawer toggled by a hamburger menu button, with a semi-transparent overlay.
- **Editor help panel** — an expandable reference panel to the right of the editor with a quick-reference table for markdown syntax, wiki links, secure field syntax (single-line and multiline), and image usage.
- **Page history** — `/history/{slug}` shows the git commit log for a page (hash, date, author, message). Accessible via the "History" tab on every page.
- **Page/Edit/History tabs** — every page now has MediaWiki-style tabs below the title for quick navigation between view, edit, and history.
- **MediaWiki-style topbar** — brand and search bar with red and blue horizontal rulers underneath, inspired by MediaWiki.
- **Blue title ruler** — a blue horizontal rule appears under every page title.
- **New Page flow** — the "+ New Page" sidebar link now opens a title input form. The title must be unique; duplicate names are rejected with an error message.
- **Content validation** — on save, the editor validates custom tags: unknown `{{tag:...}}` patterns are rejected, unclosed `{{secure:` blocks are caught, and multiline blocks require `{{secure:` and `}}` each on their own line.
- **Multiline secure blocks** — `{{secure:` blocks can span multiple lines. The opening `{{secure:` and closing `}}` must each be on their own line.
- **Multiline decrypted display** — decrypted secure fields with line breaks now render with `<br>` tags instead of collapsing to a single line.
- **Image support** — paste images directly into the editor to upload. Images are stored in `data/images/` and referenced as standard markdown images. An image index page (`/images`) lists all uploaded images with thumbnails, file sizes, page usage, and a delete button for cleanup.
- **Image git integration** — image uploads and deletions are auto-committed to the data repo.
- **Secure field auto-hide** — decrypted secure fields automatically revert to `🔒****` after 60 seconds.
- **Secure field copy button** — a clipboard copy button appears next to revealed secure values.
- **Secure field monospace rendering** — secure fields always render in a monospace font for readability.
- **All Pages index** — `/pages` route lists every wiki page in a two-column layout.
- **Favorites sidebar** — pin pages by editing `_favorites.md`; favorites appear in the sidebar.
- **Recently Edited sidebar** — the 5 most recently modified pages are shown in the sidebar.
- **Full-text search** — search bar in the top navigation searches page titles and content.
- **Unicode slug support** — wiki links like `[[Lösenord]]` correctly preserve non-ASCII characters in slugs.
- **Inline encrypted fields** — `{{secure:...}}` syntax in the editor is encrypted with AES-256-GCM on save using a server-side key (`GYPSUM_SECRET_KEY`). Encrypted fields are stored inline in `.md` files as `{{secure_aes:BASE64}}`.
- **Modern UI** — clean layout with sidebar navigation, system font stack, rounded corners, and subtle styling.
- **Docker support** — Dockerfile with multi-stage build and entrypoint script supporting git remote configuration.
- **Auto git commits** — page saves are automatically committed to a git repo inside `data/`.
- **Wiki links** — `[[Page Title]]` syntax for inter-page linking.
- **Markdown rendering** — goldmark with GFM extensions and syntax highlighting.
- **Seed pages** — `Home` and `Scratch Pad` pages are created on first run.
