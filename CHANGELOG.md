# Changelog

All notable changes to Gypsum are documented in this file.

## Unreleased

### Added
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
