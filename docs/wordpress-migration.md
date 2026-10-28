# WordPress migration (csayantan.wordpress.com → cadentic.dev)

The old blog is mostly a tree of about 75 technical **pages** (Oracle RAC, EBS R12,
RMAN, Linux, storage notes) plus a few **posts**. `tools/wpimport` pulls all of it
through the WordPress.com public REST API and loads it into the CMS tables
(`cms_pages`, `cms_media`, `cms_redirects`).

## Rules

- **Everything is imported as a draft** (`status = 'draft'`, `published_at = NULL`).
  The owner reviews each item and publishes it from the admin.
- **No newsletter.** `first_published_at` holds the original WordPress date and
  `source = 'wordpress'`, so the publish flow never offers a subscriber notification
  for imported items.
- **Content only.** WordPress themes, layouts, classes, inline styles, share/like
  widgets, scripts and shortcodes are dropped. Each item uses the new CMS layout:
  the hero is the featured image (or `none`), and the body is native blocks.

## Mapping

| WordPress | CMS |
| --- | --- |
| post | `cms_pages.kind = 'article'`, URL `/blog/{slug}` |
| page | `cms_pages.kind = 'page'`, URL `/{parent-slug}/…/{slug}` via `parent_id` |
| slug | `slug` unchanged; a page slug that repeats under another parent becomes `{parent-slug}-{slug}` (cms_pages has `UNIQUE(kind, slug)`) |
| title, excerpt, author, menu order | `title`, `excerpt` (plain text), `author_name`, `menu_order` |
| date | `first_published_at` |
| featured image | `hero = {"type":"image",…}`, `featured_image_url`, `seo.og_image` |
| categories, tags | `categories`, `tags` (names) |
| `<h1>`–`<h6>` | `heading` (levels 2–4) |
| `<p>` | `paragraph`, or an `html` block when it has links or emphasis (only `a`, `strong`, `em`, `code`, `br`, `sub`, `sup` are kept, with no attributes except `href`) |
| `<ul>`/`<ol>` | `list` (`bullet`/`ordered`) |
| `<blockquote>` | `quote` |
| `[code]`/`[sourcecode]`/`<pre>` | `code` with its language |
| `<hr>` | `divider` |
| images | `html` block `<figure><img><figcaption>`; file saved to `frontend/public/media/wordpress/YYYY/MM/…` and served at `/media/wordpress/…`; row in `cms_media` |
| tables | `html` block with bare `table/tr/th/td` |
| links to other old pages | rewritten to the new path |
| SEO | `seo.title`, `seo.description` (first 160 chars of the excerpt), `seo.canonical = https://cadentic.dev{path}` |

Only images are copied. Other attachments (the two old resume PDFs) keep their
wordpress.com URL, so nothing with personal contact details lands in the public
`frontend/public/media` folder. Comments are not imported.

The old blog had no real code formatting: commands, SQL and output sat in
blockquotes, and subheadings were bold paragraphs. Those become `code` blocks
(language guessed as `sql`, `bash` or `plaintext`) and level-3 `heading` blocks.

## What was imported (28 Oct 2026)

- 1 post (`/blog/hello-world`, titled "About") and 82 pages, 71 of them nested
  under a parent page. Page paths are unchanged.
- 16 images (certificates, Teradata and screenshot images).
- 5 pages are empty on WordPress too (section index pages such as `/storage`);
  they are imported as empty drafts.
- `/about` ("Details") is an old resume with a home address, personal emails
  and phone numbers. Review or delete it before publishing anything there.

## Redirects (301)

`content/wordpress/redirects.json` and `cms_redirects` hold:

- every old post URL `/YYYY/MM/DD/{slug}` → `/blog/{slug}`
- any page whose path changed because of a slug clash
- `/YYYY`, `/YYYY/MM`, `/YYYY/MM/DD`, `/category/{slug}`, `/tag/{slug}`, `/feed` → `/blog` (with a `category`/`tag` filter)

Page paths are otherwise the same as on WordPress, so `/11gr2-rac-installation/oracleasm/`
keeps working without a redirect.

A redirect to an item that is still a draft lands on a 404 until the item is published.

These redirects only receive traffic once requests for the old domain reach
cadentic.dev. WordPress.com can do that with its **Site Redirect** upgrade
(Settings → General → Site Address on wordpress.com), which forwards
`csayantan.wordpress.com/any/path` to `cadentic.dev/any/path` with a 301. Turn it on
after the important pages are published.

## Running it

The cloud build sandbox cannot reach wordpress.com, so the content is fetched by
the **WordPress import** GitHub Action (`.github/workflows/wordpress-import.yml`).
It runs when the importer changes or on demand (Actions → WordPress import → Run
workflow) and commits `content/wordpress/`, the images and
`backend/internal/store/migrations/0003_wordpress_import.sql`.

Because the seed is also migration 0003, `docker compose up` loads it into local
Postgres automatically. To do it by hand or on your own machine:

```sh
scripts/wordpress-import.sh fetch          # re-download from WordPress.com
scripts/wordpress-import.sh load           # apply content/wordpress/seed.sql to $DATABASE_URL
scripts/test-wordpress-seed.sh             # importer test against a throwaway Postgres
```

The seed is idempotent. Running it again:

- adds nothing new if nothing changed,
- refreshes only drafts that nobody has edited since the import (`updated_at = created_at`),
- never touches edited or published items, and never changes `status`,
- skips (with a NOTICE) any item whose slug is already used by non-WordPress content,
- never overwrites an existing redirect.

Raw API responses are kept in `content/wordpress/raw/`, so `scripts/wordpress-import.sh build`
can regenerate everything offline after the importer changes.
