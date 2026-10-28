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
| **Firebase** | A project with Authentication → Google sign-in enabled, `cadentic.dev` added to Authorized domains, and a Web app (gives the API key and auth domain). |
| **Brevo** and **Mailjet** | An API key from each, and `cadentic.dev` added as a sending domain in both (they show the DKIM records to copy). |
| **Cloudflare** | The `cadentic.dev` zone id and an API token with Zone → DNS → Edit. |
| **Where it runs** | One of: Google Cloud (project with billing; `gcloud auth login` and `gcloud auth application-default login`; verify `cadentic.dev` in Search Console), Azure (`az login`; `az extension add --name containerapp`), or a VPS with root SSH key login (a Bluehost VPS or dedicated server with plain Ubuntu or AlmaLinux, no cPanel; Bluehost shared hosting cannot run Docker). |
| **Media bucket** | Needed on Google Cloud and Azure, optional on a VPS: a private Google Cloud Storage bucket or Azure Blob container, which may sit in another project or subscription. Terraform creates it; you only choose the names. |

Tools on the machine you deploy from: Docker with buildx, Terraform 1.9+, `ansible-core`, git, and
`gcloud` or `az`. **On Windows, run the deploy from WSL (Ubuntu)**, because Ansible does not run
natively on Windows. Docker Desktop's WSL integration gives WSL the `docker` command. Clone the
repository inside WSL (`cd ~ && git clone ...`), not under `/mnt/c`: Ansible ignores `ansible.cfg`
in a Windows folder.

## 2. Fill in the secrets

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
- `media_storage` (`gcs` or `azure`) and the bucket name, `media_gcs_bucket` or
  `media_azure_account`. A site outside the bucket's cloud (a VPS, or Azure with a Google bucket)
  also needs `media_access_key = true`.

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
HTTPS certificate can take 15 to 60 minutes after DNS is in place.

## 4. After the first deploy

1. Sign in at https://cadentic.dev/admin.
2. Add the Brevo and Mailjet webhooks (URLs in [docs/deploy.md](docs/deploy.md#4-after-the-first-deploy)).
3. Send a test email from each provider (Subscribers & Email → Providers → Test send).
4. Copy the WordPress images into the media bucket (once):
   `python3 tools/cloudmigrate/cloudmigrate.py media copy frontend/public/media gcs` (or `azure`, or `vps`).
5. Review the imported WordPress drafts under Pages in the admin and publish the ones you want.
   Fix or delete `/about` first: it is an old resume with personal contact details.

## Updating

```sh
git pull
cd infra/ansible && ansible-playbook deploy.yml -e target=gcp   # or azure, or vps
```

New migrations run automatically when the new API starts.

## Moving to another host or database

```sh
python3 tools/cloudmigrate/cloudmigrate.py check                         # what is configured, where DNS points
python3 tools/cloudmigrate/cloudmigrate.py move --to vps --db-to vps --dry-run
```

`move` deploys the new host, copies the database if asked (the old one is read-only meanwhile, and
every table is compared afterwards) and switches DNS last. DNS stays on Cloudflare throughout.
Details: [tools/cloudmigrate/README.md](tools/cloudmigrate/README.md).
