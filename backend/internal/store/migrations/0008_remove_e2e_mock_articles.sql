-- Remove articles left by the old e2e publish test (mock video dQw4w9WgXcQ) and their
-- never-sent notification campaigns. The match is that test's exact data
-- (e2e/tests/admin.spec.ts:34-39 at 4c14b3e), so it deletes nothing written by the owner.
WITH mock AS (
  SELECT id FROM cms_pages
  WHERE kind = 'article'
    AND title ~ '^Rolling patch walkthrough [0-9]{13}$'
    AND excerpt = 'Patching every RAC node without downtime.'
    AND hero->>'youtube_id' = 'dQw4w9WgXcQ'
), camp AS (
  DELETE FROM email_campaigns c USING mock
  WHERE c.blog_post_id = mock.id AND c.status IN ('draft', 'cancelled')
  RETURNING c.id
)
DELETE FROM cms_pages p USING mock WHERE p.id = mock.id;
