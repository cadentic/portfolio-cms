# Portfolio CMS (cadentic.dev)

Personal site and CMS for Sayantan Chakraborty: a server-rendered Angular 22
site, an owner console for pages, articles, subscribers and newsletters, and a
Go API with a durable email queue that fails over from Brevo to Mailjet.

| Part | Where | What it does |
| --- | --- | --- |
| Web | `frontend/` | Angular 22 with SSR. Public pages render on the server with SEO, Open Graph and JSON-LD. `/admin` is the owner console (client-rendered). The Node server also proxies `/api`, `/mcp` and `/oauth` to the API and sends a nonce-based CSP. |
| API and worker | `backend/` | Go 1.25 + Postgres (Neon in production). Subscribers with double opt-in, consent records (DPDP), campaigns, templates, CMS content, MCP server with OAuth login, webhooks, and the email queue worker. |
| Local stack | `docker-compose.yml` | Postgres, API, worker, web, a mock Brevo/Mailjet server and the Firebase Auth emulator. |
| Deploy | `infra/` | Terraform for Google Cloud Run or Azure Container Apps, Cloudflare DNS (site, SPF, DKIM, DMARC), and an Ansible playbook that builds, pushes and applies. Secrets live in `infra/terraform/secret.tfvars`. |
| Browser tests | `e2e/` | Playwright + axe: accessibility, phone layout and CMS usability. |

## Run it locally

You need Docker. Nothing else, and no cloud accounts.

```sh
docker compose up --build
```

| URL | |
| --- | --- |
| http://localhost:4000 | The site |
| http://localhost:4000/admin | Owner console. Sign in with `owner@cadentic.dev` / `localpass123` (change both in `.env`, see `.env.example`). |
| http://localhost:8025 | Mock email providers: every "sent" email, buttons to fire delivery/bounce/complaint webhooks, and failure modes per provider. |

Try the failover: open http://localhost:8025, set Brevo to `error500`,
subscribe on the homepage, and watch the confirmation arrive through Mailjet.
The Delivery monitor in the console shows the attempts and the failover reason.
`unknown` mode makes Brevo store the email and then time out, which is the case
where the queue must not send a duplicate.

Uploads (Admin → Media, or the Upload buttons in the page editor) are kept
in the `media` Docker volume locally and in a private Google Cloud Storage
bucket or Azure Blob container in the cloud (`MEDIA_STORAGE`, see
[docs/configuration.md](docs/configuration.md)). Either way they are served
from `/media/…` on the site's own domain.

To add your resume download, mount the PDF into the `api` service and set
`RESUME_FILE_PATH` (see `.env.example`).

## Tests

```sh
# Go: unit tests plus the acceptance suite (criteria 1–14) against a real Postgres
cd backend && TEST_DATABASE_URL=postgres://postgres@localhost:5432/postgres?sslmode=disable go test ./...

# Browser: criterion 15 (accessibility, mobile, CMS usability), against `docker compose up`
cd e2e && npm install && npx playwright install chromium && npx playwright test
```

The acceptance tests create a throwaway database per test, run the real Brevo
and Mailjet adapters against the mock server, and drive the worker
deterministically.

| # | Criterion | Test |
| --- | --- | --- |
| 1 | Delivery through Brevo | `TestAC01_DeliveryThroughBrevo` |
| 2 | Brevo failure, then Mailjet succeeds | `TestAC02_BrevoFailureFailsOverToMailjet` |
| 3 | Both fail: stays queued, delivered later; permanent failures dead-letter and replay | `TestAC03_*` |
| 4 | Rate limits and transient errors back off (Retry-After honoured) | `TestAC04_*`, `TestBackoffExponentialWithJitter` |
| 5 | Unknown acceptance never sends a duplicate; enqueue is idempotent | `TestAC05_UnknownAcceptanceNoDuplicate`, `TestAC05b_*` |
| 6 | Concurrent workers never double-send jobs or campaigns | `TestAC06_*`, `TestAC06b_*` |
| 7 | Webhooks authenticated, idempotent, safe out of order | `TestAC07_*` |
| 8 | Double opt-in, expired tokens, duplicate signups | `TestAC08_DoubleOptIn` |
| 9 | Unsubscribe from every marketing email, enforced | `TestAC09_*` |
| 10 | Hard bounces and complaints suppress | `TestAC10_*` |
| 11 | Only the owner reaches admin APIs; provider keys never exposed | `TestAC11_*` |
| 12 | Article publish creates a campaign that needs approval, no duplicates | `TestAC12_*` |
| 13 | Provider order, enablement and credentials change without redeploy | `TestAC13_*` |
| 14 | Recovery after a crash mid-send | `TestAC14_*` |
| 15 | Accessibility, mobile, CMS usability | `e2e/tests/*.spec.ts` |

Also covered: MCP OAuth login and page creation, SQL injection attempts on
the search, filter and sort parameters, CSV import/export, retention purges.

## Deploy

See [docs/deploy.md](docs/deploy.md). In short: create the Neon database and
Firebase project yourself, copy `infra/terraform/secret.tfvars.example` to
`secret.tfvars`, fill it in, then

```sh
cd infra/ansible
ansible-playbook deploy.yml -e target=gcp     # or target=azure
```

## Connect an AI assistant (MCP)

In the console, open **MCP connector** and copy the URL
(`https://cadentic.dev/mcp`). In Claude: Settings → Connectors → Add custom
connector, paste it, and sign in with your owner account when asked. The
assistant can then list, create, update, publish and unpublish pages and
articles (with image, video or YouTube heroes), and read subscriber and
delivery stats. It cannot send email or see subscriber addresses.

## More

- [docs/deploy.md](docs/deploy.md): cloud setup, DNS, email authentication (SPF, DKIM, DMARC), webhooks
- [docs/operations.md](docs/operations.md): how delivery and failover work, alerts, replaying dead letters, resolving unknown acceptance, data retention
- [docs/configuration.md](docs/configuration.md): every environment variable
