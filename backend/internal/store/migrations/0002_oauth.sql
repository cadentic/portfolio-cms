-- OAuth 2.1 authorization server for the MCP connector. The owner signs in
-- with Firebase; only hashes of codes and tokens are stored.

CREATE TABLE oauth_clients (
    client_id     text PRIMARY KEY,
    client_name   text NOT NULL,
    redirect_uris text[] NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE oauth_codes (
    code_hash      bytea PRIMARY KEY,
    client_id      text NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    redirect_uri   text NOT NULL,
    code_challenge text NOT NULL,
    scope          text NOT NULL DEFAULT '',
    resource       text NOT NULL DEFAULT '',
    uid            text NOT NULL,
    email          text NOT NULL,
    expires_at     timestamptz NOT NULL,
    used_at        timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE oauth_tokens (
    token_hash  bytea PRIMARY KEY,
    kind        text NOT NULL CHECK (kind IN ('access','refresh')),
    client_id   text NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    uid         text NOT NULL,
    email       text NOT NULL,
    scope       text NOT NULL DEFAULT '',
    expires_at  timestamptz NOT NULL,
    revoked_at  timestamptz,
    last_used_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX oauth_tokens_client_idx ON oauth_tokens (client_id, kind);
