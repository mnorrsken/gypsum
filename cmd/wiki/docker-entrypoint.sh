#!/bin/sh
set -eu

DATA_DIR="${GYPSUM_DATA_DIR:-/app/data}"
REPO_DIR="$DATA_DIR/repo"
INIT_REPO="${GYPSUM_GIT_INIT:-}"

mkdir -p "$REPO_DIR/pages"

is_true() {
  case "${1:-}" in
    1|true|TRUE|yes|YES|on|ON) return 0 ;;
    *) return 1 ;;
  esac
}

# Mark repo directory as safe for git (required when volume ownership
# differs from the running user, e.g. PVC mounts in Kubernetes).
git config --global --add safe.directory "$REPO_DIR"

if is_true "$INIT_REPO"; then
  if ! git -C "$REPO_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    git -C "$REPO_DIR" init >/dev/null
  fi
fi

# Remote configuration, pulling, and pushing are handled by the Go
# application via GitAutoCommitter — no shell-based setup needed.

exec gypsum
