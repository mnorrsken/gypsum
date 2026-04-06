package wiki

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const ctxUsername contextKey = "auth-username"

// HeaderAuthConfig configures header-based authentication via a reverse proxy.
type HeaderAuthConfig struct {
	// UserHeader is the HTTP header containing the authenticated username (default: Remote-User).
	UserHeader string
	// GroupHeader is the HTTP header containing comma-separated groups (default: Remote-Group).
	GroupHeader string
	// RequiredGroup, if set, requires this group to be present in the group header.
	RequiredGroup string
}

// UsernameFromRequest returns the authenticated username from the request context, or "".
func UsernameFromRequest(r *http.Request) string {
	if v, ok := r.Context().Value(ctxUsername).(string); ok {
		return v
	}
	return ""
}

// HeaderAuth returns middleware that enforces the configured header authentication.
// Requests without a valid username header (or missing the required group) get a 403.
// OAuth paths (/.well-known/*, /oauth/*, /mcp) are excluded.
func HeaderAuth(cfg HeaderAuthConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path

			// Skip auth for static assets, public pages, and OAuth-related endpoints.
			if strings.HasPrefix(path, "/static/") ||
				strings.HasPrefix(path, "/public/") ||
				strings.HasPrefix(path, "/.well-known/") ||
				strings.HasPrefix(path, "/oauth/") ||
				strings.HasPrefix(path, "/mcp/") || 
				path == "/mcp" {
				next.ServeHTTP(w, r)
				return
			}

			username := r.Header.Get(cfg.UserHeader)
			if username == "" {
				http.Error(w, "Forbidden: missing authentication header", http.StatusForbidden)
				return
			}

			if cfg.RequiredGroup != "" {
				groups := r.Header.Get(cfg.GroupHeader)
				if !containsGroup(groups, cfg.RequiredGroup) {
					http.Error(w, "Forbidden: insufficient group membership", http.StatusForbidden)
					return
				}
			}

			ctx := context.WithValue(r.Context(), ctxUsername, username)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// containsGroup checks if needle is one of the comma-separated groups in the header value.
func containsGroup(header, needle string) bool {
	for _, g := range strings.Split(header, ",") {
		if strings.TrimSpace(g) == needle {
			return true
		}
	}
	return false
}
