# Secrets

The Secrets vault is an opinionated view for credentials: a searchable list where
each entry is a title, an encrypted block of text, and — optionally — a
description, a link, and a picture of the site it belongs to. Open **Secrets**
in the sidebar.

It uses exactly the same encryption as inline [`{{secure:...}}`](usage.md) fields:
AES-256-GCM with a PBKDF2-derived key, done entirely in the browser. The server
stores ciphertext and never holds the key or the plaintext.

## Using the vault

- **Filter as you type.** The filter box narrows the list on every keystroke,
  matching against title, description and URL. Multiple words all have to match.
  Filtering happens in the browser — no query is sent to the server. `Esc`
  clears the box.
- **Reveal.** Click the eye (or the masked value itself) to decrypt a secret in
  place. It re-masks itself after 60 seconds, the same hold as a secure field on
  a wiki page. Clicking again hides it immediately, and everything re-masks when
  you lock the vault or switch away from the tab.
- **Copy.** The clipboard button decrypts and copies without ever putting the
  secret on screen.
- **Unlock once.** Revealing, copying or saving asks for your passphrase if the
  browser does not already hold the key. Editing only the metadata of a secret
  does not require unlocking — leave the Secret field blank and the stored
  ciphertext is kept as-is.

## Tiles

Each entry gets a 96px tile, in this order of preference:

1. **An image you name.** Any file in the wiki's image library — put its
   filename in the Image field (upload it first on the [Images](usage.md) page).
2. **The site's own picture.** With a URL and no image, Gypsum fetches the page
   and takes the picture the site nominates for itself: `og:image`, then
   `twitter:image`, then `apple-touch-icon`, then its `<link rel=icon>`, then
   `/favicon.ico`. The first one that downloads as an image is stored in the
   image library as `secret-<id>-<random>.<ext>` and attached to the secret.
   Use **From site** in the edit dialog to re-fetch it later.
3. **A two-letter mnemonic.** Failing both, the tile shows the initials of the
   first two words of the title ("Big Secret thing" → **BS**) on a background
   color derived from a hash of the title, so a given title always looks the
   same.

The image fetch is a metadata request made by the server, not a rendered
screenshot — Gypsum has no headless browser. Nothing is sent to a third-party
service, and because the server makes the request directly it also works for
intranet hosts on a self-hosted deployment. Fetches follow redirects, cap the
download at 5 MB, and give up after 15 seconds.

## Storage

Each secret is a small markdown file in the git repo — headers, a blank line,
then the encrypted body:

```
data/repo/secrets/20260824-231630.md
```

```
title: Big Secret thing
url: https://github.com
image: secret-20260824-231630-e8d9aa8f.png
description: Prod admin credentials

{{secure_aes2:VTVKJhK82GvRfv8IWgQx/EtUH0oKp4TCkwIOiR1lkOamu7K4F4A/8BQbjeMwHu43GbZpXw4h}}
```

The file name is the secret's id — a creation timestamp (`yyyymmdd-hhmmss`, with
a numeric suffix on the rare same-second collision) — so renaming a secret never
renames its file. Files are written `0600`, and every change is committed to
git like any other wiki content. Because only ciphertext is stored, the git
history is safe to push to a remote.

Saving without changing the secret itself reuses the stored ciphertext rather
than re-encrypting, so a metadata edit produces a one-line diff instead of a
whole new block (AES-GCM uses a random nonce, so re-encrypting the same text
always looks different).

The server refuses to store a body that is not an encrypted block, so a
malformed request can never write a plaintext secret to disk. If a file is
hand-edited to hold plaintext, the vault shows it as "not encrypted" and never
serves it as a revealable secret.

## Rotating the passphrase

Secrets use the same key as every other secure field, so they rotate with the
same tool — point it at the secrets directory as well as your pages:

```bash
gypsum re-encrypt -dir data/repo/secrets \
  -old-key <old-passphrase> -new-key <new-passphrase> \
  -new-salt "$GYPSUM_SECURE_SALT" -old-salt <base64>
```

See [Configuration → Migrating secure fields](configuration.md).

## MCP

Secrets are deliberately **not** exposed over the [MCP server](mcp.md). There
are no secrets tools, and no MCP tool returns their contents — the vault is
reachable only from the web UI. The server has no key during normal operation,
so it could only ever hand out ciphertext in any case.

## Sharing

Secrets are not part of page sharing: they are not pages, they have no share
links, and they never appear on public pages.
