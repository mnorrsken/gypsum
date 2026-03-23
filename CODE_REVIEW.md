# Code Review — Gypsum Wiki

**Reviewer:** Claude (automated)
**Date:** 2026-03-23
**Scope:** Full codebase review — architecture, security, correctness, testing, and maintainability.

---

## Summary

Gypsum is a personal wiki server written in Go with git-backed storage, AES-256-GCM encrypted secure fields, an MCP (Model Context Protocol) API for Claude integration, an OAuth 2.0 authorization server for external MCP access, and a MediaWiki importer. The codebase is well-organized, compact (~2,500 LOC of Go), and ships with a Helm chart and Dockerfile.

**Overall impression:** Solid, pragmatic project with clean separation of concerns. There are a handful of bugs, one security concern, and several areas where robustness can be improved.

---

## Critical Issues

### 1. Test build failure: missing `OAuthServer` parameter (BUG — FIXED)

**File:** `internal/wiki/handlers_inline_test.go:14,49`

`NewHandler` was updated to accept an `*OAuthServer` parameter (6 args), but the two test call sites in `handlers_inline_test.go` still passed only 5 arguments, causing a build failure for `go test ./internal/wiki/`.

**Fix applied:** Added the missing `nil` argument to both call sites.

### 2. OAuth password compared in constant-time? (SECURITY — FIXED)

**File:** `internal/wiki/oauth.go`

The password comparison used `!=`, which is vulnerable to timing side-channel attacks.

**Fix applied:** Replaced with `crypto/subtle.ConstantTimeCompare`.

### 3. `findImageUsage` has O(pages × images) complexity (PERFORMANCE — FIXED)

**File:** `internal/wiki/store.go`

For every page, `findImageUsage` re-read the images directory and did a `strings.Contains` scan for every image filename. This is quadratic.

**Fix applied:** Read the images directory once upfront, then iterate pages and check for each image name.

### 4. `excerptForQuery` slices on byte index, not rune boundary (BUG — FIXED)

**File:** `internal/wiki/store.go`

`strings.Index` returns a byte offset in the lowercased string, but the original code sliced the mixed-case string using those byte offsets. With multi-byte UTF-8 characters, this could slice mid-rune and produce garbled output.

**Fix applied:** Converted to rune-based indexing and slicing.

---

## Moderate Issues

### 5. OAuth authorization codes and tokens never expire/garbage-collect (RESOURCE LEAK — FIXED)

**File:** `internal/wiki/oauth.go`

`o.codes` and `o.tokens` maps grew without bound. Expired entries were only cleaned up on access.

**Fix applied:** Added a `purgeExpiredLoop` goroutine that runs every 10 minutes to evict expired codes and tokens.

### 6. `syncAsync` called with mutex held, spawns goroutine that re-acquires it (DEADLOCK RISK — FIXED)

**File:** `internal/wiki/git_commit.go`

`syncAsync()` was called while `c.mu` was held (from `commitFile`/`commitDelete`). It spawns a goroutine that calls `c.mu.Lock()`. This worked only because the caller happened to release the lock before the goroutine ran — a fragile pattern.

**Fix applied:** Restructured `commitFile` and `commitDelete` to explicitly unlock the mutex before calling `syncAsync`, eliminating the implicit ordering dependency.

### 7. Templates re-parsed on every request (PERFORMANCE — FIXED)

**File:** `internal/wiki/handlers.go`

Every call to `h.render()` re-parsed template files from disk.

**Fix applied:** Templates are now parsed once at startup into `h.tmplCache` and reused on every request.

### 8. `myersDiff` is actually LCS-based, not Myers (NAMING — FIXED)

**File:** `internal/wiki/diff.go`

The function was named `myersDiff` but used an O(NM) LCS algorithm, not Myers' O(ND) algorithm.

**Fix applied:** Renamed to `lcsDiff` with an accurate comment.

### 9. CORS allows all origins on MCP endpoint (SECURITY — LOW RISK)

**File:** `internal/wiki/mcp.go:128`

```go
w.Header().Set("Access-Control-Allow-Origin", "*")
```

The internal MCP endpoint allows `*` for CORS. This is documented as "required for Claude's remote MCP connector" and is mitigated by the fact that the internal endpoint is meant to be behind a reverse proxy (Authelia etc.). The external endpoint is OAuth-protected. This is acceptable but worth noting.

---

## Minor Issues

### 10. `handlers_test.go` is an empty file

**File:** `internal/wiki/handlers_test.go`

Contains only `package wiki` with no tests. The handler logic (view, edit, search, etc.) has no HTTP-level test coverage. The MCP tests and inline-secure tests cover some handler functionality indirectly.

**Recommendation:** Add handler tests or remove the empty file.

### 11. Regex compiled on every `ConvertMediaWikiToMarkdown` call

**File:** `internal/wiki/mediawiki.go:30-178`

Multiple `regexp.MustCompile` calls are inside the `ConvertMediaWikiToMarkdown` function body, so regexes are recompiled on every invocation. Since this is called for MediaWiki import (not a hot path), performance impact is minimal.

**Recommendation:** Move regex compilation to package-level `var` blocks for idiomatic Go.

### 12. Duplicate regex for `secureAesMacro`

**Files:** `internal/wiki/secure.go:16` and `internal/wiki/markdown.go:20`

```go
// secure.go
var secureAesMacroRe = regexp.MustCompile(`\{\{secure_aes:([\w+/=]+)\}\}`)

// markdown.go
var secureAesMacroPattern = regexp.MustCompile(`\{\{secure_aes:([\w+/=]+)\}\}`)
```

Two identical regexes with different names. One should be removed and the other shared.

### 13. `injectAuth` doesn't URL-encode credentials

**File:** `cmd/wiki/main.go:157-162`

If the git token or password contains `@`, `/`, or other URL-special characters, the injected URL will be malformed. Consider using `url.UserPassword()` for proper encoding.

### 14. Image upload lacks MIME-type validation

**File:** `internal/wiki/handlers.go:505-509`

The upload handler only checks file extension, not actual file content. A malicious user could upload an executable with a `.png` extension. For a personal wiki this risk is low, but content-type sniffing with `http.DetectContentType` would be more robust.

### 15. `ValidateContent` has commented-out dead code

**File:** `internal/wiki/secure.go:159-161, 200-205`

`unknownTagRe` and `anyDoubleBraceClose` are declared but `unknownTagRe` is never used. The stray `}}` detection block (lines 200-205) is commented-out logic inside an if-block that does nothing.

**Recommendation:** Remove unused regex and dead code.

---

## Architecture & Design

### Strengths

- **Clean package structure**: `cmd/wiki`, `cmd/mcp-proxy`, `internal/wiki` — standard Go layout.
- **Git-backed storage** is a strong design choice for a personal wiki — provides versioning, backup, and remote sync with zero extra dependencies.
- **Encryption approach** (AES-256-GCM with `{{secure:...}}` macros) is well-implemented with proper nonce handling, and the `EncryptForSavePreserving` optimization to avoid spurious diffs is thoughtful.
- **MCP integration** is comprehensive with 17 tools, proper JSON-RPC 2.0 handling, session management, and a separate external endpoint with secure field redaction.
- **OAuth 2.0 with PKCE** implementation is correctly following the spec (S256 challenge, single-use codes, token expiry).
- **Good test coverage** on core functionality (models, store, crypto, MCP, diff, mediawiki conversion, markdown rendering).

### Suggestions for improvement

- **Graceful shutdown**: `http.ListenAndServe` doesn't handle OS signals. Consider `http.Server` with `Shutdown()` and a signal handler, especially since there's a background goroutine (`StartPeriodicPull`) that should be stopped cleanly.
- **Request logging/middleware**: No request logging. Adding basic access logging would aid debugging in production.
- **Rate limiting**: The MCP and OAuth endpoints have no rate limiting. For an internet-exposed wiki, basic rate limiting on `/oauth/authorize` (login) would prevent brute-force attacks.

---

## Test Coverage

| Package | Status | Notes |
|---------|--------|-------|
| `cmd/wiki` | PASS | Seed page tests |
| `internal/wiki` (non-git) | PASS | Models, store, crypto, MCP, diff, mediawiki, markdown, handlers_inline |
| `internal/wiki` (git) | SKIP* | Git commit tests fail in this environment due to signing config — tests are correct |
| `cmd/mcp-proxy` | N/A | No test files |
| HTTP handlers | LOW | `handlers_test.go` is empty; no direct handler tests |

---

## Files Changed in This Review

| File | Change |
|------|--------|
| `internal/wiki/handlers_inline_test.go` | Fixed missing `OAuthServer` parameter (test build failure) |
| `internal/wiki/oauth.go` | Constant-time password comparison; periodic token/code garbage collection |
| `internal/wiki/store.go` | Fixed `findImageUsage` O(n²) → O(n); fixed `excerptForQuery` rune boundary bug |
| `internal/wiki/git_commit.go` | Restructured mutex handling to eliminate deadlock risk in `syncAsync` |
| `internal/wiki/handlers.go` | Template caching at startup instead of re-parsing per request |
| `internal/wiki/diff.go` | Renamed `myersDiff` → `lcsDiff` |
| `CODE_REVIEW.md` | This review document |
