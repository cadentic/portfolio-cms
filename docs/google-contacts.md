# Google Contacts list

**Optional, off by default.** Turn it on or off with:

- `python3 installSetup.py --google-contacts` (asks for the OAuth client id and
  secret) or `--no-google-contacts`, then deploy; or
- `google_contacts_enabled = true | false` in `infra/terraform/secret.tfvars`,
  then deploy; or
- locally, `GOOGLE_CONTACTS_ENABLED=true` in `.env`, then `docker compose up -d`.

When it is off, the admin hides the Google Contacts tab, the only endpoint left
is the status call (`GET /api/admin/google-contacts` answers `{"enabled":false}`),
the Google callback does not exist, and the worker never syncs. Contacts already
on the list stay (still out of the regular newsletter), and the stored Google
connection is kept for when it is turned back on.

Contacts that carry one chosen Google Contacts label are copied into the
newsletter subscribers as a **separate list** (source `google_contacts`).

- The regular newsletter never goes to this list. A campaign reaches it only
  when its audience sources include `google_contacts` (Campaign editor →
  "Audience: sources"). Campaigns created when an article is published leave
  it out.
- New contacts are added as verified, with the owner's attestation as consent
  evidence ("Google Contacts label "X", consent attested by owner"), and get no
  email when they are added.
- Anyone who unsubscribed, bounced, complained or has any active suppression is
  skipped and never added back. A contact who is already a subscriber from the
  site stays on the regular list.
- Taking a label off a contact in Google does not unsubscribe them. Use the
  subscriber's Unsubscribe or Erase button in the admin.
- If a Google contact signs up on the site and confirms by email, they move to
  the regular newsletter list.
- Only the contact's primary email address is used (or the first one, when
  none is marked primary).

## Using it

Admin → Subscribers & Email → **Google Contacts**:

1. **Connect Google Contacts** and sign in with the Google account that holds
   the contacts. The site asks for read-only access to contacts
   (`contacts.readonly`) and your email address.
2. **Choose a label**, tick the consent confirmation and press **Use this
   label**. Only contacts with that label are read.
3. **Preview** shows who would be added or skipped, without changing anything.
4. **Sync now** runs a sync straight away. Otherwise the worker syncs about
   every 6 hours. **Pause automatic sync** stops the schedule (Sync now still
   works). **Disconnect** revokes the access at Google; contacts already on the
   list stay.

Every connect, label change, pause and sync run is in Admin → Audit log.

To mail the list: create a campaign and put `google_contacts` in "Audience:
sources". The public `/resume` page is a good link to include: visitors
download the resume after confirming their email with a one-time code.

## Google Cloud setup (once)

Use any Google Cloud project (the one hosting the site is fine).

1. **Enable the People API**: APIs & Services → Library → "Google People API"
   → Enable.
2. **OAuth consent screen** (Google Auth Platform → Branding / Audience):
   - User type **External** (or Internal for a Google Workspace domain).
   - App name, support email, and `cadentic.dev` as an authorised domain.
   - Data access → Add scope `.../auth/contacts.readonly` (plus `openid` and
     `.../auth/userinfo.email`).
   - Audience → **Publish app** so its status is **In production**. In
     *Testing*, Google expires the refresh token after 7 days and the sync
     stops. An unverified app in production works for your own account: on
     first sign-in Google shows "Google hasn't verified this app"; choose
     Advanced → Go to (app name). Verification is only needed for more than
     100 users.
3. **OAuth client**: Credentials → Create credentials → OAuth client ID →
   **Web application**. Authorised redirect URIs:
   - `https://cadentic.dev/api/google/oauth/callback` (your site host; the
     admin page shows the exact URI it uses)
   - `http://localhost:4000/api/google/oauth/callback` for the local stack
4. Turn the feature on with the client ID and secret:
   - Cloud: `python3 installSetup.py --google-contacts` (or set
     `google_contacts_enabled = true`, `google_oauth_client_id` and
     `google_oauth_client_secret` in `infra/terraform/secret.tfvars`), then
     deploy. The secret goes to Secret Manager / Key Vault / the VPS `.env`
     like the other secrets, and only while the feature is on.
   - Local: `GOOGLE_CONTACTS_ENABLED=true`, `GOOGLE_OAUTH_CLIENT_ID` and
     `GOOGLE_OAUTH_CLIENT_SECRET` in `.env`, then `docker compose up -d`.
   The API refuses to start when the feature is on without both values.

## Security notes

- The refresh token is stored encrypted (AES-256-GCM, key derived from
  `APP_SECRET`) and never returned by the API. Changing `APP_SECRET` makes it
  unreadable: the admin then asks you to disconnect and connect again.
- The sign-in uses a single-use, 10-minute `state` (stored hashed) and PKCE.
  Only an admin can start it.
- Labels with more than 10,000 contacts are refused rather than cut short.
