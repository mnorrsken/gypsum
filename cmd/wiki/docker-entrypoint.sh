#!/bin/sh
set -eu

DATA_DIR="${GYPSUM_DATA_DIR:-/app/data}"
INIT_REPO="${GYPSUM_GIT_INIT:-}"
REMOTE_NAME="${GYPSUM_GIT_REMOTE_NAME:-origin}"
REMOTE_URL="${GYPSUM_GIT_REMOTE_URL:-}"
GIT_USERNAME="${GYPSUM_GIT_USERNAME:-}"
GIT_PASSWORD="${GYPSUM_GIT_PASSWORD:-}"
GIT_TOKEN="${GYPSUM_GIT_TOKEN:-}"

mkdir -p "$DATA_DIR/pages" "$DATA_DIR/secure"

is_true() {
  case "${1:-}" in
    1|true|TRUE|yes|YES|on|ON) return 0 ;;
    *) return 1 ;;
  esac
}

inject_auth_into_url() {
  url="$1"
  auth="$2"
  case "$url" in
    https://*)
      printf 'https://%s@%s' "$auth" "${url#https://}"
      ;;
    *)
      printf '%s' "$url"
      ;;
  esac
}

if is_true "$INIT_REPO"; then
  if ! git -C "$DATA_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    git -C "$DATA_DIR" init >/dev/null
  fi

  if [ -n "${GYPSUM_GIT_COMMIT_NAME:-}" ]; then
    git -C "$DATA_DIR" config user.name "$GYPSUM_GIT_COMMIT_NAME"
  fi
  if [ -n "${GYPSUM_GIT_COMMIT_EMAIL:-}" ]; then
    git -C "$DATA_DIR" config user.email "$GYPSUM_GIT_COMMIT_EMAIL"
  fi

  if [ -n "$REMOTE_URL" ]; then
    AUTH_URL="$REMOTE_URL"

    if [ -n "$GIT_TOKEN" ]; then
      AUTH_URL="$(inject_auth_into_url "$REMOTE_URL" "$GIT_TOKEN")"
    elif [ -n "$GIT_USERNAME" ] && [ -n "$GIT_PASSWORD" ]; then
      AUTH_URL="$(inject_auth_into_url "$REMOTE_URL" "$GIT_USERNAME:$GIT_PASSWORD")"
    fi

    if git -C "$DATA_DIR" remote get-url "$REMOTE_NAME" >/dev/null 2>&1; then
      git -C "$DATA_DIR" remote set-url "$REMOTE_NAME" "$AUTH_URL"
    else
      git -C "$DATA_DIR" remote add "$REMOTE_NAME" "$AUTH_URL"
    fi
  fi
fi

exec /app/gypsum
