# Gypsum

Self-hosted personal wiki for LLM-driven knowledge management. Go server
(`cmd/wiki`, all logic in `internal/wiki`), Markdown pages in a git repo under
`data/`, built-in MCP server (Streamable HTTP) plus `cmd/mcp-proxy`, skills and
notes, htmx/Alpine web UI in `web/`. Deployed with the Helm chart in `charts/`.

## Commands

- `make build`, `make run`, `make test`, `make vet`, `make fmt`, `make tidy`
- `make docker-build` / `make docker-run` for the container
- `make vendor-js` re-downloads the pinned htmx and Alpine builds after a
  version bump in the Makefile
- `make deploy` is mostly commented out; releases deploy through the Helm
  chart from CI, not from here

## Verify before done

`go build ./... && go vet ./... && go test ./...`. For MCP changes, start the
server and call the tool once (Claude Wiki connector or curl) to confirm the
schema and response.

## Documentation

`docs/` is served in-app, so it is part of the product. Update the relevant
file (`mcp.md`, `configuration.md`, `usage.md`, `skills.md`, `notes.md`,
`secrets.md`, `authentication.md`, `docker.md`, `helm.md`) for any change that
is not a pure bugfix: new, renamed or removed MCP tools or parameters, new
endpoints, config keys, or behaviour changes.

## Release notes (what differs from the wiki "GitHub Release Process" skill)

- Changelog heading: `## vX.Y.Z`, sections `### Added` / `### Changed` /
  `### Fixed` and optionally `### Notes` for migration callouts. Bold the
  feature name, then a dash and a concise description.
- README "Wiki Features" list gets a bullet for each new capability.
- One commit for code, docs, changelog and readme: `Add <feature> (vX.Y.Z)`.
- `ci.yml` on push/PR; `release.yml` (binary and image) and `helm.yml` (chart to
  GHCR) on `v*` tags. Version comes only from git tags.
