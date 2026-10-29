#!/usr/bin/env bash
# Migrate csayantan.wordpress.com into the CMS.
#
#   scripts/wordpress-import.sh fetch   download posts, pages and images from
#                                       WordPress.com and regenerate
#                                       content/wordpress/* and migration 0003
#   scripts/wordpress-import.sh build   regenerate from the saved raw API files
#   scripts/wordpress-import.sh load    apply content/wordpress/seed.sql to
#                                       $DATABASE_URL (safe to repeat)
#
# Uses a local Go toolchain if present, otherwise the golang Docker image.
# Everything is imported as a draft; nothing is published and no newsletter
# is sent.
set -euo pipefail
cd "$(dirname "$0")/.."
cmd="${1:-}"; shift || true

run_go() {
  if command -v go >/dev/null 2>&1; then
    (cd tools/wpimport && go run . "$@" -root ../..)
  else
    docker run --rm -v "$PWD":/src -w /src/tools/wpimport golang:1.23 go run . "$@" -root /src
  fi
}

case "$cmd" in
  fetch|build) run_go "$cmd" "$@" ;;
  load)
    if [ -z "${DATABASE_URL:-}" ] && [ -f .env ]; then
      DATABASE_URL="$(grep -E '^DATABASE_URL=' .env | tail -1 | cut -d= -f2- | tr -d '"')"
    fi
    : "${DATABASE_URL:?set DATABASE_URL (e.g. postgres://user:pass@localhost:5432/db?sslmode=disable)}"
    if command -v psql >/dev/null 2>&1; then
      psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f content/wordpress/seed.sql
    else
      docker run --rm -i --network host postgres:18-alpine psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f - < content/wordpress/seed.sql
    fi
    ;;
  *) sed -n '2,13p' "$0"; exit 2 ;;
esac
