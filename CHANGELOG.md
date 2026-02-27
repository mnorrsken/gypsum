# Changelog

All notable changes to Gypsum are documented in this file.

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
