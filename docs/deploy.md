# Deploying to cadentic.dev

Three targets share the same images, the same `secret.tfvars` and the same
Ansible playbook. Pick one; [tools/cloudmigrate](../tools/cloudmigrate/README.md)
moves the site (and its database and media) from one to another later.

| | Google Cloud Run (`target=gcp`) | Azure Container Apps (`target=azure`) | Any VPS (`target=vps`) |
| --- | --- | --- | --- |
| Web | Public service, scales to zero | External ingress, 1–4 replicas | Caddy in front, HTTPS by Let's Encrypt |
| API | IAM-only; web calls it with an identity token | Internal ingress only | Container network only |
| Worker | Always-on instance, CPU not throttled | 1 replica, no ingress | 1 container |
| Database | Neon | Neon | Neon, or PostgreSQL 18 on the server (`vps_local_db`) |
| Media | Private bucket (GCS or Azure) | Private bucket (Azure or GCS) | Bucket, or a volume on the server |
| Secrets | Secret Manager | Container App secrets | `.env` on the server (mode 0600) |
| Images | Artifact Registry | Azure Container Registry | Built here, copied over SSH |
| Domain | Cloud Run domain mapping + Google-managed cert | Custom domain + free managed cert | Caddy certificate |

DNS stays on Cloudflare for every target, as DNS-only records: the managed
certificates are validated over plain DNS, so turning on Cloudflare's proxy
would stop them renewing. The `dns/` Terraform root owns every record and
points the site records at whichever target was deployed last; the mail
records (SPF, DKIM, DMARC) never change.

## 1. One-time setup (by hand)

1. **Neon**: create a project (PostgreSQL 18) and a database named `portfolio` in the Neon
   console. Copy the direct connection string: in Connect, turn "Connection
   pooling" off so the host has no `-pooler` (the API's start-up migrations use
   session locks, which the pooler does not support). It ends in `sslmode=require`. The API creates its tables on first start; Terraform and
   Ansible never create databases.
2. **Firebase**: create a project, enable Authentication with the Google
   provider, add `cadentic.dev` to Authentication → Settings → Authorized
   domains, and register a Web app to get the API key and auth domain.
3. **Brevo** (primary) and **Mailjet** (secondary): create API keys, and add
   `cadentic.dev` as a sending domain in both. Copy the DKIM and verification
   records they show into `email_dns_records` in `secret.tfvars`.
4. **Cloudflare**: create an API token with Zone → DNS → Edit for
   `cadentic.dev` and note the zone id.
5. **Where the site runs**:
   - GCP: create a project with billing, then `gcloud auth login` and
     `gcloud auth application-default login`. Verify `cadentic.dev` in Google
     Search Console with the same account (domain mappings require it).
   - Azure: `az login`, and `az extension add --name containerapp`.
   - VPS: a server with a public IPv4 address and root SSH (or a user with
     passwordless sudo), running Ubuntu 22.04+, Debian 12+ or AlmaLinux/Rocky 9+
     without a control panel; cPanel's web server would hold ports 80 and 443. A Bluehost
     **VPS or dedicated** plan works; Bluehost shared hosting cannot run Docker.
     Set up key login (`ssh-copy-id root@<ip>`). The playbook installs Docker.
6. **Media bucket** (optional on a VPS, needed on Cloud Run and Azure): see
   [Media](#media) below. The bucket may sit in another Google Cloud project or
   Azure subscription than the site.
7. Install Docker (with buildx), Terraform 1.9+ and `ansible-core` 2.15 or
   newer (`pipx install 'ansible-core>=2.15'`) on the machine you deploy from. On Windows, use WSL and keep the clone inside the
   WSL home directory (not under `/mnt/c`), where file permissions and line
   endings behave.

## 2. Secrets

```sh
cp infra/terraform/secret.tfvars.example infra/terraform/secret.tfvars
```

Fill in every value. The file is git-ignored. It is the only place secrets
live: Terraform copies them into Secret Manager, Container App secrets or the
server's `.env`, and the API reads them as environment variables. Provider keys
never reach the browser, API responses, logs or git; the admin console only
stores the *names* of the env vars that hold them (`EMAIL_BREVO_API_KEY` and so
on).

Terraform state also contains these values. Before the first real deploy,
uncomment the `backend` block in each root you use (`infra/terraform/<target>/`,
`dns/` and `media-gcs/` or `media-azure/`) and point it at a private, encrypted
bucket or storage account.

`infra/terraform/shared-variables.tf` is copied into every root. Edit the top
copy and run `scripts/sync-terraform-vars.sh`; CI checks the copies match.

## 3. Deploy

```sh
cd infra/ansible
ansible-playbook deploy.yml -e target=gcp      # or target=azure, or target=vps
```

The playbook:

1. creates or updates the media bucket named by `media_storage`, if any,
2. builds `api` and `web`, tagged with the git commit (a build with
   uncommitted changes, allowed by `-e allow_dirty=true`, gets a `-dirty-<time>` tag),
3. on Cloud Run and Azure: pushes the images and applies Terraform;
   on a VPS: installs Docker if needed, copies the images, `compose.yml`,
   `Caddyfile` and `.env` to `vps_dir` (default `/opt/portfolio`) and starts the stack,
4. smoke-tests the service,
5. points the domain at it (`dns/` root) and, on Azure, issues the managed
   certificates; on a VPS it restarts Caddy so the certificate is requested
   straight away. `-e switch_dns=false` skips this step.

Run it again for every release; unchanged infrastructure is left alone.
The first certificate can take 15–60 minutes after DNS is in place. Until then
the summary says the domain is "not reachable yet".

On a VPS: `ssh root@<ip> 'cd /opt/portfolio && docker compose ps'` shows the
stack, and `docker compose logs -f api` its logs.

Cloud Run and Container Apps refuse `media_storage = "local"` (the playbook and
Terraform both stop), because their containers lose written files.

### Deployments made before the `dns/` root

Earlier versions kept the Cloudflare records in the `gcp/` or `azure/` state.
The playbook now stops when it finds them there, because applying that state
would delete the records. Move them into `dns/` once; Cloudflare is not
touched, and both states are backed up to `backups/` first:

```sh
python3 tools/cloudmigrate/cloudmigrate.py dns adopt gcp   # or azure
```

## 4. After the first deploy

1. Sign in at `https://cadentic.dev/admin` with an account listed in
   `admin_emails`.
2. **Webhooks.** In Brevo (Transactional → Settings → Webhooks) add
   `https://webhook:<email_brevo_webhook_secret>@cadentic.dev/api/webhooks/brevo`
   for delivered, soft/hard bounce, blocked, spam, invalid, unsubscribed,
   opened and clicked. In Mailjet (Account → Event notifications) add
   `https://webhook:<email_mailjet_webhook_secret>@cadentic.dev/api/webhooks/mailjet`
   for sent, bounce, blocked, spam, unsub, open and click.
3. **Test each provider** from Subscribers & Email → Providers → Test send.
   Check the received message's headers for `spf=pass`, `dkim=pass` and
   `dmarc=pass`.
4. **Email settings**: set the postal address shown in newsletter footers.
5. **DMARC**: start with `dmarc_policy = "quarantine"` (or `none`) and a
   `dmarc_rua` mailbox. After a few weeks of clean reports, switch to `reject`
   and redeploy.
6. **Media**: copy the WordPress images into the bucket (next section).

## Site text

The home page, profile, organisations, videos, FAQ, SEO text and the legal
pages are edited in the admin under Site content (Home page, Site profile,
Videos, Legal pages). Changes show on the site within a few seconds, with no
deploy. After the first deploy that includes them:

1. **Resume**: upload the PDF in Site profile → Resume. Until a resume exists
   (or `RESUME_FILE_PATH` is set) the resume buttons are hidden, but the FAQ
   answer to "How can I get his resume?" still tells visitors and search
   engines to use the button. Edit or remove that answer in Home page → FAQ if
   no resume will be uploaded, and keep it in step if you rename the button.
2. **Featured video**: set the real title and upload date of the YouTube
   video in Videos.
3. **AI policy**: its "Content management" bullet says what the AI assistant
   connected over MCP can do: edit pages, articles and site text, with every
   change in the audit log. If that changes, update the bullet in Legal pages.

## Media

Uploads and the imported WordPress images live in one private place, set by
`media_storage` in `secret.tfvars`:

| `media_storage` | Where | Created by |
| --- | --- | --- |
| `local` | The `media` volume on the VPS. Cloud Run and Container Apps discard local files, so use a bucket there. | the VPS stack |
| `gcs` | A Google Cloud Storage bucket, `media_gcs_bucket`, in `media_gcs_project_id` (or the site's project) | `infra/terraform/media-gcs` |
| `azure` | An Azure Blob container, `media_azure_account`/`media_azure_container`, in `media_azure_subscription_id` (or the site's subscription, same Entra tenant) | `infra/terraform/media-azure` |

Visitors never reach the bucket. The site serves files at `/media/<key>`
through the API, so the bucket stays private:

- **Nothing is public.** Google: public access prevention enforced, uniform
  bucket-level access (no per-object ACLs). Azure: anonymous blob access off,
  private container, HTTPS and TLS 1.2+ only, no cross-tenant replication.
- **Only the site's API can use it, and only this bucket.** On Cloud Run, the
  API's service account gets `roles/storage.objectUser` on the bucket alone; on
  Azure, the API's own managed identity gets Storage Blob Data Contributor on
  the container alone. Neither has any project- or subscription-wide storage
  role, and the web container has no access at all.
- **A site outside the bucket's cloud** (a VPS, or Azure with a Google bucket
  and the other way round) needs `media_access_key = true`. Google: a
  dedicated service account whose only permission is object access on this
  bucket, and a key for it. Azure: a SAS token limited to this container (no
  account-wide SAS), renewed every 6 months on deploy and valid for 19. Without
  `media_access_key`, the Azure account keys are switched off entirely. The key
  goes into the site's secrets, never into git. Organisations that block
  service-account keys (`iam.disableServiceAccountKeyCreation`) need an
  exception for the media project, or an Azure container for a VPS.
- **Deletes can be undone for 7 days** (soft delete), and Terraform refuses to
  delete the bucket or storage account (`prevent_destroy`).
- On Azure, whoever runs Terraform also gets Storage Blob Data Contributor on
  the container, so `az --auth-mode login` and the migration tool can copy files.

After the bucket exists (first deploy with `media_storage` set), copy the
WordPress images into it once:

```sh
python3 tools/cloudmigrate/cloudmigrate.py media copy frontend/public/media gcs   # or azure, or vps
```

## Moving to another host

`tools/cloudmigrate` deploys the new host, optionally copies the database
(Neon ↔ the VPS container) with the old one read-only during the copy, checks
every table, and switches DNS last. See
[tools/cloudmigrate/README.md](../tools/cloudmigrate/README.md).

## Email authentication

Terraform creates:

- **SPF** on `cadentic.dev`: `v=spf1 include:spf.brevo.com include:spf.mailjet.com ~all`.
  If you add other senders (Google Workspace, say), add their include to the
  `spf` record in `infra/terraform/modules/cloudflare-dns/main.tf`; a domain
  may have only one SPF record.
- **DKIM**: whatever you list in `email_dns_records` (Brevo uses two CNAMEs,
  Mailjet a TXT record). Both providers must show the domain as authenticated
  before mail is signed with `d=cadentic.dev`.
- **DMARC** on `_dmarc.cadentic.dev` with relaxed alignment.

An API key alone does not authenticate the domain or guarantee inbox
delivery; the records above, a warmed-up sending reputation and low bounce and
complaint rates do.

## Rolling back

Re-run the playbook from an older commit (`git checkout <sha>` first), or in
the cloud console route traffic back to the previous revision. Roll back `api`
and `web` together, `web` first: the web app takes the site text from the API
(`/api/public/site-content`), which older APIs do not serve, so a newer web app
in front of an older API fails every page (503) once its cached copy has gone.

Most migrations only add tables and columns, so an older image runs against a
newer schema. Check each migration you roll back past for anything more:

- **0007** turns the privacy notice, terms and AI policy into CMS pages. An API
  older than 0007 lists those three paths in the sitemap itself as well, so
  each appears twice, and its page editor refuses to save them ("reserved"
  slug). The pages still display. Roll forward before editing them. The same
  mix exists for a few minutes during the deploy that adds 0007, while the old
  API revision still serves.
- **0008** deletes articles left behind by old end-to-end test runs. Rolling
  back does not bring them back.
