# Usage

## Pages

All pages live in `data/repo/pages/` as `.md` files. Click **+ New Page** in the sidebar to create a page.

Use `[[Page Title]]` to link between pages. The title is converted to a slug (`Page_Title`) automatically. Clicking a link to a non-existent page opens the editor pre-populated with a heading.

If a page starts with a level-1 heading (`# My Title`), that heading is used as the browser and page title instead of the slug-derived name.

Each page has **Page**, **Edit**, **Share**, and **History** tabs.

## Secure Fields

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

On save, the content is encrypted with AES-256-GCM in your browser before it
reaches the server, becoming:

```
WiFi password: {{secure_aes2:BASE64_CIPHERTEXT}}
```

The key is derived from your passphrase with PBKDF2-HMAC-SHA256 and the
per-deployment salt (`GYPSUM_SECURE_SALT`). Older `{{secure_aes:...}}` blocks
(unsalted SHA-256) still decrypt with the same passphrase; editing a page
upgrades its blocks to `secure_aes2`. See
[Configuration → Encryption](configuration.md#encryption) for the salt and bulk
migration.

The passphrase is entered once via the 🔒 icon in the top bar and can be
remembered on the device (the derived keys are stored in `localStorage`). The
server never sees the passphrase, the derived key, or any plaintext.

When viewing the page, encrypted fields appear as `🔒****`. Click to decrypt
in the browser and reveal for 60 seconds, then it auto-hides. A clipboard
copy button appears next to revealed values.

The editor validates custom tags on save — unknown tags, unclosed blocks, and improperly formatted multiline blocks are rejected with clear error messages.

### Escaping

Prefix any macro or wiki link with a backslash to display it literally without processing:

| Input | Rendered as |
|---|---|
| `\{{secure:example}}` | `{{secure:example}}` (not encrypted) |
| `\{{secure_aes:BASE64}}` | `{{secure_aes:BASE64}}` (not expanded) |
| `\{{secure_aes2:BASE64}}` | `{{secure_aes2:BASE64}}` (not expanded) |
| `\[[Page Title]]` | `[[Page Title]]` (not a link) |

Content inside backtick code spans (`` `[[like this]]` ``) and fenced code blocks is always displayed verbatim — macros and wiki links inside them are never processed.

## Images

Three ways to upload images:

- **Paste** — copy an image to the clipboard and paste into the editor
- **Drag-and-drop** — drag an image file onto the editor textarea
- **Image picker** — click **Images** in the editor toolbar to browse and insert existing uploads

A markdown image reference is inserted at the cursor. Filenames are derived from the original file name where available.

### Size Hints

Images automatically scale to fit the content area. Add an optional size hint to the alt text:

| Syntax | Effect |
|---|---|
| `![screenshot\|500](/images/foo.png)` | max-width: 500px |
| `![screenshot\|50%](/images/foo.png)` | max-width: 50% |
| `![screenshot\|800x400](/images/foo.png)` | width: 800px, height: 400px |

### Image Management

Manage images at `/images` (linked in the sidebar). The index shows each image's thumbnail, filename, file size, and which pages reference it. Unused images can be deleted.

Supported formats: PNG, JPG, JPEG, GIF, WEBP, SVG (max 10 MB).

## Page History

Click the **History** tab to view the git commit log for a page. Select two revisions using the **Old** and **New** radio buttons, then click **Compare Selected** to see a colorized unified diff.

## Diff Preview

Tick "Show diff" in the editor before saving to see a colorized unified diff of changes. The diff shows raw on-disk content including encrypted tags. The preference is remembered across sessions. Click **Confirm Save** to commit or **Back to Editor** to continue editing.

## Favorites

Edit the special `_favorites.md` page (linked at the bottom of the Favorites section in the sidebar) to pin pages:

```
[[Home]]
[[Scratch Pad]]
[[My Important Page]]
```

## Search

Use the search bar or visit `/search`. Queries are split into individual terms, each matched independently against page titles and content. Prefix matching is supported — e.g. "lösen" finds "lösenord". Title matches score higher than content matches, and pages matching all terms are boosted.

## Table Editor

Click **Insert Table** (or **Edit Table** when the cursor is inside a table) to open a visual table editor. Supports adding/removing rows and columns and per-column alignment (left/center/right).

## Link Graph

Visit `/graph` for an interactive force-directed graph showing how pages connect via `[[wiki links]]`. Double-click a node to navigate to that page.

## MediaWiki Import

Click "Import MediaWiki" in the editor to paste MediaWiki wikitext and convert it to Markdown. Handles headings, bold/italic, `<syntaxhighlight>`, `<nowiki>`, lists, tables, wiki links, and preformatted lines.

## Public Page Sharing

Share any page via the **Share** tab. Public pages are rendered in a minimal read-only layout without sidebars, editing, or navigation. Wiki links become plain text and encrypted fields are hidden. Links can be revoked or regenerated at any time.

## Printing

Use your browser's print function (Ctrl+P / Cmd+P) to get a clean printout with just the page title and content. Navigation, sidebar, tabs, and encrypted fields are hidden automatically.
