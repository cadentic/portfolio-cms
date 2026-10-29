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
    'email/monitor', 'email/suppressions', 'content', 'messages', 'mcp', 'audit',
    'site/home', 'site/profile', 'site/videos', 'site/legal']) {
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
  const title = `E2E video article ${Date.now()}`;
  let path = '';
  try {
    await page.goto('/admin/content/new?kind=article');
    await page.getByRole('textbox', { name: 'Title', exact: true }).fill(title);
    await page.getByLabel('Summary / excerpt').fill('Patching every RAC node without downtime.');
    await page.locator('.a-hero-pick label', { hasText: 'YouTube' }).click();
    await page.getByLabel('YouTube URL or video ID').fill('https://youtu.be/gXT9izr0ljQ?si=XKDH-MSqA-o66lk3');
    await page.getByLabel('Paragraph').first().fill('Keep **every node** available.');
    await page.getByRole('button', { name: 'Save draft' }).click();
    await page.waitForURL(/\/admin\/content\/[0-9a-f-]{36}$/);
    await page.getByRole('button', { name: 'Publish…' }).click();
    // No newsletter: the test must not leave a campaign behind.
    await page.getByLabel('Prepare a newsletter for verified subscribers').uncheck();
    await page.getByRole('button', { name: 'Publish now' }).click();
    await expect(page.getByText(/^Published/)).toBeVisible();

    path = (await page.locator('.a-mono').first().textContent())!.trim();
    const res = await request.get(path);
    expect(res.status()).toBe(200);
    const html = await res.text();
    expect(html).toContain('BlogPosting');
    expect(html).toContain('VideoObject');
    expect(html).toContain('<strong>every node</strong>');
  } finally {
    if (/\/admin\/content\/[0-9a-f-]{36}$/.test(page.url())) {
      const publishDialog = page.getByRole('dialog');
      if (await publishDialog.isVisible()) await publishDialog.getByRole('button', { name: 'Cancel' }).click();
      page.once('dialog', (d) => d.accept());
      await page.getByRole('button', { name: 'Delete article' }).click();
      await page.waitForURL(/\/admin\/content$/);
      if (path) expect((await request.get(path)).status()).toBe(404);
    }
  }
});

test('owner changes years of experience and the home page shows it', async ({ page, request }) => {
  await signIn(page);
  await page.goto('/admin/site/home');
  const hero = page.locator('#sec-hero');
  await hero.locator('summary').click();
  const years = hero.getByLabel('Years of experience', { exact: true });
  const original = await years.inputValue();
  try {
    await years.fill('47');
    await page.getByRole('button', { name: 'Save changes' }).click();
    await expect(page.locator('.a-ok[role="status"]')).toHaveText(/Saved/);
    await expect.poll(async () => (await (await request.get('/')).text()).includes('47+ years'), { timeout: 15_000 }).toBe(true);
    await expect.poll(async () => (await (await request.get('/llms.txt')).text()).includes('47+ years'), { timeout: 15_000 }).toBe(true);
  } finally {
    await years.fill(original);
    await page.getByRole('button', { name: 'Save changes' }).click();
    await expect(page.locator('.a-ok[role="status"]')).toHaveText(/Saved/);
  }
  await expect.poll(async () => (await (await request.get('/')).text()).includes(`${original}+ years`), { timeout: 15_000 }).toBe(true);
});

test('unsaved changes are protected', async ({ page }) => {
  await signIn(page);
  await page.goto('/admin/site/home');
  const hero = page.locator('#sec-hero');
  await hero.locator('summary').click();
  await hero.getByLabel('Line above the headline', { exact: true }).fill(`Unsaved ${Date.now()}`);
  let message = '';
  page.once('dialog', (d) => { message = d.message(); void d.dismiss(); });
  await page.getByRole('link', { name: '⌂ Dashboard' }).click();
  await expect.poll(() => message).toContain('unsaved');
  await expect(page).toHaveURL(/site\/home/);
  // Reloading discards the draft; the browser asks first (beforeunload).
  page.on('dialog', (d) => void d.accept());
  await page.reload();
  await expect(page.locator('#sec-hero')).toBeVisible();
});

test('history restores a previous version', async ({ page, request }) => {
  await signIn(page);
  await page.goto('/admin/site/home');
  const hero = page.locator('#sec-hero');
  await hero.locator('summary').click();
  const marker = `History marker ${Date.now()}`;
  await hero.getByLabel('Line above the headline', { exact: true }).fill(marker);
  await page.getByRole('button', { name: 'Save changes' }).click();
  await expect(page.locator('.a-ok[role="status"]')).toHaveText(/Saved/);
  await expect.poll(async () => (await (await request.get('/')).text()).includes(marker), { timeout: 15_000 }).toBe(true);

  await page.getByRole('button', { name: 'History', exact: true }).click();
  const dialog = page.getByRole('dialog');
  page.once('dialog', (d) => void d.accept());
  await dialog.getByRole('button', { name: 'Restore this version' }).nth(1).click();
  await expect(dialog).toHaveCount(0);
  await expect(hero.getByLabel('Line above the headline', { exact: true })).not.toHaveValue(marker);
  await expect.poll(async () => (await (await request.get('/')).text()).includes(marker), { timeout: 15_000 }).toBe(false);
});

test('legal pages cannot be deleted', async ({ page }) => {
  await signIn(page);
  await page.goto('/admin/site/legal');
  await page.getByRole('link', { name: 'Edit Privacy notice' }).click();
  await page.waitForURL(/\/admin\/content\/[0-9a-f-]{36}$/);
  await expect(page.getByRole('button', { name: 'Save changes (live)' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Delete page' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Unpublish' })).toHaveCount(0);
  await expect(page.getByLabel('URL slug')).toHaveAttribute('readonly', '');
});

test('owner uploads a resume', async ({ page, request }) => {
  await signIn(page);
  await page.goto('/admin/site/profile#sec-resume');
  const group = page.locator('#sec-resume');
  const status = group.locator('.a-resume-status');
  await expect(status).toBeVisible();
  // Never replace or remove a resume the owner uploaded.
  test.skip(/Uploaded/.test(await status.innerText()), 'a resume is already uploaded');
  const name = `e2e-resume-${Date.now()}.pdf`;
  // The hero button; the label alone also appears in the FAQ text.
  const button = `${(await (await request.get('/api/public/site-content')).json()).hero.resume_cta_label} ↗`;
  const shown = async () => (await (await request.get('/')).text()).includes(button);
  try {
    await group.locator('input[type="file"]').setInputFiles({ name, mimeType: 'application/pdf',
      buffer: Buffer.from('%PDF-1.4\n%\xe2\xe3\xcf\xd3\n1 0 obj << >> endobj\ntrailer << >>\n%%EOF\n') });
    await expect(status).toContainText(name);
    await expect.poll(shown, { timeout: 15_000 }).toBe(true);
  } finally {
    if (/Uploaded/.test(await status.innerText())) {
      page.once('dialog', (d) => void d.accept());
      await group.getByRole('button', { name: 'Remove uploaded resume' }).click();
      await expect(status).toContainText('No resume');
    }
  }
  await expect.poll(shown, { timeout: 15_000 }).toBe(false);
});

test('non-owner accounts are refused', async ({ request }) => {
  expect((await request.get('/api/admin/me')).status()).toBe(401);
});
