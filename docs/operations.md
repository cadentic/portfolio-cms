# Operations

## How an email is delivered

1. The API writes a row to `email_jobs` with an idempotency key (for example
   `doi:<token id>` for a confirmation or `campaign:<id>:<recipient>`). Enqueuing
   the same key twice returns the existing job.
2. A worker claims due jobs with `FOR UPDATE SKIP LOCKED` and a lease, so two
   workers never hold the same job.
3. Before calling a provider, the worker records an attempt with no outcome.
   If the process dies mid-request, that open attempt is how the next worker
   knows the email may already have been accepted.
4. The provider's answer is classified:

| Answer | Meaning | What happens |
| --- | --- | --- |
| 2xx with a message id | accepted | Job `accepted`; webhooks later move it to `delivered`, `bounced` or `complained` |
| 429 | rate limited | Retry the same provider after `Retry-After` (or backoff); if that is longer than the retry cap, fail over now |
| 5xx, 408, connection refused | transient | Retry the same provider with exponential backoff and jitter, up to its attempt limit, then fail over |
| 401/403, quota exhausted | provider misconfigured | Fail over immediately; counts towards that provider's circuit breaker |
| 4xx about the message | permanent | Fail over once; if every provider rejects it, dead-letter |
| Timeout after the request was sent, 504 | **acceptance unknown** | Never resend straight away (see below) |

5. When every provider in the chain has been tried, the job waits for the
   cycle backoff and starts again from the top. After `max_cycles` it moves to
   `dead_letter` and an alert is raised.

Circuit breakers skip a provider after repeated failures and probe it again
after the cooldown. Provider order, enablement, credentials and limits are
changed in the console and take effect on the next job, without a redeploy.

## Acceptance unknown

If a provider may have accepted an email but did not say so, resending could
deliver it twice. The worker instead:

1. asks the provider whether it has the message (Brevo: events tagged
   `job:<id>`; Mailjet: messages with `CustomID=<id>`),
2. if found, records it as accepted,
3. if the provider confirms it has nothing after the grace period
   (`unknown_resend_grace_minutes`), resends through the same provider with
   the same idempotency key (Brevo honours it for 15 minutes),
4. otherwise keeps checking every `unknown_reconcile_seconds`, up to
   `unknown_max_reconciles`, then raises an alert for you to decide.

In the console (Delivery monitor → job) you can check again, mark it accepted
after checking the provider dashboard, or resend. Resend asks for explicit
confirmation because it may duplicate.

## Alerts

Raised in the console (bell icon) and emailed to `ADMIN_ALERT_EMAIL`:
every provider failing or none enabled, dead letters, emails in acceptance
unknown (and ones that need your decision), a provider circuit opening, and a
provider reporting its quota or credits exhausted. Bounce and complaint rates
are shown on the Delivery monitor.

## Dead letters

Delivery monitor → filter `dead_letter` → open a job to see every attempt and
the provider responses. Fix the cause (credentials, sender domain, a
suppressed address), then **Replay all dead letters** or **Retry** one job.
Replays are audited and rate limited. A job that a provider accepted is never
replayed.

## Webhooks

`POST /api/webhooks/{brevo|mailjet|<provider id>}`, authenticated with HTTP
basic auth (any user, password = the provider's webhook secret) or a bearer
token. Events are stored once (deduplicated on the provider's event id), and
status only moves forward (`accepted` → `deferred` → `delivered` →
`bounced`/`complained`), so retries and out-of-order deliveries are harmless.
Hard bounces, complaints and unsubscribes add the address to the suppression
list.

## Metrics

`GET /metrics` on the API (not exposed through the web server) returns queue
depth, oldest pending job, jobs in acceptance unknown and dead letters, in
Prometheus text format.

## Limits worth knowing

- Per-provider send rate (`rate_limit_per_second`) is enforced per worker
  process. With two worker replicas the combined rate can be twice the
  setting; halve it if a provider's account limit is tight. Public endpoint
  rate limits (signup, resend, contact, OTP) are stored in Postgres and shared
  by all replicas.
- The worker keeps message bodies for `job_body_retention_days` (90) for
  retries and audits, then deletes them.

## Contact form emails

A visitor verifies their email with a one-time code, then sends the message.
Two emails go out at once:

- **To the visitor:** an acknowledgement. When a resume is available (uploaded
  in Site profile → Resume, or `RESUME_FILE_PATH`), it links to the `/resume`
  page, where they confirm their email again with a new code and download it
  (template `contact_ack_resume`, otherwise `contact_ack`).
- **To the owner** (`ADMIN_ALERT_EMAIL`, else the reply-to address in Email
  settings): "Someone might have enquired about a project", with a link to
  Admin → Messages (template `contact_alert_owner`). It carries no part of the
  message; read it, reply, or press "Forward to inbox" there.

Both templates can be edited in Admin → Subscribers & Email → Templates.

## Personal data retention

Matches the privacy notice at `/privacy`; purges run every 6 hours.

| Data | Kept for |
| --- | --- |
| Unconfirmed signups | 30 days (`pending_retention_days`) |
| Resume download codes | 30 days |
| Contact messages | 24 months; sender IP 30 days |
| Email bodies | 90 days |
| Raw webhook payloads | 90 days |
| Rate-limit counters | 2 days |
| Suppression list | As long as needed to honour the opt-out |

A subscriber can be erased from the console (Subscribers → Erase). That
deletes their record and consent history, scrubs their stored email bodies,
and keeps only a suppression entry so they are never emailed again.
