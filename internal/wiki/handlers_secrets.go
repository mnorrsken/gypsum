package wiki

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// secretImageFetchTimeout bounds the whole site-image fetch (page + image).
const secretImageFetchTimeout = 15 * time.Second

// handleSecretsVault renders the secrets vault. Every entry is sent to the
// browser as ciphertext; decryption happens client-side on reveal or copy.
func (h *Handler) handleSecretsVault(w http.ResponseWriter, r *http.Request) {
	secrets, err := h.store.ListSecrets()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// A vault listing must never be cached by a shared proxy or restored from
	// the bfcache after logout.
	w.Header().Set("Cache-Control", "no-store, private")
	h.render(w, "secrets", TemplateData{
		Title:      "Secrets",
		Secrets:    secrets,
		SecretHold: SecretRevealSeconds,
		SecretHues: SecretColorCount,
	})
}

// secretFormEntry reads the shared secret fields from a submitted form.
func secretFormEntry(r *http.Request) SecretEntry {
	return SecretEntry{
		Title:       strings.TrimSpace(r.FormValue("title")),
		Description: strings.TrimSpace(r.FormValue("description")),
		URL:         strings.TrimSpace(r.FormValue("url")),
		Image:       strings.TrimSpace(r.FormValue("image")),
	}
}

// secretJSON is the payload returned after a create or save so the board can
// restyle the card without a reload.
func secretJSON(id string, entry SecretEntry) map[string]any {
	return map[string]any{
		"id":       id,
		"title":    entry.Title,
		"mnemonic": SecretMnemonic(entry.Title),
		"color":    SecretColor(entry.Title),
		"image":    entry.Image,
	}
}

// handleSecretCreate stores a new secret. The body must already be encrypted:
// the browser holds the key, so a request carrying plaintext is a bug and is
// rejected rather than written to disk.
func (h *Handler) handleSecretCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	entry := secretFormEntry(r)
	if entry.Title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	body := strings.TrimSpace(r.FormValue("secret"))
	if !ValidSecretBody(body) {
		http.Error(w, "secret must be an encrypted block", http.StatusBadRequest)
		return
	}
	if entry.Image != "" && !validSecretImageName(entry.Image) {
		http.Error(w, "invalid image name", http.StatusBadRequest)
		return
	}
	id, err := h.store.CreateSecret(entry, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.autoCommit.CommitSecretSave(id, UsernameFromRequest(r))
	h.writeJSON(w, http.StatusCreated, secretJSON(id, entry))
}

// handleSecretSave overwrites an existing secret. An empty "secret" field
// keeps the stored ciphertext, so metadata can be edited while locked.
func (h *Handler) handleSecretSave(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !ValidSecretID(id) {
		http.Error(w, "invalid secret id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	existing, err := h.store.LoadSecret(id)
	if err != nil {
		http.Error(w, "secret not found", http.StatusNotFound)
		return
	}
	entry := secretFormEntry(r)
	if entry.Title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	if entry.Image != "" && !validSecretImageName(entry.Image) {
		http.Error(w, "invalid image name", http.StatusBadRequest)
		return
	}
	body := strings.TrimSpace(r.FormValue("secret"))
	if body == "" {
		body = existing.SecureMacro()
	}
	if !ValidSecretBody(body) {
		http.Error(w, "secret must be an encrypted block", http.StatusBadRequest)
		return
	}
	if err := h.store.SaveSecret(id, entry, body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.autoCommit.CommitSecretSave(id, UsernameFromRequest(r))
	h.writeJSON(w, http.StatusOK, secretJSON(id, entry))
}

// handleSecretDelete permanently removes a secret. Any image fetched for it is
// left in place — images are shared storage and may be referenced elsewhere.
func (h *Handler) handleSecretDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !ValidSecretID(id) {
		http.Error(w, "invalid secret id", http.StatusBadRequest)
		return
	}
	if _, err := h.store.LoadSecret(id); err != nil {
		http.Error(w, "secret not found", http.StatusNotFound)
		return
	}
	if err := h.store.DeleteSecret(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.autoCommit.CommitSecretDelete(id, UsernameFromRequest(r))
	w.WriteHeader(http.StatusNoContent)
}

// handleSecretImage fetches a picture for the secret's site and attaches it.
// It is a no-op (404) for a secret without a URL. The browser calls this after
// creating or saving a secret that has a URL but no image.
func (h *Handler) handleSecretImage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !ValidSecretID(id) {
		http.Error(w, "invalid secret id", http.StatusBadRequest)
		return
	}
	entry, err := h.store.LoadSecret(id)
	if err != nil {
		http.Error(w, "secret not found", http.StatusNotFound)
		return
	}
	if entry.URL == "" {
		http.Error(w, "secret has no url", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), secretImageFetchTimeout)
	defer cancel()
	img, err := FetchSiteImage(ctx, h.siteImageClient(), entry.URL)
	if err != nil {
		// A site with no usable image is ordinary, not an error worth logging
		// loudly: the card keeps its mnemonic tile.
		h.writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	filename, err := h.saveSecretImage(id, img)
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "failed to store image"})
		return
	}
	_ = h.autoCommit.CommitImageSave(filename, UsernameFromRequest(r))

	entry.Image = filename
	if err := h.store.SaveSecret(id, *entry, entry.SecureMacro()); err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	_ = h.autoCommit.CommitSecretSave(id, UsernameFromRequest(r))

	h.writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"image": filename,
		"url":   "/images/" + filename,
	})
}

// siteImageClient returns the HTTP client used for site-image fetches.
func (h *Handler) siteImageClient() *http.Client {
	if h.siteImages != nil {
		return h.siteImages
	}
	return &http.Client{Timeout: secretImageFetchTimeout}
}

// SetSiteImageClient overrides the HTTP client used to fetch site images.
// Tests point it at a local test server.
func (h *Handler) SetSiteImageClient(c *http.Client) {
	h.siteImages = c
}

// saveSecretImage writes a fetched site image into the images directory and
// returns its filename: secret-<id>-<8hex><ext>.
func (h *Handler) saveSecretImage(id string, img *SiteImage) (string, error) {
	randBytes := make([]byte, 4)
	if _, err := rand.Read(randBytes); err != nil {
		return "", err
	}
	filename := fmt.Sprintf("secret-%s-%s%s", id, hex.EncodeToString(randBytes), img.Ext)
	if err := os.MkdirAll(h.store.ImagesDir(), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(h.store.ImagesDir(), filename), img.Data, 0o644); err != nil {
		return "", err
	}
	return filename, nil
}

// validSecretImageName guards the stored image field against path traversal:
// it names a file in the images directory, nothing else.
func validSecretImageName(name string) bool {
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) {
		return false
	}
	return name != "." && name != ".."
}
