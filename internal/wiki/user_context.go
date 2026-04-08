package wiki

import (
	"context"
	"net/http"
)

// Context keys for per-request user wiki resolution.
type userCtxKey string

const (
	ctxUserStore      userCtxKey = "user-store"
	ctxUserAutoCommit userCtxKey = "user-autocommit"
	ctxUserDB         userCtxKey = "user-db"
	ctxUserPrefix     userCtxKey = "user-prefix" // URL prefix for building links
	ctxTargetUser     userCtxKey = "target-user"  // the wiki owner's username
)

// withUserContext injects a UserContext into the request context.
func withUserContext(r *http.Request, uc *UserContext, prefix, targetUser string) *http.Request {
	ctx := r.Context()
	ctx = context.WithValue(ctx, ctxUserStore, uc.Store)
	ctx = context.WithValue(ctx, ctxUserAutoCommit, uc.AutoCommit)
	ctx = context.WithValue(ctx, ctxUserDB, uc.DB)
	ctx = context.WithValue(ctx, ctxUserPrefix, prefix)
	ctx = context.WithValue(ctx, ctxTargetUser, targetUser)
	return r.WithContext(ctx)
}

// storeFor returns the PageStore for this request: either the per-user store
// from context or the handler's shared store.
func (h *Handler) storeFor(r *http.Request) *PageStore {
	if s, ok := r.Context().Value(ctxUserStore).(*PageStore); ok {
		return s
	}
	return h.store
}

// autoCommitFor returns the GitAutoCommitter for this request.
func (h *Handler) autoCommitFor(r *http.Request) *GitAutoCommitter {
	if ac, ok := r.Context().Value(ctxUserAutoCommit).(*GitAutoCommitter); ok {
		return ac
	}
	return h.autoCommit
}

// dbFor returns the DB for this request.
func (h *Handler) dbFor(r *http.Request) *DB {
	if db, ok := r.Context().Value(ctxUserDB).(*DB); ok {
		return db
	}
	return h.db
}

// urlPrefix returns the URL prefix for building links in the current context.
// Returns "" for the shared wiki or "/~username" for per-user wikis.
func urlPrefix(r *http.Request) string {
	if p, ok := r.Context().Value(ctxUserPrefix).(string); ok {
		return p
	}
	return ""
}

// targetUser returns the wiki owner's username from context, or "".
func targetUser(r *http.Request) string {
	if u, ok := r.Context().Value(ctxTargetUser).(string); ok {
		return u
	}
	return ""
}
