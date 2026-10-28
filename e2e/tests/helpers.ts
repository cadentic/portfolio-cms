import AxeBuilder from '@axe-core/playwright';
import { expect, type Page } from '@playwright/test';

/** Collects console errors and failed same-origin requests while a test runs. */
export function watchErrors(page: Page, origin: string) {
  const errors: string[] = [];
  page.on('console', (m) => {
    // Third-party fetches (YouTube thumbnails) may be blocked in CI networks.
    if (m.type() === 'error' && !m.text().startsWith('Failed to load resource')) errors.push(m.text());
  });
  page.on('pageerror', (e) => errors.push('pageerror: ' + e.message));
  page.on('response', (r) => {
    // API 4xx answers (an invalid token, say) are handled by the page; 5xx and missing assets are bugs.
    const limit = r.url().startsWith(origin + '/api/') ? 500 : 400;
    if (r.status() >= limit && r.url().startsWith(origin) && r.request().resourceType() !== 'document') errors.push(`${r.status()} ${r.url()}`);
  });
  return errors;
}

/** WCAG 2.1 A/AA checks; serious and critical violations fail the test. */
export async function expectAccessible(page: Page, exclude: string[] = []) {
  let builder = new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']);
  for (const sel of exclude) builder = builder.exclude(sel);
  const { violations } = await builder.analyze();
  const bad = violations.filter((v) => v.impact === 'serious' || v.impact === 'critical');
  expect(bad.map((v) => `${v.id}: ${v.help} (${v.nodes.map((n) => n.target.join(' ')).slice(0, 3).join(', ')})`)).toEqual([]);
}

export async function expectNoHorizontalScroll(page: Page) {
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(overflow, 'page scrolls horizontally').toBeLessThanOrEqual(1);
}
