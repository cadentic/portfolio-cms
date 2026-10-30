# Security model

How the site defends against common web attacks, and where each defence
lives. Last reviewed 2026-10-29.

| Attack | Defence | Where |
|---|---|---|
| SQL injection | Every query uses pgx `$n` parameters. Dynamic column lists (page create/update, campaign segments) are built only from server-side constant names; user values are always bound as parameters. The WordPress seed generator escapes literals and is run by the operator, never by the API. | `backend/internal/**` |
| Cross-site request forgery | Admin and MCP calls authenticate with an `Authorization: Bearer` token, never a cookie, so a forged request carries no credentials. On top of that, every unsafe request (POST/PUT/PATCH/DELETE) from another site is refused with Go's `http.CrossOriginProtection` (`Sec-Fetch-Site`, then `Origin`), and JSON endpoints require `Content-Type: application/json`, which an HTML form cannot send. Provider webhooks, one-click unsubscribe and the OAuth/MCP endpoints are exempt because they are called server-to-server and authenticate with secrets, signed tokens or PKCE. | `api/server.go` (`csrf`, `decode`) |
| Cross-site scripting | Author HTML is cleaned with bluemonday on save. Text blocks are escaped and only a small inline Markdown subset is rendered, with links limited to https, mailto and site-relative URLs. Angular sanitises `[innerHTML]` again. A nonce-based Content-Security-Policy blocks injected scripts. Email previews in the admin render in `sandbox=""` iframes. | `render/render.go`, `content/content.go`, `shared/inline.ts`, `frontend/src/server.ts` |
| Malicious uploads | File type is detected from the bytes, not the name or Content-Type. Only images, PDF, MP4 and WebM are accepted (no SVG or HTML). Keys are server-generated, and `/media/` responses carry `nosniff` and a locked-down CSP. | `api/media.go`, `media/media.go` |
| Authentication / authorisation | Firebase ID tokens are verified on the server (signature, audience, issuer, expiry). Admin rights come only from `ADMIN_UIDS` / `ADMIN_EMAILS`. MCP access tokens are opaque, hashed at rest, expire after an hour, and the allow-list is checked again on every call. | `auth/auth.go`, `oauth/oauth.go` |
| Brute force / abuse | Rate limits are kept in Postgres, so they hold across instances: subscribe, contact, resume OTP, unsubscribe, and the OAuth register, token, authorize, client and revoke endpoints. OTPs allow 5 attempts. Contact and subscribe forms have a honeypot field. | `ratelimit/`, `api/public.go`, `api/server.go` |
| Email relaying spam | Visitors can make the site email any address (acknowledgements, confirmations), so a typed name that looks like a link, domain or address is never put in the greeting or the To line. | `mailer/mailer.go` |
| Clickjacking | `frame-ancestors 'none'` and `X-Frame-Options: DENY`. | `server.ts`, `api/server.go` |
| Transport | HSTS in production, `upgrade-insecure-requests`, HTTPS-only `PUBLIC_SITE_URL` in production. | `server.ts`, `config/config.go` |
| IP spoofing (rate-limit bypass) | The web tier removes any client `X-Client-IP` and sets its own. The API trusts that header only when it cannot be reached directly (Cloud Run IAM, internal ingress, compose network). | `server.ts`, `api/server.go` |
| Secrets in logs | Query strings (which carry tokens) are not logged. Error text is redacted, and request ids are limited to safe characters. | `api/server.go`, `email/redact.go` |
| Host header / SSRF | Angular SSR only answers for `NG_ALLOWED_HOSTS`. The API makes no outbound requests to user-supplied URLs. Provider base-URL overrides are refused in production. | infra, `config/config.go` |
| Third-party account tokens | The Google Contacts refresh token is encrypted with AES-256-GCM (key derived from `APP_SECRET`) and never returned by the API. The sign-in uses PKCE and a single-use, hashed, 10-minute `state` that only an admin can create; the scope is read-only contacts. | `googlecontacts/googlecontacts.go` |
| Dependencies | Go modules and npm packages are checked with `govulncheck` and `npm audit --omit=dev`. | `backend/go.mod`, `frontend/package.json` |

## Checking for vulnerable dependencies

```powershell
cd backend;  go run golang.org/x/vuln/cmd/govulncheck@latest ./...
cd frontend; npm audit --omit=dev
```

`@grpc/grpc-js` is pinned to a fixed release with an npm `overrides` entry. It
comes in through Firebase's Firestore package, which the site does not use;
remove the override when Firebase raises its own range.
