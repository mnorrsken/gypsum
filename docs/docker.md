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

## Process Model

The image runs [tini](https://github.com/krallin/tini) as PID 1, which starts
the entrypoint script and forwards signals to it. tini's job is to reap orphaned
processes: git forks helpers (`git-remote-https`, `ssh`, credential helpers) that
can outlive the `git` process that started them, and once orphaned they are
reparented to PID 1. Without an init that reaps them, they pile up as zombies
until the container hits its PID limit and git starts failing with
`cannot fork()`.

If you run the `gypsum` binary directly as PID 1 in a custom image, run it under
an init (`docker run --init`, tini, or `shareProcessNamespace`/an init container
in Kubernetes) for the same reason.

## Debugging

The runtime image bundles a few basic tools so you can exec into a running
container and poke around: `bash` (shell), `curl` (HTTP client), `nano` (editor),
and `git`.

```bash
docker exec -it <container> bash
# then, e.g.
curl -s localhost:8080/git-status

# count processes; a healthy container has a handful, and no long-lived
# <defunct> entries
ps -eo pid,ppid,stat,args | grep -c ''
ps -eo pid,ppid,stat,args | grep -E 'defunct|git'
```

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
| `GYPSUM_GIT_PUSH_DELAY` | `30s` | Debounce window for pushes; `0` pushes immediately |
