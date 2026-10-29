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

test('cancelling sign-out keeps the session and the draft', async ({ page }) => {
  await signIn(page);
  await page.goto('/admin/site/home');
  const hero = page.locator('#sec-hero');
  await hero.locator('summary').click();
  const line = hero.getByLabel('Line above the headline', { exact: true });
  const draft = `Unsaved ${Date.now()}`;
  await line.fill(draft);
  page.once('dialog', (d) => void d.dismiss());
  await page.getByRole('button', { name: 'Sign out' }).click();
  await expect(page).toHaveURL(/site\/home/);
  await expect(page.locator('.who')).toContainText(email);
  await expect(line).toHaveValue(draft);
  // Still signed in: the admin API still answers.
  await page.getByRole('button', { name: 'History', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog.getByRole('cell', { name: /Original site text/ })).toBeVisible();
  await expect(dialog.locator('.a-err')).toHaveCount(0);
});

test('unsaved changes can be discarded, also from History', async ({ page }) => {
  await signIn(page);
  await page.goto('/admin/site/home');
  const hero = page.locator('#sec-hero');
  await hero.locator('summary').click();
  const line = hero.getByLabel('Line above the headline', { exact: true });
  const original = await line.inputValue();
  await line.fill(`Unsaved ${Date.now()}`);

  const history = page.getByRole('button', { name: 'History', exact: true });
  await history.click();
  const dialog = page.getByRole('dialog');
  const restore = dialog.getByRole('button', { name: 'Restore this version' }).first();
  await expect(restore).toBeDisabled();
  page.once('dialog', (d) => void d.accept());
  await dialog.getByRole('button', { name: 'Discard changes' }).click();
  await expect(restore).toBeEnabled();
  await dialog.getByRole('button', { name: 'Close' }).click();
  await expect(history).toBeFocused();
  await expect(line).toHaveValue(original);
  await expect(page.locator('.a-savebar .pill.pending')).toHaveCount(0);

  // And from the save bar.
  await line.fill(`Unsaved ${Date.now()}`);
  page.once('dialog', (d) => void d.accept());
  await page.locator('.a-savebar').getByRole('button', { name: 'Discard changes' }).click();
  await expect(line).toHaveValue(original);
  await expect(page.locator('.a-savebar .pill.pending')).toHaveCount(0);
});

test('edits made while a save is running are kept', async ({ page }) => {
  await signIn(page);
  await page.goto('/admin/site/home');
  const hero = page.locator('#sec-hero');
  await hero.locator('summary').click();
  const line = hero.getByLabel('Line above the headline', { exact: true });
  const original = await line.inputValue();
  const later = `Typed while saving ${Date.now()}`;
  const slow = (u: URL) => u.pathname === '/api/admin/site-content';
  await page.route(slow, async (r) => {
    if (r.request().method() === 'PUT') await new Promise((done) => setTimeout(done, 1500));
    await r.continue();
  });
  try {
    await line.fill(`Saved first ${Date.now()}`);
    await page.getByRole('button', { name: 'Save changes' }).click();
    await line.fill(later);
    await expect(page.locator('.a-ok[role="status"]')).toHaveText(/newer changes are not saved/);
    await expect(line).toHaveValue(later);
    await expect(page.locator('.a-savebar .pill.pending')).toBeVisible();
  } finally {
    await page.unroute(slow);
    await line.fill(original);
    await page.getByRole('button', { name: 'Save changes' }).click();
    await expect(page.locator('.a-ok[role="status"]')).toHaveText(/^Saved\.$/);
  }
});

test('a one-per-line list keeps an emptied line where the owner types', async ({ page }) => {
  await signIn(page);
  await page.goto('/admin/site/home');
  const about = page.locator('#sec-about');
  await about.locator('summary').click();
  const skills = about.getByLabel('Skills', { exact: true });
  const lines = (await skills.inputValue()).split('\n');
  expect(lines.length).toBeGreaterThan(2);
  // Select the second line, delete it and type its replacement.
  const start = lines[0].length + 1;
  await skills.evaluate((el: HTMLTextAreaElement, [a, b]) => {
    el.focus();
    el.setSelectionRange(a, b);
  }, [start, start + lines[1].length]);
  await page.keyboard.press('Backspace');
  await expect(skills).toHaveValue([lines[0], '', ...lines.slice(2)].join('\n'));
  await page.keyboard.type('E2E skill');
  await expect(skills).toHaveValue([lines[0], 'E2E skill', ...lines.slice(2)].join('\n'));
});

test('list rows: focus after Remove, and a group reset keeps the years', async ({ page }) => {
  await signIn(page);
  await page.goto('/admin/site/home');
  const hero = page.locator('#sec-hero');
  await hero.locator('summary').click();
  const values = hero.getByLabel('Value', { exact: true });
  const count = await values.count();
  const second = await values.nth(1).inputValue();
  await hero.getByRole('button', { name: 'Remove key number 1' }).click();
  await expect(values).toHaveCount(count - 1);
  // Focus moves into the row that took its place, not onto its Remove button.
  await expect(values.first()).toBeFocused();
  await expect(values.first()).toHaveValue(second);

  // Years of experience is a fact, not site text: resetting the group leaves it alone.
  const years = hero.getByLabel('Years of experience', { exact: true });
  await years.fill('47');
  page.once('dialog', (d) => void d.accept());
  await hero.getByRole('button', { name: /^Reset .* to defaults$/ }).click();
  await expect(page.locator('.a-ok[role="status"]')).toContainText('back to the original text');
  await expect(years).toHaveValue('47');
});

test('legal pages screen offers a retry when loading fails', async ({ page }) => {
  await signIn(page);
  const pages = (u: URL) => u.pathname === '/api/admin/pages';
  await page.route(pages, (r) => r.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"Database unavailable."}' }));
  await page.goto('/admin/site/legal');
  await expect(page.getByText('Could not load the legal pages')).toBeVisible();
  await expect(page.getByText('Loading…')).toHaveCount(0);
  await page.unroute(pages);
  await page.getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByRole('link', { name: 'Edit Privacy notice' })).toBeVisible();
});

test('a missing legal page can be created from the Legal pages screen', async ({ page }) => {
  await signIn(page);
  // As if the privacy notice had been deleted.
  await page.route((u) => u.pathname === '/api/admin/pages' && u.searchParams.get('q') === 'privacy', (r) =>
    r.fulfill({ status: 200, contentType: 'application/json', body: '{"items":[],"total":0}' }));
  await page.goto('/admin/site/legal');
  await expect(page.getByText('This page is missing')).toBeVisible();
  await page.getByRole('link', { name: 'Create Privacy notice' }).click();
  await expect(page.getByLabel('URL slug')).toHaveValue('privacy');
  await expect(page.getByRole('textbox', { name: 'Title', exact: true })).toHaveValue('Privacy notice');
});

test('a failed restore from a legal page version says why', async ({ page }) => {
  await signIn(page);
  await page.goto('/admin/site/legal');
  await page.getByRole('button', { name: 'History of Privacy notice' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByRole('button', { name: 'View' }).first().click();
  await page.route((u) => /^\/api\/admin\/pages\/[^/]+\/versions\/\d+\/restore$/.test(u.pathname), (r) =>
    r.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"Could not restore this version."}' }));
  page.once('dialog', (d) => void d.accept());
  await dialog.getByRole('button', { name: 'Restore this version' }).click();
  await expect(dialog.getByRole('alert')).toContainText('Could not restore this version.');
});

test('the article preview shows the video placeholder text', async ({ page, request }) => {
  await signIn(page);
  await page.goto('/admin/content/new?kind=article');
  await page.locator('.a-hero-pick label', { hasText: 'YouTube' }).click();
  await page.getByRole('button', { name: 'Preview', exact: true }).click();
  const videos = (await (await request.get('/api/public/site-content')).json()).videos;
  await expect(page.locator('.yt-placeholder')).toContainText(videos.placeholder_title);
});

test('non-owner accounts are refused', async ({ request }) => {
  expect((await request.get('/api/admin/me')).status()).toBe(401);
});
