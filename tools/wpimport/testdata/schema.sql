-- Minimal copy of the cms_* tables from backend migration 0001 (as described
-- by the CMS thread), used only by scripts/test-wordpress-seed.sh.
CREATE TABLE cms_pages (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  kind text NOT NULL CHECK (kind IN ('page','article')),
  parent_id uuid REFERENCES cms_pages(id) ON DELETE SET NULL,
  menu_order int NOT NULL DEFAULT 0,
  slug text NOT NULL,
  title text NOT NULL,
  excerpt text NOT NULL DEFAULT '',
  hero jsonb NOT NULL DEFAULT '{"type":"none"}',
  blocks jsonb NOT NULL DEFAULT '[]',
  featured_image_url text,
  author_name text,
  categories text[] NOT NULL DEFAULT '{}',
  tags text[] NOT NULL DEFAULT '{}',
  seo jsonb NOT NULL DEFAULT '{}',
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published','archived')),
  published_at timestamptz,
  first_published_at timestamptz,
  source text,
  source_id text,
  created_by text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (kind, slug)
);
CREATE UNIQUE INDEX cms_pages_source_uq ON cms_pages (source, source_id) WHERE source IS NOT NULL;

CREATE TABLE cms_media (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  url text NOT NULL,
  kind text NOT NULL CHECK (kind IN ('image','video','file')),
  alt text NOT NULL DEFAULT '',
  caption text NOT NULL DEFAULT '',
  width int, height int,
  source text, source_id text, source_url text,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX cms_media_source_uq ON cms_media (source, source_id) WHERE source IS NOT NULL;

CREATE TABLE cms_redirects (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  from_path text NOT NULL UNIQUE,
  to_path text NOT NULL,
  status_code int NOT NULL DEFAULT 301 CHECK (status_code IN (301,302,307,308)),
  source text,
  created_at timestamptz NOT NULL DEFAULT now()
);
