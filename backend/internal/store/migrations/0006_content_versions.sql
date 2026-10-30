-- Version history for owner-edited content: site content (app_settings 'site_content')
-- and the protected legal pages. Snapshots hold no visitor personal data.
CREATE TABLE content_versions (
    id          bigserial PRIMARY KEY,
    entity_type text NOT NULL CHECK (entity_type IN ('site_content', 'page')),
    entity_id   text NOT NULL,
    action      text NOT NULL CHECK (action IN ('seed', 'update', 'reset', 'restore')),
    changed     text[] NOT NULL DEFAULT '{}',
    snapshot    jsonb NOT NULL,
    sha256      text NOT NULL,
    actor       text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX content_versions_entity_idx ON content_versions (entity_type, entity_id, id DESC);
