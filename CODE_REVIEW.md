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

### 2. OAuth password compared in constant-time? (SECURITY)

**File:** `internal/wiki/oauth.go:231`

```go
if password != o.password {
```

The password comparison uses `!=`, which is vulnerable to timing side-channel attacks. While the risk is low for a personal wiki, this should use `crypto/subtle.ConstantTimeCompare` for defense-in-depth.

### 3. `findImageUsage` has O(pages × images) complexity (PERFORMANCE)

**File:** `internal/wiki/store.go:77-106`

For every page, `findImageUsage` re-reads the images directory and does a `strings.Contains` scan for every image filename. This is quadratic. For a wiki with many pages and images, this becomes a bottleneck.

**Recommendation:** Read the images directory once, then iterate pages and check for each image name in the page content.

### 4. `excerptForQuery` slices on byte index, not rune boundary (BUG)

**File:** `internal/wiki/store.go:342-373`

`strings.Index` returns a byte offset in the lowercased string, but `clean[start:end]` slices the original (mixed-case) string using those byte offsets. When the content contains multi-byte UTF-8 characters (common for a wiki supporting Unicode slugs like `Lösenord`), the index from the lowered string may not match the byte offsets in `clean`, potentially slicing mid-rune and producing garbled output.

**Recommendation:** Use `strings.ToLower(clean)` consistently or convert to `[]rune` for index arithmetic.

---

## Moderate Issues

### 5. OAuth authorization codes and tokens never expire/garbage-collect (RESOURCE LEAK)

**File:** `internal/wiki/oauth.go`

`o.codes` and `o.tokens` maps grow without bound. Expired entries are only cleaned up on access (e.g., `ValidateBearer` deletes an expired token when checked). Authorization codes that are never exchanged, or tokens that expire without being re-checked, remain in memory forever.

**Recommendation:** Add a periodic cleanup goroutine or a bounded map with LRU eviction.

### 6. `syncAsync` called with mutex held, spawns goroutine that re-acquires it (DEADLOCK RISK)

**File:** `internal/wiki/git_commit.go:232-242`

`syncAsync()` is called while `c.mu` is held (from `commitFile`/`commitDelete`). It spawns a goroutine that calls `c.mu.Lock()`. This works because the calling goroutine returns and releases the lock before the spawned goroutine tries to acquire it. However, this pattern is fragile — if the code flow changes so the caller doesn't immediately release the lock, it will deadlock. The comment says "Must be called with c.mu held" but the reason is not intuitive.

**Recommendation:** Either document this more explicitly or restructure so `syncAsync` doesn't need the caller to hold the mutex.

### 7. Templates re-parsed on every request (PERFORMANCE)

**File:** `internal/wiki/handlers.go:676-700`

Every call to `h.render()` re-parses the template files from disk. For a personal wiki this is fine (and aids development), but for production it adds unnecessary I/O and allocation overhead.

**Recommendation:** Parse templates once at startup (or use a `sync.Once`/cache). Re-parsing on every request is acceptable if this is intentional for live-reload during development — add a comment if so.

### 8. `myersDiff` is actually LCS-based, not Myers (NAMING)

**File:** `internal/wiki/diff.go:200`

The function is named `myersDiff` but the comment and implementation say "Simple O(NM) LCS-based diff." This is misleading.

**Recommendation:** Rename to `lcsDiff` or similar.

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
| `CODE_REVIEW.md` | This review document |
