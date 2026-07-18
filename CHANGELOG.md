# Changelog

All notable changes to Gypsum are documented in this file.

## v0.49.0

### Added
- **Git sync status indicator** — when a git remote is configured, the top bar now shows a colored status dot next to the 🔒 icon: **green** when the last fetch/push succeeded, **amber (pulsing)** while a sync is in flight, and **red** when a fetch or push is failing. On failure a red warning banner also drops under the top bar with the error message, so unreachable-remote / bad-credential problems (e.g. `git fetch failed`) are visible in the UI instead of only the logs. The indicator is hidden when no remote is configured. Backed by a new `GET /git-status` JSON endpoint the page polls every 15s; error messages are credential-sanitized. See [Usage → Git Sync Status](docs/usage.md).

## v0.48.0

### Added
- **Quick Notes board** — a new whiteboard-style `/notes` view of short, sticky-note jottings (a middle ground between a wiki page and a to-do). Every note is always editable: open the page and start typing. Notes flow into a responsive card grid, and each card's color is derived by hashing its title so a note keeps a stable color. Linked from the sidebar as **Quick Notes**. See [Quick Notes](docs/notes.md).
- **Autosave with git history** — note edits save automatically ~2s after you stop typing (and immediately on blur / tab switch), each committed to git as `wiki: update note <id>`. Notes are stored as plain markdown under `notes/` (active) and `notes/archive/` (archived), so they stay readable and diffable in the repo. The first line of a note is its title; the id is a creation timestamp, so autosave never renames a file.
- **Archive lifecycle** — notes can be archived (moved off the board but kept in git and searchable) and restored, in addition to permanent delete.
- **`notes` MCP section (6 tools)** — `list_notes` (with optional full-text `query` and `include_archived`), `get_note`, `create_note`, `edit_note` (same edit modes as `edit_page`), `archive_note` (with `restore`), and `delete_note`. Notes are full-text indexed via FTS5 alongside pages and skills.

### Notes
- The MCP tool surface grows from 19 to 25 tools with the new `notes` section. Existing tools are unchanged. The `notes` section is enabled by default; add or omit it via `GYPSUM_MCP_SECTIONS` (default is now `read,edit,delete,skills,notes`).

## v0.47.1

### Changed
- **Documentation synced with the v0.47.0 MCP surface** — the built-in `docs/mcp.md` tool reference now lists the actual 19 tools (grouped by section, with the new parameters), drops the five removed tools, and adds `suggest_page_location`. `README.md` gets the same fix.
- **Docs-maintenance rule** — `CLAUDE.md` now requires updating the built-in `docs/` whenever the API/tool surface changes or for any change that isn't a straight patch.

## v0.47.0

### Added
- **`suggest_page_location` MCP tool** — given a working title (and optional keywords), returns a ranked list of existing pages that would make good parents to link a new page from, combining full-text relevance with link-graph position (hub/index pages are favored). Each suggestion includes the outgoing/backlink counts, the headings that already contain links (good insertion points), and a few sample links — so an agent can decide where a page belongs without listing and reading candidate pages.
- **`create_page` gains `link_from` (+ `link_section`)** — atomically add a `[[link]]` to the new page from a parent page (optionally under a named heading), so the "every page must be linked from a parent" convention is enforced in one call instead of a follow-up `edit_page`. A missing parent is reported as a note rather than failing the create.
- **`get_page` gains `include_links`** — append the page's outgoing links and backlinks after the content, so you can understand where a page sits in the wiki in a single read.
- **`link_graph` scoped modes** — `format: tree` renders an indented outline from favorites/Home; `slug` (+ `depth`) returns just the neighborhood subgraph around a page; `orphans_only` lists pages with no backlinks. Prefer these over fetching the full map on large wikis.

### Changed
- **`list_pages` absorbs `get_recent_pages` and `get_favorites`** — pass `sort: recent` (results include a `modified` timestamp), `favorites_only: true`, or `limit`. The two standalone tools are removed.
- **`page_links` absorbs `what_links_here`** — one tool with `direction: out | in | both` (default `both`) returns outgoing links and/or backlinks. `what_links_here` is removed.
- **MediaWiki import folded into `create_page`/`edit_page`** — pass `format: mediawiki` with wikitext in `content`. The standalone `create_page_from_mediawiki` and `edit_page_from_mediawiki` tools are removed.
- **`search_pages` is leaner and more informative** — results are de-duplicated across multiple queries, capped by a new `limit` (default 10), and annotated with each page's outgoing/backlink counts so hub pages stand out. `search_skills` also accepts `limit`.

### Notes
- The MCP tool surface drops from 23 to 19 tools. This is a breaking change for connectors that call the removed tool names (`get_recent_pages`, `get_favorites`, `what_links_here`, `create_page_from_mediawiki`, `edit_page_from_mediawiki`) — use the consolidated equivalents above. The section-based enable/disable model (read/edit/delete/skills) is unchanged.

## v0.46.0

### Added
- **PBKDF2 key derivation for secure fields** — new `{{secure_aes2:...}}` blocks derive their AES-256-GCM key with PBKDF2-HMAC-SHA256 (600,000 iterations) over the passphrase and a per-deployment salt, replacing the unsalted single-SHA-256 KDF. Encryption and decryption still happen entirely in the browser. Legacy `{{secure_aes:...}}` blocks (including pages from ≤ 0.42.x that used `GYPSUM_SECRET_KEY`) keep decrypting with the same passphrase — no migration required — and editing a page upgrades all of its secure blocks to `secure_aes2`.
- **`GYPSUM_SECURE_SALT`** — base64 PBKDF2 salt served to the browser. If unset, a random salt is generated on first run and persisted in `gypsum.db` so it stays stable across restarts. The salt is not secret; it ensures two deployments derive different keys from the same passphrase.
- **Helm salt generation** — the pre-install hook now also generates a random salt into a `<release>-gypsum-secure` Secret when neither `secureSalt.value` nor `secureSalt.existingSecret` is set, and wires `GYPSUM_SECURE_SALT` into the deployment.

### Changed
- **`gypsum re-encrypt` is salt-aware** — it now decrypts both legacy `secure_aes` (with `-old-key`) and `secure_aes2` (with `-old-key` + `-old-salt`) and re-encrypts everything to `secure_aes2` under `-new-key` and the now-required `-new-salt`. This is the bulk migration path from the old KDF to PBKDF2.

### Notes
- After upgrading, returning users re-enter their passphrase once: the new salt means the cached SHA-256 key cannot derive the PBKDF2 key. Existing encrypted blocks remain readable immediately; the next save or new secret prompts a one-time unlock, then the derived keys are cached again.

## v0.45.3

### Security
- **Public share pages are HTML-sanitized** — `/public/{token}` (and `/docs/`) output now passes through a bluemonday sanitizer, so author-supplied raw HTML/JS in a shared page can no longer execute for anonymous visitors. Authenticated pages still render raw HTML as before. Sized images, syntax-highlighting classes, heading anchors, and task-list checkboxes are preserved.
- **Image-size macro escapes its attributes** — `![alt|500](url)` no longer allows breaking out of the generated `<img>` tag via quotes in the alt text, and `javascript:`/`data:` URLs leave the macro unexpanded.
- **`/docs/{slug}` rejects path separators and `..`** — defense-in-depth guard on top of the ServeMux path cleaning.
- **Baseline security headers** — every response now carries `X-Content-Type-Options: nosniff`, `X-Frame-Options: SAMEORIGIN`, and `Referrer-Policy: strict-origin-when-cross-origin`. HSTS and CSP remain the reverse proxy's responsibility.

## v0.45.2

### Fixed
- **Stale git lock recovery** — if a git process was killed mid-write (timeout, OOM, SIGTERM) and left `.git/index.lock` behind, every subsequent auto-commit would fail. The committer now detects the lock-contention error, removes the stale lock, and retries the command once. Safe because Gypsum is the sole git user of the data repo and index-mutating operations are serialised internally.

## v0.45.1

### Changed
- **Smaller binaries** — the Makefile and Dockerfile now build with `-trimpath -ldflags="-s -w"`, stripping DWARF debug info and the symbol table. The `gypsum` binary drops from ~24 MB to ~17 MB (-29 %). Panic stack traces are unaffected (Go's `.gopclntab` still resolves function names); only `dlv`/`gdb` source-level debugging needs an unstripped build.

## v0.45.0

### Changed
- **`rekey` renamed to `re-encrypt`** — the passphrase-rotation CLI is now `gypsum re-encrypt -dir <pages-dir> -old-key <old> -new-key <new> [-dry-run]`. The function name (`RunReencrypt`), file (`internal/wiki/reencrypt.go`), and flag-set name follow the same rename. Behaviour and flags are unchanged.

### Removed
- **Standalone `rekey` binary and Docker symlink** — `cmd/rekey/` is gone, the Makefile no longer builds `bin/rekey[.exe]`, and the Docker image no longer ships the `rekey → gypsum` symlink. Use `gypsum re-encrypt ...` instead; in containers, `docker exec <ctr> gypsum re-encrypt ...`.

## v0.44.0

### Changed
- **Web assets embedded in the binary** — HTML templates and static JS/CSS (`web/templates/`, `web/static/`) are now compiled into the `gypsum` binary via `//go:embed`. The server no longer reads them from disk at runtime, so the `web/` directory is not needed alongside the binary. The Docker image drops `COPY web /app/web`; deployments that bind-mount or override `web/` will need to be adjusted.

## v0.43.2

### Fixed
- **Editor save with secure blocks** — saving a page with `{{secure:...}}` blocks could land on the diff-preview page instead of saving when the "Show diff" preference was set, because the post-encryption resubmit re-fired listener chains in unintended order. The encryption path now calls `form.submit()` directly (bypassing listeners) and replicates the showdiff hidden-input logic inline, so a save click always saves whether or not the diff toggle is on.

## v0.43.1

### Changed
- **`rekey` is now a subcommand of the gypsum binary** — the rekey CLI logic moved into `internal/wiki/rekey.go` and the gypsum binary dispatches when invoked as `rekey` (via symlink) or as `gypsum rekey ...`. The Docker image now ships a single binary plus a `rekey → gypsum` symlink, dropping ~16 MB. `go run ./cmd/rekey` still works locally; existing usage (`rekey -dir ... -old-key ... -new-key ...`) is unchanged.
- **Binaries live in `/usr/local/bin/`** — both `gypsum` and `rekey` are now on the default `$PATH` inside the container. `docker exec <ctr> rekey ...` works without an absolute path.

## v0.43.0

### Changed
- **Client-side encryption for secure fields** — `{{secure:...}}` blocks are now encrypted and decrypted entirely in the browser using WebCrypto AES-256-GCM. The server no longer holds the encryption passphrase, never sees plaintext, and stores only ciphertext. A new top-bar 🔒 button opens an unlock dialog where the passphrase is entered; tick "Remember on this device" to persist the SHA-256-derived key in `localStorage`. The wire format and key-derivation function are unchanged, so existing pages decrypt with the same passphrase you previously set in `GYPSUM_SECRET_KEY`.
- **Logged template parse errors** — `parseTemplates` now logs html/template parse failures instead of silently dropping the template. A new regression test (`TestRealTemplatesParse`) walks every page template against the real `web/templates/` directory so stray `{{` in script blocks fail at test time rather than at render time.

### Removed
- **`GYPSUM_SECRET_KEY` env var** — no longer read or required. Drop it from your `docker-compose.yaml`, Helm values, and any other deployment manifests after upgrading. The corresponding Helm chart values (`gypsum.secretKey`, `gypsum.existingSecret`), the auto-generated encryption Secret, and the `secret.yaml` template are gone; the `secret-generate-hook` Job now only generates an OAuth password when needed.
- **`/secure-inline/unlock` endpoint** — the server-side decrypt route is gone. Inline reveal and copy actions in the page view now decrypt locally via the unlocked browser key.

### Migration
1. Upgrade to v0.43.0.
2. Open the wiki, click the 🔒 button in the top bar, paste your old `GYPSUM_SECRET_KEY` value, tick "Remember on this device", and Unlock.
3. Drop `GYPSUM_SECRET_KEY` from your env / compose file / Helm values.

The `rekey` CLI tool is unchanged and still available if you need to rotate the passphrase across stored pages.

## v0.42.0

### Added
- **Debounced git push** — bursts of MCP edits now coalesce into a single push after the burst settles, instead of one push per commit. Configurable via `GYPSUM_GIT_PUSH_DELAY` (default `30s`; set to `0` to restore immediate push). Local commits remain per-edit so git history stays granular. On shutdown any pending push is flushed synchronously so no commits are lost.

### Changed
- **Periodic pull folds pending pushes** — when the periodic pull ticker fires while a debounced push is queued, the two are combined into a single locked pull+push cycle so they never duplicate git work back-to-back.

## v0.41.1

### Fixed
- **Defunct git processes when remote is unreachable** — git subprocesses (e.g. `ssh`, `git-remote-https`) that outlive their parent `git fetch`/`git push` process kept the stdout/stderr pipe open, preventing Go from calling `Wait()` and leaving the parent as a zombie. Fixed by adding a 2-minute timeout to all git operations and setting `WaitDelay` so Go force-closes the pipe and reaps the process if a grandchild lingers after the git process exits.
- **Sync goroutine accumulation under write bursts** — each page save spawned a new goroutine that queued up waiting for the git mutex. Many pending saves produced a backlog of goroutines, each eventually doing a redundant pull+push cycle. Replaced with a single long-lived sync worker goroutine and a buffered channel so concurrent saves coalesce into at most one queued sync.

## v0.41.0

### Added
- **Interactive task-list checkboxes** — clicking a checkbox on any wiki page toggles it immediately, writing the change back to the page source without a full page edit.

### Fixed
- **Backslash escape inside code spans** — `\[[Page]]`, `\{{secure_aes:…}}`, and `\{{secure:…}}` inside backtick code spans and fenced blocks now render without the leading backslash, consistent with the escape behaviour outside code.
- **`\{{secure:…}}` escape was not preserved across saves** — the backslash was previously stripped from storage on first save, causing the content to be encrypted on the next save. It is now kept in storage so re-saving the page is idempotent.

## v0.40.0

### Added
- **Backslash escaping** — prefix any Gypsum-specific syntax with `\` to render it literally: `\[[Page]]` displays as `[[Page]]` without creating a wiki link, `\{{secure:…}}` is stored as plain text without encrypting, and `\{{secure_aes:…}}` is displayed as-is without expanding the secure widget.
- **Seed skill** — fresh installs now include an example skill ("Writing Good Wiki Pages") loaded into `skills/` automatically, demonstrating the skill format and available features.

### Fixed
- **Code spans and fenced blocks suppress Gypsum processing** — wiki links and secure macros inside backtick code spans (`` `[[link]]` ``) and fenced code blocks are no longer resolved or expanded; they render as literal code as CommonMark requires.

## v0.39.0

### Added
- **Multi-level section support** — `get_page`, `get_skill`, `edit_page`, and `edit_skill` now recognise `##`, `###`, and deeper headings as section boundaries, not just `# `. Heading level is preserved on write. `sections_only` returns headings with their `#` prefix so the caller can see the structure.

### Fixed
- **Ambiguous section names** — if a section heading appears more than once in a page (e.g. two `## Budget` sections under different `#` headings), section-edit and section-get now return a clear error instead of silently affecting all matching sections. Use search-and-replace mode (`old_text`/`new_text`) to target a specific occurrence.

## v0.38.2

### Fixed
- **MCP tool annotations** — tools now include `readOnlyHint`/`destructiveHint` annotations so the Claude connector UI categorizes them correctly instead of showing everything under "Other Tools".

## v0.38.1

### Fixed
- **Header auth bypass for `/mcp`** — the header auth middleware now correctly excludes `/mcp` and all `/mcp/*` subpaths from enforcement (previously only `/mcp/external` was excluded). Updated Authelia example config accordingly.

## v0.38.0

### Added
- **Token-efficient MCP editing** — `edit_page` and `edit_skill` now support search-and-replace (`old_text`/`new_text`), section editing (`section`), and append mode (`append`) in addition to full page replacement. Small edits no longer require sending the entire page content.
- **Section-scoped reads** — `get_page` and `get_skill` accept an optional `section` parameter to return only a named `# heading` section, or `sections_only: true` to list section headings without content.

## v0.37.1

### Changed
- **MCP tool descriptions** — `get_page`, `get_skill`, `edit_page`, and `edit_skill` now explicitly instruct models to use `search_pages`/`search_skills` first when the exact slug is not known, instead of guessing.

## v0.37.0

### Added
- **`mcp-proxy auth` subcommand** — performs the PKCE OAuth flow non-interactively (no browser) and prints the access token to stdout. Use with `GYPSUM_TOKEN` in Claude Desktop's MCP JSON config or shell scripts: `mcp-proxy auth https://wiki.example.com --password=pass`.

### Changed
- **MCP requires OAuth** — `/mcp` and `/mcp/external` now both require OAuth authentication when OAuth is configured. If OAuth is not configured, the MCP endpoints are not registered at all.
- **Secure field pass-through** — `{{secure_aes:...}}` ciphertext is no longer redacted by the MCP layer. AI assistants receive the raw ciphertext and can round-trip pages with encrypted fields without corrupting them.
- **`/mcp/external` is an alias** — identical behaviour to `/mcp`; kept for backwards compatibility only.

## v0.36.5

### Changed
- **Internal refactoring** — split large source files into focused modules: `handlers.go` (1334 lines) split into six handler files, `mcp.go` (1181 lines) split into protocol/tools/implementations, and `store.go` search logic extracted to `store_search.go`. No behaviour changes.

## v0.36.4

### Changed
- **`search_skills` MCP description** — updated to document that full skill content is returned when exactly one match is found.

## v0.36.3

### Changed
- **`search_skills`** — when only one skill matches, returns the full skill content instead of a snippet summary.

## v0.36.2

### Fixed
- **New skill slug** — creating a new skill now derives the slug from the H1 heading in the content instead of always using "New_Skill".
- **New page slug** — creating a new page now derives the slug from the H1 heading in the content instead of keeping the placeholder slug.
- **New page dialog removed** — the title dialog before editing a new page is skipped; go directly to the editor and set the title via the H1 heading.

## v0.36.1

### Added
- **Skills documentation page** — dedicated `docs/skills.md` covering skill structure, MCP tools, automatic lookup with Claude Code, and tips.

### Changed
- **Sidebar** — skills section now shows only "All Skills" and "+ New Skill" instead of listing every individual skill.
- **MCP docs** — moved skills documentation out of `docs/mcp.md` into the new dedicated page to avoid duplication.

## v0.36.0

### Added
- **htmx + Alpine.js frontend** — vendored htmx 2.0.4 and Alpine.js 3.14.9 for declarative server interactions and client-side reactivity, replacing hand-rolled vanilla JS.
- **Live search** — search input now fetches results as you type (300ms debounce) via htmx partial responses.
- **Inline image delete** — deleting an image on the images page removes the table row without a full page reload.
- **`go vet` Makefile target** — `make vet` runs Go static analysis.
- **`make vendor-js`** — downloads pinned versions of htmx and Alpine.js into `web/static/`.

### Changed
- **Sidebar and theme toggle** — migrated from vanilla JS event listeners to Alpine.js directives.
- **Image picker modal** — open/close state managed by Alpine.js, image grid loaded via htmx HTML fragment instead of JSON fetch + DOM construction.
- **Encrypted field reveal** — click-to-reveal on secure fields now uses htmx (`hx-post`/`hx-swap`) instead of fetch + manual DOM manipulation; auto-relocks after 60 seconds.
- **Delete confirmations** — page, skill, and image delete buttons use `hx-confirm` and `hx-post` instead of hidden forms with `confirm()` + JS `submit()`.

## v0.35.0

### Added
- **Prometheus metrics server** — a dedicated metrics server (default `:9090`, configurable via `GYPSUM_METRICS_PORT`) exposes per-tool MCP usage metrics in Prometheus exposition format at `GET /metrics`: call counts, error counts, sent characters, and received characters.
- **Helm metrics support** — new `metrics` section in `values.yaml` (disabled by default) to expose the metrics port and optionally create a Prometheus `ServiceMonitor` for automatic scrape discovery.

## v0.34.1

### Removed
- **MCP duplicate-check on create** — removed the optional `query` parameter from `create_page` and `create_skill`. Multi-query search on `search_pages`/`search_skills` is unchanged.

## v0.34.0

### Added
- **MCP multi-query search** — `search_pages` and `search_skills` now accept an array of queries, returning results for multiple topics in a single call with per-query section headers.
- **MCP duplicate-check on create** — `create_page` and `create_skill` now accept an optional `query` parameter. If the search finds existing pages/skills, results are returned instead of creating — call again without `query` to force creation.

### Changed
- **MCP tool descriptions** — `list_pages` and `list_skills` now recommend using `search_pages`/`search_skills` instead of listing all items to find a specific one. `create_page` and `create_skill` guide AI assistants to use the `query` parameter before creating.

## v0.33.1

### Fixed
- **Skills directory auto-created** — `Save` now calls `MkdirAll` before writing, so the `skills/` folder is always created on first use even on pre-existing Docker volumes that predate the skills feature.
- **MCP tool descriptions** — clarified when to use page tools vs skill tools: `create_page`/`edit_page` now mention "document on my wiki" / "add a note to my wiki" as triggers; `create_skill`/`edit_skill`/`delete_skill` now say to only use them when the user explicitly says "skill".

## v0.33.0

### Added
- **MCP tool sections** — MCP tools are now categorized into `read`, `edit`, `delete`, and `skills` sections. Set `GYPSUM_MCP_SECTIONS` to a comma-separated list (e.g. `read,skills`) to control which tools are exposed to AI assistants. Defaults to all sections enabled.

## v0.32.0

### Added
- **Skills system** — new skill pages for storing procedural knowledge (build instructions, testing conventions, deployment steps). Skills live in a separate `skills/` directory, have tag-based discoverability, and are optimized for AI retrieval via MCP.
- **6 new MCP tools** — `list_skills`, `get_skill`, `create_skill`, `edit_skill`, `delete_skill`, `search_skills` with FTS5 tag-boosted search (tags weighted 15x over content).
- **Skills web UI** — skills section in sidebar, dedicated list/view/edit pages, tag display, and inclusion in the All Pages view.
- **Skills documentation** — added skills guide to the MCP docs page, including recommended structure and Claude Code auto-lookup instructions.

### Changed
- **Unified document storage** — pages and skills now share a single set of `Load`/`Save`/`Delete`/`List`/`Search` methods parameterized by `DocKind`, replacing duplicate implementations. Git commit/history methods similarly unified.

## v0.31.0

### Added
- **FTS5 full-text search** — search is now powered by a SQLite FTS5 index with BM25 ranking, replacing the previous filesystem scan. Results include highlighted context snippets showing where terms matched. The index is built on startup and updated incrementally on page save/delete.
- **Incremental reindex after git pull** — periodic pulls diff changed files and reindex only affected pages instead of rescanning the entire wiki.
- **MCP search context** — the `search_pages` MCP tool now returns context snippets around matched terms, giving AI assistants better visibility into where keywords appear on a page.

## v0.30.2

### Fixed
- **Docker build** — added `docs/` to `.dockerignore` allowlist so the built-in documentation section is included in container images.

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
