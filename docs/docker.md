# Docker

## Build

```bash
make docker-build
# or
docker build -t gypsum:latest .
```

## Run

```bash
make docker-run
# or
docker run --rm -p 8080:8080 -v $(pwd)/data:/app/data gypsum:latest
```

Mount `/app/data` to persist pages and images across container restarts.

## Docker Compose

A ready-to-use [`docker-compose.yaml`](../docker-compose.yaml) is included in the repository:

```bash
docker compose up -d
```

Adjust environment variables in the file to set your encryption key and optional git remote sync.

## Environment Variables

All [application variables](configuration.md) apply inside the container. The Docker entrypoint script also supports these additional variables:

| Variable | Default | Description |
|---|---|---|
| `GYPSUM_DATA_DIR` | `/app/data` | Path to the data directory inside the container |
| `GYPSUM_GIT_INIT` | _(empty)_ | Set to `true` to initialize a git repo on startup |
| `GYPSUM_GIT_REMOTE_NAME` | `origin` | Name of the git remote |
| `GYPSUM_GIT_REMOTE_URL` | _(empty)_ | URL of a git remote for backup/sync |
| `GYPSUM_GIT_USERNAME` | _(empty)_ | Git username for HTTPS authentication |
| `GYPSUM_GIT_PASSWORD` | _(empty)_ | Git password for HTTPS authentication |
| `GYPSUM_GIT_TOKEN` | _(empty)_ | Git token (takes precedence over username/password) |
| `GYPSUM_GIT_COMMIT_NAME` | _(empty)_ | Git commit author name |
| `GYPSUM_GIT_COMMIT_EMAIL` | _(empty)_ | Git commit author email |
| `GYPSUM_GIT_PULL_INTERVAL` | `5m` | How often to pull from the remote |
