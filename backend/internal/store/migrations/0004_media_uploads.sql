-- Uploaded media lives in a private bucket (or a local directory) under
-- storage_key, the path after /media/ in its public URL.
ALTER TABLE cms_media
    ADD COLUMN storage_key  text,
    ADD COLUMN content_type text,
    ADD COLUMN size_bytes   bigint,
    ADD COLUMN filename     text,
    ADD COLUMN uploaded_by  text;

-- Imported WordPress images already use /media/<key> URLs.
UPDATE cms_media SET storage_key = substr(url, 8)
WHERE storage_key IS NULL AND url LIKE '/media/%';

CREATE UNIQUE INDEX cms_media_storage_key_uq ON cms_media (storage_key) WHERE storage_key IS NOT NULL;
CREATE INDEX cms_media_created_idx ON cms_media (created_at DESC);
