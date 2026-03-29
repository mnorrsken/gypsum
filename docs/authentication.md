# Authentication

Gypsum supports reverse proxy authentication via HTTP headers, designed for use with identity providers like [Authelia](https://www.authelia.com/) or [Authentik](https://goauthentik.io/).

## Header Authentication

Set `GYPSUM_AUTH_USER_HEADER` to a header name (e.g. `Remote-User`) to enable header auth. When set, every request (except OAuth and health probe endpoints) must include this header. The username from the header is used as the git commit author.

Optionally restrict access to a specific group:

```bash
GYPSUM_AUTH_USER_HEADER=Remote-User
GYPSUM_AUTH_GROUP_HEADER=Remote-Group       # default
GYPSUM_AUTH_REQUIRED_GROUP=wiki-users
```

## Authelia Setup

A typical Authelia `access_control` configuration for Gypsum:

```yaml
access_control:
  rules:
    # Bypass auth for endpoints that handle their own authentication
    # or must be publicly accessible
    - domain: wiki.example.com
      resources:
        - "^/mcp/external.*$"
        - "^/.well-known/oauth.*$"
        - "^/oauth/.*$"
        - "^/public/.*$"
        - "^/static/.*$"
        - "^/healthz$"
        - "^/readyz$"
      policy: bypass

    # Protect the wiki UI and internal MCP endpoint
    - domain: wiki.example.com
      policy: one_factor
```

### What each bypass covers

| Path | Reason |
|---|---|
| `/mcp/external` | OAuth-protected MCP endpoint (has its own auth) |
| `/.well-known/oauth*` | OAuth discovery documents |
| `/oauth/*` | OAuth authorization and token endpoints |
| `/public/*` | Publicly shared pages (secret-link access) |
| `/static/*` | CSS, JS, and other static assets (needed by public pages) |
| `/healthz`, `/readyz` | Kubernetes health probes (served on a separate port, but bypass is harmless) |

### Nginx auth_request

If you're using nginx with Authelia, ensure the `auth_request` directive forwards the user header:

```nginx
location / {
    auth_request /authelia;
    auth_request_set $user $upstream_http_remote_user;
    auth_request_set $groups $upstream_http_remote_groups;
    proxy_set_header Remote-User $user;
    proxy_set_header Remote-Group $groups;
    proxy_pass http://gypsum:8080;
}
```

### Traefik with Authelia

With Traefik and Authelia as ForwardAuth middleware, the `Remote-User` and `Remote-Group` headers are forwarded automatically. Configure the middleware in your Traefik dynamic config or Docker labels:

```yaml
http:
  middlewares:
    authelia:
      forwardAuth:
        address: "http://authelia:9091/api/authz/forward-auth"
        authResponseHeaders:
          - "Remote-User"
          - "Remote-Group"
```

Then apply the middleware to the Gypsum router, and set the bypass rules in Authelia as shown above.
