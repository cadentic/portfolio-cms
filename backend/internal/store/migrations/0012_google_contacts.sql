-- Google Contacts sync: contacts with one chosen Google Contacts label become
-- newsletter subscribers on their own list (source 'google_contacts'), kept
-- out of the regular newsletter unless a campaign targets that list.
--
-- One row holds the connection. The refresh token is encrypted with a key
-- derived from APP_SECRET; it is never returned by the API.
CREATE TABLE google_contacts_sync (
    id                smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    account_email     text,
    refresh_token_enc bytea,
    connected_at      timestamptz,
    connected_by      text,
    label_resource    text,
    label_name        text,
    attested_by       text,
    attested_at       timestamptz,
    paused            boolean NOT NULL DEFAULT false,
    sync_started_at   timestamptz,
    last_sync_at      timestamptz,
    last_result       jsonb,
    last_error        text,
    updated_at        timestamptz NOT NULL DEFAULT now()
);
INSERT INTO google_contacts_sync (id) VALUES (1);

-- Pending "Connect Google Contacts" sign-ins: single use, ten minutes. Only
-- the SHA-256 hash of the state parameter is stored.
CREATE TABLE google_oauth_states (
    state_hash    bytea PRIMARY KEY,
    code_verifier text NOT NULL,
    created_by    text NOT NULL,
    expires_at    timestamptz NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
