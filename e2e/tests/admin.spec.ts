import { expect, test, type Page } from '@playwright/test';
import { expectAccessible, expectNoHorizontalScroll, watchErrors } from './helpers';

// Acceptance criterion 15 (CMS usability): the owner signs in, every admin
// screen loads without errors, and a page with a YouTube hero can be built
// and published with no code.
const email = process.env['LOCAL_ADMIN_EMAIL'] ?? 'owner@cadentic.dev';
const password = process.env['LOCAL_ADMIN_PASSWORD'] ?? 'localpass123';

async function signIn(page: Page) {
  await page.goto('/admin');
  await page.getByLabel('Email', { exact: true }).fill(email);
  await page.getByLabel('Password', { exact: true }).fill(password);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await page.waitForURL(/\/admin\/?$/);
}

test('owner can reach every admin screen', async ({ page, baseURL }) => {
  const errors = watchErrors(page, baseURL!);
  await signIn(page);
  for (const path of ['email/subscribers', 'email/campaigns', 'email/templates', 'email/providers', 'email/settings',
    'email/monitor', 'email/suppressions', 'content', 'messages', 'mcp', 'audit']) {
    await page.goto('/admin/' + path, { waitUntil: 'networkidle' });
    await expect(page.locator('.a-err'), path).toHaveCount(0);
    await expectNoHorizontalScroll(page);
  }
  await page.goto('/admin/email/subscribers', { waitUntil: 'networkidle' });
  await expectAccessible(page);
  expect(errors).toEqual([]);
});

test('owner builds and publishes an article with a YouTube hero', async ({ page, request }) => {
  await signIn(page);
  const title = `Rolling patch walkthrough ${Date.now()}`;
  await page.goto('/admin/content/new?kind=article');
  await page.getByRole('textbox', { name: 'Title', exact: true }).fill(title);
  await page.getByLabel('Summary / excerpt').fill('Patching every RAC node without downtime.');
  await page.locator('.a-hero-pick label', { hasText: 'YouTube' }).click();
  await page.getByLabel('YouTube URL or video ID').fill('https://youtu.be/dQw4w9WgXcQ');
  await page.getByLabel('Paragraph').first().fill('Keep **every node** available.');
  await page.getByRole('button', { name: 'Save draft' }).click();
  await page.waitForURL(/\/admin\/content\/[0-9a-f-]{36}$/);
  await page.getByRole('button', { name: 'Publish…' }).click();
  await page.getByRole('button', { name: 'Publish now' }).click();
  await expect(page.getByText(/^Published/)).toBeVisible();

  const path = await page.locator('.a-mono').first().textContent();
  const res = await request.get(path!.trim());
  expect(res.status()).toBe(200);
  const html = await res.text();
  expect(html).toContain('BlogPosting');
  expect(html).toContain('VideoObject');
  expect(html).toContain('<strong>every node</strong>');
});

test('non-owner accounts are refused', async ({ request }) => {
  expect((await request.get('/api/admin/me')).status()).toBe(401);
});
