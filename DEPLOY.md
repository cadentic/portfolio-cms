# Quick deployment guide

The short version. Full details, including webhooks, DMARC and rollback, are in
[docs/deploy.md](docs/deploy.md).

## 0. Test locally first

```sh
git clone https://github.com/cadentic/portfolio-cms.git
cd portfolio-cms
docker compose up --build
```

Open http://localhost:4000 and http://localhost:4000/admin (`owner@cadentic.dev` / `localpass123`).
If a port is already taken, copy `.env.example` to `.env` and change `AUTH_PORT`, `DB_PORT`,
`MOCK_PORT` or `API_PORT`.

## 1. Accounts to prepare (once)

| Service | What you need |
| --- | --- |
| **Neon** | A project on **PostgreSQL 18** with a database named `portfolio`. Copy the *direct* connection string: in Connect, turn "Connection pooling" off so the host has no `-pooler`. Do not create tables: the API runs the migrations on first start, including the imported WordPress drafts. |
| **Firebase** | A project with Authentication → Google sign-in enabled, the site's hostname (`cadentic.dev`, or the subdomain such as `profile.cadentic.dev`) added to Authorized domains, and a Web app (gives the API key and auth domain). |
| **Brevo** and **Mailjet** | An API key from each, and `cadentic.dev` added as a sending domain in both (they show the DKIM records to copy). |
| **Cloudflare** | The `cadentic.dev` zone id and an API token with Zone → DNS → Edit. |
| **Where it runs** | One of: Google Cloud (project with billing; `gcloud auth login` and `gcloud auth application-default login`; verify `cadentic.dev` in Search Console), Azure (`az login`; `az extension add --name containerapp`), or a VPS with root SSH key login (a Bluehost VPS or dedicated server with plain Ubuntu 22.04+, Debian 12+ or AlmaLinux/Rocky 9+, no cPanel; Bluehost shared hosting cannot run Docker). |
| **Media bucket** | Needed on Google Cloud and Azure, optional on a VPS: a private Google Cloud Storage bucket or Azure Blob container, which may sit in another project or subscription. Terraform creates it; you only choose the names. |

Tools on the machine you deploy from: Docker with buildx, Terraform 1.9+, `ansible-core` 2.15 or
newer (`pipx install 'ansible-core>=2.15'`), git, and `gcloud` or `az`. **On Windows, run the deploy from WSL (Ubuntu)**, because Ansible does not run
natively on Windows. Docker Desktop's WSL integration gives WSL the `docker` command. Clone the
repository inside WSL (`cd ~ && git clone ...`), not under `/mnt/c`: Ansible ignores `ansible.cfg`
in a Windows folder.

## 2. Fill in the secrets

The easy way is `installSetup.py`. It creates (or updates) `infra/terraform/secret.tfvars`, takes
any setting as a flag, asks for whatever is still missing, generates the random secrets, and can
run the deploy at the end:

```sh
python3 installSetup.py                                    # asks for everything it needs
python3 installSetup.py --target vps --site-host profile --vps-ipv4 203.0.113.10
python3 installSetup.py --target gcp --site-host profile --deploy
python3 installSetup.py --help                             # every flag
```

Secrets can also come from environment variables named `SETUP_<SETTING>` (for example
`SETUP_CLOUDFLARE_API_TOKEN`), which keeps them out of the shell history. `--dry-run` shows the
changes without writing; `--non-interactive` fails instead of asking. The previous file is kept as
`secret.backup-<time>.tfvars` (git-ignored). Writing the file works from PowerShell too; the deploy
itself needs WSL.

Or by hand:

```sh
cp infra/terraform/secret.tfvars.example infra/terraform/secret.tfvars
```

Edit `infra/terraform/secret.tfvars` (it is git-ignored) and set at least:

- `database_url`: the Neon direct connection string (no `-pooler` in the host)
- `app_secret`: any long random string, e.g. the output of `openssl rand -base64 48`
- `firebase_project_id`, `firebase_api_key`, `firebase_auth_domain`
- `admin_emails`: the Google account(s) allowed into `/admin`
- `email_brevo_api_key`, `email_mailjet_api_key`, `email_mailjet_secret_key`, plus the two webhook secrets (random strings)
- `email_dns_records`: the DKIM records from Brevo and Mailjet
- `cloudflare_api_token`, `cloudflare_zone_id`
- `gcp_project_id` / `gcp_region`, **or** `azure_subscription_id` / `azure_location` / `acr_name`,
  **or** `vps_ipv4` / `vps_ssh_user` (plus `vps_local_db = true` and `vps_db_password` to run
  PostgreSQL on the server instead of Neon)
- `site_host`: leave empty to serve the site on `cadentic.dev` (and www), or set a subdomain such
  as `profile.cadentic.dev` (see [Site on a subdomain](#site-on-a-subdomain))
- `media_storage` (`gcs` or `azure`) and the bucket name, `media_gcs_bucket` or
  `media_azure_account`. A site outside the bucket's cloud (a VPS, or Azure with a Google bucket)
  also needs `media_access_key = true`.
- Optional: `google_contacts_enabled = true` with `google_oauth_client_id` and
  `google_oauth_client_secret` turns on the Google Contacts list (off by default; or run
  `python3 installSetup.py --google-contacts`). See [docs/google-contacts.md](docs/google-contacts.md).

Before the first real deploy, also turn on the Terraform remote state `backend` block in each
root you use (`infra/terraform/<gcp|azure|vps>/`, `dns/`, `media-gcs/` or `media-azure/`),
because the state files contain these secrets.

## 3. Deploy

```sh
git status                      # must be clean: the playbook refuses uncommitted changes
cd infra/ansible
ansible-playbook deploy.yml -e target=gcp      # Google Cloud Run
# or
ansible-playbook deploy.yml -e target=azure    # Azure Container Apps
# or
ansible-playbook deploy.yml -e target=vps      # your VPS (Docker Compose behind Caddy)
```

This creates the media bucket, builds the images, deploys them (pushes to the cloud, or copies them
to the VPS over SSH), smoke-tests the site, then points the Cloudflare DNS records at it. The first
HTTPS certificate can take 15 to 60 minutes after DNS is in place on Google Cloud and Azure; on a VPS,
Caddy is restarted after the DNS change and usually has it within a minute.

## 4. After the first deploy

1. Sign in at https://cadentic.dev/admin (or https://profile.cadentic.dev/admin on a subdomain).
2. Add the Brevo and Mailjet webhooks (URLs in [docs/deploy.md](docs/deploy.md#4-after-the-first-deploy)).
3. Send a test email from each provider (Subscribers & Email → Providers → Test send).
4. Copy the WordPress images into the media bucket (once):
   `python3 tools/cloudmigrate/cloudmigrate.py media copy frontend/public/media gcs` (or `azure`, or `vps`).
5. Review the imported WordPress drafts under Pages in the admin and publish the ones you want.
   Fix or delete `/about` first: it is an old resume with personal contact details.

## Site on a subdomain

To serve the site on `profile.cadentic.dev` while the email records stay on `cadentic.dev`:

```sh
python3 installSetup.py --site-host profile           # sets site_host = "profile.cadentic.dev"
cd infra/ansible && ansible-playbook deploy.yml -e target=vps   # or gcp, or azure
```

- `domain` stays `cadentic.dev`: it is the Cloudflare zone and the sending domain. SPF, DMARC and
  the Brevo/Mailjet DKIM records stay on it and do not change.
- Only the site records move: a CNAME (Cloud Run, Azure, plus Azure's `asuid.profile` TXT) or an A
  record (VPS) on `profile.cadentic.dev`. Terraform removes the old site records on `cadentic.dev`
  and `www`, and leaves anything else on the apex alone.
- No `www.profile...` by default; `site_www = true` (or `--www yes`) adds it.
- Add `profile.cadentic.dev` to Firebase Authorized domains, and move the Brevo and Mailjet
  webhooks to `https://webhook:<secret>@profile.cadentic.dev/api/webhooks/...`.
- On Google Cloud, verifying `cadentic.dev` in Search Console covers the subdomain.
- Canonical and Open Graph URLs, the sitemap and links in emails all follow the new hostname.

Going back is the same with `--site-host @`.

## Updating

```sh
git pull
cd infra/ansible && ansible-playbook deploy.yml -e target=gcp   # or azure, or vps
```

New migrations run automatically when the new API starts.

If the playbook stops with "the gcp Terraform state still holds the Cloudflare records", the site was
deployed before the DNS records moved to their own Terraform root (`infra/terraform/dns`). Move them
once, without touching Cloudflare, then deploy again:

```sh
python3 tools/cloudmigrate/cloudmigrate.py dns adopt gcp      # or azure
```

## Moving to another host or database

```sh
python3 tools/cloudmigrate/cloudmigrate.py check                         # what is configured, where DNS points
python3 tools/cloudmigrate/cloudmigrate.py move --to vps --db-to vps --dry-run
```

`move` deploys the new host, copies the database if asked (the old one is read-only meanwhile, and
every table is compared afterwards) and switches DNS last. DNS stays on Cloudflare throughout.
Details: [tools/cloudmigrate/README.md](tools/cloudmigrate/README.md).
