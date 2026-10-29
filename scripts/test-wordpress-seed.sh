#!/usr/bin/env bash
# Loads the importer's test fixture into a throwaway Postgres twice and checks
# that the seed is idempotent, draft-only and leaves edited items alone.
# Needs Docker (or set TEST_DATABASE_URL to an empty scratch database).
set -euo pipefail
cd "$(dirname "$0")/.."
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"; [ -n "${cid:-}" ] && docker rm -f "$cid" >/dev/null' EXIT
mkdir -p "$tmp/content/wordpress" && cp -r tools/wpimport/testdata/raw "$tmp/content/wordpress/raw"
(cd tools/wpimport && go run . build -root "$tmp" -download-media=false -migration "")

url="${TEST_DATABASE_URL:-}"
if [ -z "$url" ]; then
  cid="$(docker run -d -e POSTGRES_PASSWORD=pw -p 127.0.0.1::5432 postgres:18-alpine)"
  port="$(docker port "$cid" 5432 | head -1 | sed 's/.*://')"
  url="postgres://postgres:pw@127.0.0.1:$port/postgres?sslmode=disable"
  until psql "$url" -c 'select 1' >/dev/null 2>&1; do sleep 1; done
fi
p() { psql "$url" -v ON_ERROR_STOP=1 -qAt "$@"; }
check() { [ "$2" = "$3" ] || { echo "FAIL $1: got '$2' want '$3'"; exit 1; }; echo "ok   $1"; }

p -f tools/wpimport/testdata/schema.sql
p -c "insert into cms_pages(kind,slug,title) values('page','storage','Native page')"
p -f "$tmp/content/wordpress/seed.sql" 2>/dev/null
check "items imported (one skipped for slug clash)" "$(p -c "select count(*) from cms_pages where source='wordpress'")" 5
check "all drafts, unpublished" "$(p -c "select count(*) from cms_pages where source='wordpress' and (status<>'draft' or published_at is not null)")" 0
check "hierarchy" "$(p -c "select par.slug from cms_pages c join cms_pages par on par.id=c.parent_id where c.slug='raw-device-creation'")" oracleasm
p -c "update cms_pages set title='EDITED', updated_at=now()+interval '1s' where source_id='11'"
p -c "update cms_pages set status='published', published_at=now(), updated_at=now()+interval '1s' where source_id='12'"
p -f "$tmp/content/wordpress/seed.sql" 2>/dev/null
check "rerun adds nothing" "$(p -c "select count(*) from cms_pages")" 6
check "edited draft kept" "$(p -c "select title from cms_pages where source_id='11'")" EDITED
check "published item kept" "$(p -c "select status from cms_pages where source_id='12'")" published
check "media" "$(p -c "select count(*) from cms_media")" 1
check "redirects" "$(p -c "select to_path from cms_redirects where from_path='/2009/04/28/hello-world'")" /blog/hello-world
