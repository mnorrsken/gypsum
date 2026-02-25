#!/bin/sh
set -eu

DATA_DIR="${GYPSUM_DATA_DIR:-/app/data}"
INIT_REPO="${GYPSUM_GIT_INIT:-}"

mkdir -p "$DATA_DIR/pages"

is_true() {
  case "${1:-}" in
    1|true|TRUE|yes|YES|on|ON) return 0 ;;
    *) return 1 ;;
  esac
}

# Mark data directory as safe for git (required when volume ownership
# differs from the running user, e.g. PVC mounts in Kubernetes).
git config --global --add safe.directory "$DATA_DIR"

if is_true "$INIT_REPO"; then
  if ! git -C "$DATA_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    git -C "$DATA_DIR" init >/dev/null
  fi
fi

# Remote configuration, pulling, and pushing are handled by the Go
# application via GitAutoCommitter — no shell-based setup needed.

exec /app/gypsum
