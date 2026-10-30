# Configuration

Local runs get these from `docker-compose.yml` (override with `.env`); cloud
runs get them from Terraform, which reads `infra/terraform/secret.tfvars`.
Everything else (sender identity, consent text, retry policy, provider order)
is edited in the console under Subscribers & Email → Settings and Providers.

## API and worker (`backend`)

| Variable | Required | Default | Meaning |
| --- | --- | --- | --- |
| `DATABASE_URL` | yes | | Postgres connection string (Neon in production) |
| `APP_SECRET` | yes | | 32+ characters; signs confirmation and unsubscribe tokens |
| `FIREBASE_PROJECT_ID` | yes | | Firebase project whose ID tokens are accepted |
| `ADMIN_EMAILS`, `ADMIN_UIDS` | one of them | | Comma-separated owner accounts allowed into the admin API and MCP |
| `PUBLIC_SITE_URL` | | `http://localhost:4200` | Used in links, sitemap, OAuth metadata. Must be https in production |
| `CORS_ALLOWED_ORIGINS` | | `http://localhost:4200` | Browser origins allowed to call the API directly |
| `APP_ENV` | | `development` | `production` turns on the strict checks below |
| `HTTP_ADDR` | | `:8080` | Listen address |
| `RUN_WORKER` | | `true` | Run the queue worker in this process |
| `WORKER_ID` | | hostname | Shown in attempt logs and leases |
| `DB_MAX_CONNS` | | `10` | Connection pool size |
| `EMAIL_FROM_ADDRESS` | | | Default sender until one is set in Email settings |
| `ADMIN_ALERT_EMAIL` | | | Private address for delivery alerts |
| `EMAIL_BREVO_API_KEY` | for Brevo | | Referenced by the Brevo provider's credential settings |
| `EMAIL_BREVO_WEBHOOK_SECRET` | for Brevo | | Password for the Brevo webhook URL |
| `EMAIL_MAILJET_API_KEY`, `EMAIL_MAILJET_SECRET_KEY` | for Mailjet | | Mailjet API key pair |
| `EMAIL_MAILJET_WEBHOOK_SECRET` | for Mailjet | | Password for the Mailjet webhook URL |
| `EMAIL_*` (any) | | | Providers added in the console may reference any variable matching `EMAIL_[A-Z0-9_]{2,64}` |
| `RESUME_FILE_PATH` | | | Fallback PDF served after email verification. The owner normally uploads the resume in Admin → Site profile → Resume; that upload is kept in private media storage and takes precedence. With neither, the resume buttons are hidden |
| `MEDIA_STORAGE` | | `local` | Where admin uploads are kept: `local`, `gcs` or `azure`. Buckets stay private; files are served only through `GET /media/<key>` |
| `MEDIA_LOCAL_DIR` | | `/data/media` | Directory for `local` storage (a Docker volume locally) |
| `MEDIA_MAX_UPLOAD_MB` | | `30` | Largest accepted upload. Keep it under 32 on Cloud Run, whose HTTP/1 request limit is 32 MiB |
| `MEDIA_GCS_BUCKET` | for `gcs` | | Bucket name. The bucket may live in another Google Cloud project |
| `MEDIA_GCS_CREDENTIALS` | | | Service-account key JSON (one line), for hosts outside Google Cloud. Empty uses the Cloud Run service account via the metadata server |
| `MEDIA_AZURE_ACCOUNT`, `MEDIA_AZURE_CONTAINER` | for `azure` | | Storage account and container. The account may live in another subscription |
| `MEDIA_AZURE_SAS` | | | Container SAS token without the leading `?`, for hosts outside Azure. Empty uses the managed identity (`IDENTITY_ENDPOINT`, `IDENTITY_HEADER`, and `AZURE_CLIENT_ID` for a user-assigned identity) |
| `GOOGLE_CONTACTS_ENABLED` | | `false` | Turns on the optional Google Contacts list (admin tab, API, worker sync). Terraform: `google_contacts_enabled`; `installSetup.py --google-contacts` |
| `GOOGLE_OAUTH_CLIENT_ID`, `GOOGLE_OAUTH_CLIENT_SECRET` | when `GOOGLE_CONTACTS_ENABLED=true` | | OAuth web client for the Google Contacts list. Setup: [google-contacts.md](google-contacts.md) |
| `GTM_ID`, `GA4_MEASUREMENT_ID` | | | Analytics, loaded only after visitor consent |
| `BLOG_ENABLED` | | `true` | Hide the blog section |
| `TRUST_CLIENT_IP_HEADER` | | `false` | Take the visitor IP from `X-Client-IP` set by the web server. Only when the API is reachable solely through it |
| `TRUSTED_PROXY_HOPS` | | `0` | Otherwise, how many proxies in `X-Forwarded-For` to trust |
| `TRUST_CLOUDFLARE_IP` | | `false` | Trust `CF-Connecting-IP` (only if the origin accepts Cloudflare traffic alone) |
| `SHUTDOWN_TIMEOUT_SECONDS` | | `25` | Grace period for in-flight sends on shutdown |
| `FIREBASE_AUTH_EMULATOR_HOST` | local only | | Accept Auth emulator tokens. Rejected when `APP_ENV=production` |
| `EMAIL_BREVO_BASE_URL`, `EMAIL_MAILJET_BASE_URL` | local only | | Point the adapters at the mock server. Rejected in production |

## Web (`frontend`)

| Variable | Default | Meaning |
| --- | --- | --- |
| `API_INTERNAL_URL` | `http://localhost:8080` | Where `/api`, `/mcp`, `/oauth`, the SEO files and `/media/` are proxied (`/media/` files baked into the image are served first) |
| `SSR_API_ORIGIN` | `API_INTERNAL_URL` | Origin used by server-side rendering for API calls. On Cloud Run it is the web server itself, so calls carry the identity token |
| `API_ID_TOKEN_AUDIENCE` | | Cloud Run only: request a Google identity token for this audience and send it as `X-Serverless-Authorization` |
| `PUBLIC_SITE_URL` | `https://cadentic.dev` | Canonical URLs and Open Graph |
| `PORT` | `4000` | Listen port |
| `NODE_ENV` | | `production` adds HSTS and `upgrade-insecure-requests` |
| `TRUSTED_PROXY_HOPS` | `0` | Load balancers in front of the web server (1 on Cloud Run and Container Apps) |
| `TRUST_CLOUDFLARE_IP` | `false` | Use `CF-Connecting-IP` as the visitor IP |
| `FIREBASE_API_KEY`, `FIREBASE_AUTH_DOMAIN`, `FIREBASE_PROJECT_ID` | | Web config for the owner sign-in page (public values) |
| `FIREBASE_AUTH_EMULATOR_URL` | | Local only: send the sign-in page to the Auth emulator |
