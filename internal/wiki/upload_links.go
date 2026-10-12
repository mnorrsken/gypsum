package wiki

import (
	"sync"
	"time"
)

// uploadLinkTTL is how long an upload link from create_upload_link stays valid.
const uploadLinkTTL = 30 * time.Minute

// UploadLinks holds short-lived, single-use upload tokens handed out over
// MCP, so a photo taken on a phone can be uploaded without a wiki login.
// Tokens live in memory only; a restart invalidates them.
type UploadLinks struct {
	mu    sync.Mutex
	links map[string]uploadLink
	now   func() time.Time
}

type uploadLink struct {
	Page    string // page slug the uploads are appended to; "" = none
	Author  string // git author for the upload commits
	Expires time.Time
}

// NewUploadLinks creates an empty upload link store.
func NewUploadLinks() *UploadLinks {
	return &UploadLinks{links: make(map[string]uploadLink), now: time.Now}
}

// Create mints a new upload token.
func (u *UploadLinks) Create(page, author string) (string, time.Time) {
	u.mu.Lock()
	defer u.mu.Unlock()
	now := u.now()
	for t, l := range u.links {
		if now.After(l.Expires) {
			delete(u.links, t)
		}
	}
	token := oauthGenerateToken()
	expires := now.Add(uploadLinkTTL)
	u.links[token] = uploadLink{Page: page, Author: author, Expires: expires}
	return token, expires
}

// Get returns the link for a token if it exists and has not expired.
func (u *UploadLinks) Get(token string) (uploadLink, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	l, ok := u.links[token]
	if !ok || u.now().After(l.Expires) {
		return uploadLink{}, false
	}
	return l, true
}

// Consume removes and returns a valid link, so it can only be used once.
func (u *UploadLinks) Consume(token string) (uploadLink, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	l, ok := u.links[token]
	delete(u.links, token)
	if !ok || u.now().After(l.Expires) {
		return uploadLink{}, false
	}
	return l, true
}

// Restore puts back a consumed link whose upload failed, so the user can try
// again before it expires.
func (u *UploadLinks) Restore(token string, l uploadLink) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.links[token] = l
}
