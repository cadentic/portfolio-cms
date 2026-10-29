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
  expect(html).toMatch(/<meta property="og:image" content="[^"]+sayantan\.jpg"/);
  const ld = [...html.matchAll(/<script type="application\/ld\+json"[^>]*>([\s\S]*?)<\/script>/g)].map((m) => JSON.parse(m[1]));
  expect(JSON.stringify(ld)).toContain('"Person"');
  expect(html).toContain('<details class="sitemap"');
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
