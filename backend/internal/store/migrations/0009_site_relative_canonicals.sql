-- The WordPress import wrote canonical URLs as https://cadentic.dev/<path>.
-- The site may be served from a subdomain such as profile.cadentic.dev, so
-- make those canonicals site-relative: the site resolves them against
-- PUBLIC_SITE_URL, wherever it runs. Canonicals pointing at other sites (a
-- post first published elsewhere) are left alone.
UPDATE cms_pages
SET seo = jsonb_set(seo, '{canonical}',
        to_jsonb(coalesce(nullif(regexp_replace(seo->>'canonical', '^https?://(www\.)?cadentic\.dev', '', 'i'), ''), '/')))
WHERE seo->>'canonical' ~* '^https?://(www\.)?cadentic\.dev(/|$)';
