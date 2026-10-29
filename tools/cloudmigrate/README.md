# cloudmigrate

Moves portfolio-cms between hosts, its database between PostgreSQL servers,
and its media between buckets. One Python file, standard library only. It
reads everything from `infra/terraform/secret.tfvars` and runs the same
Ansible playbook you deploy with, so a move is a deploy plus a careful copy.

- **Hosts**: Google Cloud Run (`gcp`), Azure Container Apps (`azure`), any VPS
  with root SSH such as a Bluehost VPS (`vps`).
- **Databases**: `neon` (`database_url`), `vps` (the PostgreSQL 18 container on
  the server, reached over SSH), `local` (the `docker compose` stack here) or
  any `postgres://` URL.
- **Media**: `gcs` or `azure` (the bucket in `secret.tfvars`), `vps` (the media
  volume on the server), `local`, `gs://BUCKET`, `az://ACCOUNT/CONTAINER`, or a folder.

DNS stays on Cloudflare the whole time. Only the site records (apex A/AAAA,
`www`, Azure's `asuid` TXT) change, at the very end of a move; mail records
never change.

Needs Python 3.11+, and on the machine you run it from the deploy tools
(Docker, Terraform, `ansible-core`, `ssh` for a VPS) plus `gcloud` or `az`
for bucket copies. PostgreSQL 18 client tools are optional: without them the
tool runs `pg_dump` and `psql` in a `postgres:18-alpine` container.

```sh
python3 tools/cloudmigrate/cloudmigrate.py check    # tools, settings, where the domain points
```

## Moving the site

```sh
python3 tools/cloudmigrate/cloudmigrate.py move --to azure --dry-run   # print the plan
python3 tools/cloudmigrate/cloudmigrate.py move --to azure
```

1. Change `secret.tfvars` for the new host first (for example fill in
   `azure_subscription_id`, or `vps_ipv4`). Cloud Run and Container Apps need
   a media bucket: a site whose media is still on the VPS volume
   (`media_storage = "local"`) is refused until the files are in a bucket
   (`media copy vps gcs`, see below).
2. `move` deploys the new host, smoke-tests it, then points the domain at it.
   The old host keeps running untouched, so going back is one deploy.
3. When the site works on the new host, remove the old one (the tool prints
   the command).

`--stop-before-dns` deploys and tests the new host without touching DNS; run
`ansible-playbook deploy.yml -e target=<new>` later to switch.

### With the database

```sh
# Neon -> PostgreSQL on the VPS (set vps_local_db = true and vps_db_password first)
python3 tools/cloudmigrate/cloudmigrate.py move --to vps --db-to vps

# and back: the VPS database -> Neon, site on Cloud Run (set vps_local_db = false)
python3 tools/cloudmigrate/cloudmigrate.py move --to gcp --db-from vps --db-to neon
```

The steps, all printed before anything happens:

1. On a VPS target, start only the database container.
2. **Freeze** the old database: `default_transaction_read_only` is turned on and
   open sessions are ended. The old site keeps serving pages; edits, sign-ups
   and email sending wait. (`--no-freeze` skips this; writes made after the
   dump are then lost.)
3. **Copy**: `pg_dump` the old database (kept in `backups/`), back up the target
   if it has tables, restore in one transaction, then **compare** every table's
   row count and checksum, and every sequence. Any difference stops the move.
4. Deploy the new host against the copy and switch DNS.

If a step fails, the error says where the domain points now:

- Usually that is still the old host. The old database stays frozen until you
  run `db unfreeze` (the error says so), so nothing is written to a database
  you are leaving.
- If the deploy failed after the domain was already switched (the Azure
  certificate step, say), the new host is live on the copied data. Leave the
  old database frozen, fix the error, and finish with the `ansible-playbook`
  command the error prints. Do not run `move` again: it would copy the old
  data over the new.

Two checks run before anything starts. Leaving a VPS whose database runs in
its container (`vps_local_db = true`) needs `--db-from vps --db-to neon`, so
the live data comes along. And a move that keeps using Neon stops if Neon is
read-only, which means an earlier move left it behind and the live data is
elsewhere.

Neon's pooled host (`-pooler` in the name) is swapped for the direct host
automatically, because `pg_dump` and the API's migrations need session
features the pooler lacks.

## Database commands

```sh
python3 tools/cloudmigrate/cloudmigrate.py db info neon             # version, migration, row counts
python3 tools/cloudmigrate/cloudmigrate.py db dump vps              # backup to backups/
python3 tools/cloudmigrate/cloudmigrate.py db restore FILE local --replace
python3 tools/cloudmigrate/cloudmigrate.py db copy neon vps --freeze
python3 tools/cloudmigrate/cloudmigrate.py db verify neon vps       # row counts and checksums
python3 tools/cloudmigrate/cloudmigrate.py db freeze neon           # read-only
python3 tools/cloudmigrate/cloudmigrate.py db unfreeze neon
```

The tool never creates or drops databases: create the Neon database in the
Neon console, and the VPS container creates its own. A restore replaces the
`public` schema inside one transaction, so a failed restore changes nothing.

## Media commands

```sh
python3 tools/cloudmigrate/cloudmigrate.py media copy frontend/public/media gcs   # WordPress images -> bucket
python3 tools/cloudmigrate/cloudmigrate.py media ls gcs --long
python3 tools/cloudmigrate/cloudmigrate.py media copy gcs azure                   # bucket -> another cloud
python3 tools/cloudmigrate/cloudmigrate.py media verify gcs azure
```

`copy` never deletes anything at the target. A copy from a bucket or volume
first downloads everything to `backups/media-...` (kept as a backup), uploads
it, then checks that every file is at the target with the same size and MD5.
Bucket access uses your own `gcloud` or `az` sign-in, not the site's key.

### Moving media to another bucket or cloud

1. Add the new bucket's settings to `secret.tfvars` (for example
   `media_azure_account`) while `media_storage` still names the old one.
2. Create the new bucket: `terraform -chdir=infra/terraform/media-azure init`
   and `apply -var-file=../secret.tfvars`.
3. `media copy gcs azure`.
4. Set `media_storage = "azure"` and deploy. Files uploaded between steps 3 and
   4 are caught by running `media copy gcs azure` once more.

The old bucket is kept (Terraform refuses to delete it). Remove it by hand once
you no longer need it.

### Keys when the site and the bucket are in different clouds

A site in the bucket's own cloud (Cloud Run with a Google bucket, Container
Apps with an Azure container) uses its managed identity and needs no key.
Anything else (a VPS, Azure with a Google bucket, Cloud Run with an Azure
container) needs `media_access_key = true`, which creates a key limited to that
one bucket. `move` stops early with a message if the key is missing. Keep
it on while both hosts run; after moving back into the bucket's cloud, set it
to `false` and deploy to delete the key.

## Deployments made before the `dns/` root

Earlier versions kept the Cloudflare records in the `gcp/` or `azure/`
Terraform state; the deploy playbook now stops when it finds them there.

```sh
python3 tools/cloudmigrate/cloudmigrate.py dns adopt gcp   # or azure
```

moves them into the `dns/` state without changing anything in Cloudflare. It
saves both states to `backups/` first, re-keys the site records the way
`dns/` expects them, and refuses if `dns/` already holds records.

## Tests

```sh
python3 -m unittest discover -s tools/cloudmigrate     # unit tests, no Docker needed (Terraform optional)
tools/cloudmigrate/integration_test.sh                 # real PostgreSQL 18 servers and a media volume
```

The integration test starts throwaway containers: a "Neon" with the app's
migrations and sample data, the VPS's real `compose.yml` database reached
through a stand-in for `ssh`, an empty server, and an api-like container with a
media volume. It copies data back and forth, checks that writes are refused
while frozen, and that a changed row is caught. CI runs both.
