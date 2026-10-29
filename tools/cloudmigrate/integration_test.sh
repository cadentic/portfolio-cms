#!/usr/bin/env bash
# End-to-end test of the database moves with real PostgreSQL 18 servers:
#   "neon"  a Postgres 18 container with the app's migrations and some data
#   "vps"   infra/vps/compose.yml's db service, reached through a stand-in for ssh
#   "other" an empty Postgres 18 container
#   "media" an api-like container with a media volume, also reached through ssh
# Needs Docker with the compose plugin. Usage: tools/cloudmigrate/integration_test.sh
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
work="$(mktemp -d)"
id="cmit$$"

cleanup() {
  docker rm -f "$id-src" "$id-dst" >/dev/null 2>&1 || true
  (cd "$work/vps" 2>/dev/null && docker compose down -v >/dev/null 2>&1) || true
  (cd "$work/media" 2>/dev/null && docker compose down -v >/dev/null 2>&1) || true
  rm -rf "$work"
}
trap cleanup EXIT

free_port() { python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])'; }
src_port=$(free_port)
dst_port=$(free_port)

echo "== starting Postgres 18 servers"
docker run -d --name "$id-src" -e POSTGRES_PASSWORD='s3cr:t@pw' -p "127.0.0.1:$src_port:5432" postgres:18-alpine >/dev/null
docker run -d --name "$id-dst" -e POSTGRES_PASSWORD=other -p "127.0.0.1:$dst_port:5432" postgres:18-alpine >/dev/null

# The "VPS": the production compose file with only the database enabled.
mkdir -p "$work/vps" "$work/backups"
cp "$repo/infra/vps/compose.yml" "$repo/infra/vps/Caddyfile" "$work/vps/"
cat > "$work/vps/.env" <<'EOF'
COMPOSE_PROFILES='localdb'
POSTGRES_PASSWORD='vps-test-password-123'
DATABASE_URL='postgres://portfolio:vps-test-password-123@db:5432/portfolio?sslmode=disable'
APP_SECRET='x'
IMAGE_TAG='it'
DOMAIN='example.test'
PUBLIC_SITE_URL='https://example.test'
EOF
for v in FIREBASE_PROJECT_ID FIREBASE_API_KEY ADMIN_EMAILS EMAIL_FROM_ADDRESS EMAIL_BREVO_API_KEY \
  EMAIL_BREVO_WEBHOOK_SECRET EMAIL_MAILJET_API_KEY EMAIL_MAILJET_SECRET_KEY EMAIL_MAILJET_WEBHOOK_SECRET; do
  echo "$v='x'" >> "$work/vps/.env"
done
(cd "$work/vps" && COMPOSE_PROJECT_NAME="$id" docker compose up -d --wait --wait-timeout 120 db 2>&1 | tail -1)
# Every compose command the tool sends must reach this project, not "portfolio".
sed -i "s/^name: portfolio$/name: $id/" "$work/vps/compose.yml"

# Stand-in for the api container and its media volume (the real image also runs
# as user 10001, which owns /data/media).
mkdir -p "$work/media"
cat > "$work/media/compose.yml" <<EOF
name: ${id}m
services:
  api:
    build:
      dockerfile_inline: |
        FROM alpine:3.22
        RUN mkdir -p /data/media && chown 10001:10001 /data/media
        USER 10001:10001
    command: ["sleep", "infinity"]
    volumes: [media:/data/media]
volumes:
  media:
EOF
(cd "$work/media" && docker compose up -d --build --wait 2>&1 | tail -1)

# Stand-in for ssh: drop the options and destination, run the remote command here.
cat > "$work/fake-ssh" <<'EOF'
#!/bin/sh
while [ $# -gt 1 ]; do shift; done
exec sh -c "$1"
EOF
chmod +x "$work/fake-ssh"

wait_pg() { for _ in $(seq 1 60); do docker exec "$1" pg_isready -U postgres >/dev/null 2>&1 && return; sleep 1; done; exit 1; }
wait_pg "$id-src"
wait_pg "$id-dst"

# Apply the app's migrations the way the API does: one transaction each.
migrate() {
  local container=$1 user=$2 db=$3
  docker exec -i "$container" psql -X -q -v ON_ERROR_STOP=1 -U "$user" -d "$db" \
    -c "CREATE TABLE IF NOT EXISTS schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())"
  for f in "$repo"/backend/internal/store/migrations/*.sql; do
    v=$(basename "$f" .sql)
    { echo "BEGIN;"; cat "$f"; echo ";INSERT INTO schema_migrations (version) VALUES ('$v'); COMMIT;"; } |
      docker exec -i "$container" psql -X -q -v ON_ERROR_STOP=1 -U "$user" -d "$db" >/dev/null
  done
}
echo "== loading the schema and sample data into the source"
docker exec "$id-src" createdb -U postgres portfolio
migrate "$id-src" postgres portfolio
docker exec -i "$id-src" psql -X -q -v ON_ERROR_STOP=1 -U postgres -d portfolio <<'EOF'
INSERT INTO contact_messages (name, email, message, ip) VALUES
  ('Ada', 'ada@example.com', 'Hello from the old host, with ''quotes'' and ünïcode', '203.0.113.7'),
  ('Bob', 'BOB@example.com', E'Two\nlines', NULL);
UPDATE cms_pages SET status = 'published', published_at = now() WHERE slug = 'hello-world';
EOF
echo "== the fresh VPS stack has run its own migrations (empty tables)"
migrate "$(cd "$work/vps" && COMPOSE_PROJECT_NAME="$id" docker compose ps -q db)" portfolio portfolio

docker exec "$id-dst" createdb -U postgres portfolio

export CLOUDMIGRATE_IT=1 CLOUDMIGRATE_SSH="$work/fake-ssh"
export IT_SOURCE_URL="postgres://postgres:s3cr%3At%40pw@127.0.0.1:$src_port/portfolio?sslmode=disable"
export IT_TARGET_URL="postgres://postgres:other@127.0.0.1:$dst_port/portfolio?sslmode=disable"
export IT_VPS_DIR="$work/vps" IT_BACKUPS="$work/backups" IT_MEDIA_DIR="$work/media"

echo "== unit and database tests"
cd "$here"
python3 -m unittest -v test_cloudmigrate

echo "== command line"
python3 cloudmigrate.py --client docker db info "$IT_SOURCE_URL" > "$work/info.txt"
grep -q "contact_messages *2$" "$work/info.txt"
python3 cloudmigrate.py --client docker db verify "$IT_SOURCE_URL" "$IT_TARGET_URL" > "$work/verify.txt"
tail -1 "$work/verify.txt"
python3 cloudmigrate.py db info vps > "$work/vps.txt" 2>&1 && exit 1 # no secret.tfvars: must fail cleanly
grep -q "secret.tfvars not found" "$work/vps.txt" || { cat "$work/vps.txt"; exit 1; }
ls "$work/backups"
echo "integration test passed"
