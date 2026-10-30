import { expect, test } from '@playwright/test';
import { expectAccessible, expectNoHorizontalScroll, watchErrors } from './helpers';

// Acceptance criterion 15 (public side): every page renders on the server,
// is accessible, works at phone width and has search metadata.
const pages = ['/', '/blog', '/videos', '/privacy', '/terms', '/ai-policy', '/subscribe/confirm?token=invalid', '/unsubscribe?token=invalid'];

for (const path of pages) {
  test(`renders ${path} accessibly`, async ({ page, baseURL }) => {
    const errors = watchErrors(page, baseURL!);
    const res = await page.goto(path, { waitUntil: 'networkidle' });
    expect(res?.status()).toBe(200);
    await expect(page.locator('h1')).toHaveCount(1);
    await expect(page.locator('html')).toHaveAttribute('lang', 'en');
    await expectNoHorizontalScroll(page);
    await expectAccessible(page);
    expect(errors).toEqual([]);
  });
}

test('server-rendered HTML carries SEO, Open Graph and JSON-LD', async ({ request }) => {
  const html = await (await request.get('/')).text();
  expect(html).toContain('<link rel="canonical"');
  expect(html).toMatch(/<meta property="og:image" content="https?:\/\/[^"]+\.(jpe?g|png|webp|avif|gif)"/);
  const ld = [...html.matchAll(/<script type="application\/ld\+json"[^>]*>([\s\S]*?)<\/script>/g)].map((m) => JSON.parse(m[1]));
  expect(JSON.stringify(ld)).toContain('"Person"');
  expect(html).toContain('<details class="sitemap"');
});

// Angular escapes these in server-rendered text and attributes.
const esc = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

test('home renders the editable site content', async ({ request }) => {
  const api = await request.get('/api/public/site-content');
  expect(api.headers()['cache-control']).toBe('public, max-age=0, must-revalidate');
  const c = await api.json();
  const html = await (await request.get('/')).text();
  expect(html).toContain(esc(c.identity.name));
  for (const m of c.hero.metrics) expect(html).toContain(esc(m.value));
  if (c.organizations.visible) for (const o of c.organizations.items) expect(html).toContain(esc(o.name));
  if (c.videos.visible && c.videos.featured.youtube_id) expect(html).toContain(`i.ytimg.com/vi/${c.videos.featured.youtube_id}`);
  expect(html).not.toContain('dQw4w9WgXcQ');

  if (c.videos.items.length) {
    const videos = await (await request.get('/videos')).text();
    expect(videos).toContain(esc(c.videos.items[0].title));
    expect(videos).toContain(`vi/${c.videos.items[0].youtube_id}/hqdefault`);
  }
});

// The browser reuses what the server fetched, so a page never flips to "Loading…" (or to
// "Temporarily unavailable") after it has been rendered, and the section numbers stay put.
for (const path of ['/', '/privacy', '/blog']) {
  test(`hydration of ${path} does not refetch from the API`, async ({ page }) => {
    const urls: string[] = [];
    page.on('request', (r) => {
      if (new URL(r.url()).pathname.startsWith('/api/')) urls.push(r.url());
    });
    await page.goto(path, { waitUntil: 'networkidle' });
    expect(urls).toEqual([]);
  });
}

test('"Try again" on the unavailable page stays on this site', async ({ page, baseURL }) => {
  // The page's own read fails as if the API were down.
  await page.route((u) => u.pathname.startsWith('/api/public/pages/'), (r) =>
    r.fulfill({ status: 503, contentType: 'application/json', body: '{"error":"unavailable"}' }));
  // Decoded, the first segment starts with a backslash, "//" or a tab and "/": browsers read each
  // as a link to another site.
  for (const path of ['/%5Cevil.example%23%00', '/%2F%2Fevil.example%23%00', '/%09%2Fevil.example%23%00']) {
    await page.goto(path);
    const retry = page.getByRole('link', { name: 'Try again' });
    await expect(retry).toBeVisible();
    const href = (await retry.getAttribute('href'))!;
    expect(new URL(href, baseURL + '/x').origin, href).toBe(new URL(baseURL!).origin);
  }
});

test('legal pages are CMS pages in the sitemap', async ({ request }) => {
  const sitemap = await (await request.get('/sitemap.xml')).text();
  expect(sitemap.split('/privacy</loc>').length - 1).toBe(1);
  expect(await (await request.get('/privacy')).text()).toContain('Last updated');
});

test('titles, meta descriptions and image alts end with the brand suffix', async ({ request }) => {
  const suffix = 'Sayantan Chakraborty, Oracle DBA';
  for (const path of ['/', '/blog', '/videos', '/privacy']) {
    const html = await (await request.get(path)).text();
    expect(html).toMatch(new RegExp(`<title>[^<]*${suffix}</title>`));
    for (const tag of ['name="description"', 'property="og:title"', 'property="og:description"', 'name="twitter:title"', 'property="og:image:alt"']) {
      expect(html, `${path} ${tag}`).toMatch(new RegExp(`<meta ${tag} content="[^"]*${suffix}"`));
    }
    for (const m of html.matchAll(/<img\b[^>]*>/g)) expect(m[0], path).toMatch(new RegExp(`alt="[^"]*${suffix}"`));
  }
});

test('unknown paths return a real 404', async ({ page }) => {
  const res = await page.goto('/this-page-does-not-exist');
  expect(res?.status()).toBe(404);
});

test('robots.txt allows answer engines and llms.txt is served', async ({ request }) => {
  const robots = await (await request.get('/robots.txt')).text();
  for (const bot of ['GPTBot', 'ClaudeBot', 'PerplexityBot', 'Google-Extended']) expect(robots).toContain(`User-agent: ${bot}`);
  expect(robots).toContain('Disallow: /admin');
  expect((await request.get('/llms.txt')).status()).toBe(200);
  expect(await (await request.get('/sitemap.xml')).text()).toContain('<urlset');
});

test('footer sitemap is collapsed until opened', async ({ page }) => {
  await page.goto('/');
  const sitemap = page.locator('details.sitemap');
  await expect(sitemap).not.toHaveAttribute('open', '');
  await sitemap.locator('summary').click();
  await expect(sitemap).toHaveAttribute('open', '');
  await expect(sitemap.getByRole('link', { name: /Privacy notice/ })).toBeVisible();
});

test('newsletter signup works from the keyboard', async ({ page }) => {
  await page.goto('/');
  const form = page.getByRole('region', { name: 'Newsletter' });
  await form.getByRole('textbox', { name: 'Email address' }).fill(`kb-${Date.now()}@example.com`);
  await form.getByRole('checkbox').first().check();
  await form.getByRole('button', { name: /subscribe/i }).press('Enter');
  await expect(page.getByText(/confirmation email is on its way/i)).toBeVisible();
});

test('security headers are sent', async ({ request }) => {
  const res = await request.get('/');
  const csp = res.headers()['content-security-policy'];
  expect(csp).toContain("object-src 'none'");
  expect(csp).toMatch(/script-src 'self' 'nonce-/);
  expect(res.headers()['x-content-type-options']).toBe('nosniff');
  expect((await request.get('/admin')).headers()['x-robots-tag']).toContain('noindex');
});
