# Deploying to cadentic.dev

Two targets share the same images, the same `secret.tfvars` and the same
Ansible playbook. Pick one.

| | Google Cloud Run (`target=gcp`) | Azure Container Apps (`target=azure`) |
| --- | --- | --- |
| Web | Public service, scales to zero | External ingress, 1–4 replicas |
| API | IAM-only; web calls it with an identity token | Internal ingress only |
| Worker | Always-on instance, CPU not throttled | 1 replica, no ingress |
| Secrets | Secret Manager | Container App secrets |
| Images | Artifact Registry | Azure Container Registry |
| Domain | Cloud Run domain mapping + Google-managed cert | Custom domain + free managed cert |

The database is Neon Postgres for both. DNS is Cloudflare for both, as
DNS-only records: the managed certificates on both clouds are validated over
plain DNS, so turning on Cloudflare's proxy would stop them renewing.

## 1. One-time setup (by hand)

1. **Neon**: create a project and a database named `portfolio` in the Neon
   console. Copy the pooled connection string (it ends in
   `sslmode=require`). The API creates its tables on first start; Terraform and
   Ansible never create databases.
2. **Firebase**: create a project, enable Authentication with the Google
   provider, add `cadentic.dev` to Authentication → Settings → Authorized
   domains, and register a Web app to get the API key and auth domain.
3. **Brevo** (primary) and **Mailjet** (secondary): create API keys, and add
   `cadentic.dev` as a sending domain in both. Copy the DKIM and verification
   records they show into `email_dns_records` in `secret.tfvars`.
4. **Cloudflare**: create an API token with Zone → DNS → Edit for
   `cadentic.dev` and note the zone id.
5. **Cloud**:
   - GCP: create a project with billing, then `gcloud auth login` and
     `gcloud auth application-default login`. Verify `cadentic.dev` in Google
     Search Console with the same account (domain mappings require it).
   - Azure: `az login`, and `az extension add --name containerapp`.
6. Install Docker (with buildx), Terraform 1.9+ and `ansible-core`.

## 2. Secrets

```sh
cp infra/terraform/secret.tfvars.example infra/terraform/secret.tfvars
```

Fill in every value. The file is git-ignored. It is the only place secrets
live: Terraform copies them into Secret Manager or Container App secrets, and
the API reads them as environment variables. Provider keys never reach the
browser, API responses, logs or git; the admin console only stores the *names*
of the env vars that hold them (`EMAIL_BREVO_API_KEY` and so on).

Terraform state also contains these values. Before the first real deploy,
uncomment the `backend` block in `infra/terraform/<target>/main.tf` and point
it at a private, encrypted bucket or storage account.

## 3. Deploy

```sh
cd infra/ansible
ansible-playbook deploy.yml -e target=gcp      # or target=azure
```

The playbook:

1. creates the image registry,
2. builds `api` and `web` for linux/amd64, tagged with the git commit, and pushes them,
3. applies Terraform (services, secrets, DNS, SPF/DKIM/DMARC),
4. on Azure, issues the free managed certificates for `cadentic.dev` and `www`,
5. smoke-tests the service URL and the domain.

It refuses to deploy uncommitted changes (`-e allow_dirty=true` overrides).
Run it again for every release; unchanged infrastructure is left alone.

The first certificate can take 15–60 minutes after DNS is in place. Until then
the summary says the domain is "not reachable yet".

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
the cloud console route traffic back to the previous revision. The
migrations so far only add tables and columns, so an older image runs fine
against a newer schema; check this before rolling back past a migration that
changes or drops something.
