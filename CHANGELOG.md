# Changelog

All notable changes to Gypsum are documented in this file.

## v0.1.1

### Fixed
- **Multiline secure block linebreaks** — multiline `{{plain:\n...\n}}` blocks now strip the leading and trailing linebreaks before encrypting, so the stored content matches what the user typed. `DecryptForEdit` re-wraps multiline content with the surrounding linebreaks for correct round-tripping.
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
- **Content validation** — on save, the editor validates custom tags: unknown `{{tag:...}}` patterns are rejected, unclosed `{{plain:` blocks are caught, and multiline blocks require `{{plain:` and `}}` each on their own line.
- **Multiline secure blocks** — `{{plain:` blocks can span multiple lines. The opening `{{plain:` and closing `}}` must each be on their own line.
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
- **Inline encrypted fields** — `{{plain:...}}` syntax in the editor is encrypted with AES-256-GCM on save using a server-side key (`GYPSUM_SECRET_KEY`). Encrypted fields are stored inline in `.md` files as `{{secure:BASE64}}`.
- **Modern UI** — clean layout with sidebar navigation, system font stack, rounded corners, and subtle styling.
- **Docker support** — Dockerfile with multi-stage build and entrypoint script supporting git remote configuration.
- **Auto git commits** — page saves are automatically committed to a git repo inside `data/`.
- **Wiki links** — `[[Page Title]]` syntax for inter-page linking.
- **Markdown rendering** — goldmark with GFM extensions and syntax highlighting.
- **Seed pages** — `Home` and `Scratch Pad` pages are created on first run.
