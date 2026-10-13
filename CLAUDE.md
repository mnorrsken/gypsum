# Gypsum

Self-hosted personal wiki for LLM-driven knowledge management. Go server
(`cmd/wiki`, all logic in `internal/wiki`), Markdown pages in a git repo under
`data/`, built-in MCP server (Streamable HTTP) plus `cmd/mcp-proxy`, skills and
notes, htmx/Alpine web UI in `web/`. Deployed with the Helm chart in `charts/`.

## Commands

- `make build`, `make run`, `make test`, `make vet`, `make fmt`, `make tidy`
- `make ui-test` runs the browser tests in headless Chrome (chromedp, build tag
  `uitest`, `internal/uitest`); `UI_SCREENSHOTS=dir` saves screenshots
- `make vendor-icons` rebuilds `web/static/icons.svg` (Lucide) from
  `LUCIDE_VERSION` and the `ICONS` list in the Makefile
- `make docker-build` / `make docker-run` for the container
- `make vendor-js` re-downloads the pinned htmx and Alpine builds after a
  version bump in the Makefile
- `make deploy` is mostly commented out; releases deploy through the Helm
  chart from CI, not from here

## Verify before done

`go build ./... && go vet ./... && go test ./...`. For MCP changes, start the
server and call the tool once (Claude Wiki connector or curl) to confirm the
schema and response. For UI changes (templates, CSS, JS) also run
`make ui-test`; it is skipped when Chrome is missing, but CI fails without it.

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
- `ci.yml` on push/PR; `release.yml` (tests, image, GitHub Release with
  binaries and the CHANGELOG section as notes) and `helm.yml` (chart to GHCR) on
  `v*` tags. Version comes only from git tags.
- Deploying: `~/dev/gitops/gypsum` follows the newest chart (no pin). Every tag
  publishes a chart with `version` and `appVersion` set to the tag, so chart and
  image always match. Argo CD caches the render per gitops commit (up to 24 h);
  a hard refresh of the `gypsum` app rolls a release out at once.
