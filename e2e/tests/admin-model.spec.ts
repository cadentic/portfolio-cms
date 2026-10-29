import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from '@playwright/test';
import type { SiteContent } from '../../frontend/src/app/core/site-content';
import { findMatches, remapRows, replaceMatches } from '../../frontend/src/app/admin/site/site-content.model';

// The admin's find & replace and row bookkeeping, on the real default site content.
// Plain functions: no browser or running stack needed.
const defaults = (): SiteContent =>
  JSON.parse(readFileSync(join(__dirname, '../../backend/internal/sitecontent/testdata/defaults.golden.json'), 'utf8'));
const opts = { wholeWord: true, caseSensitive: false };
/** Every text value of the document, keys left out. */
const text = (v: unknown): string =>
  typeof v === 'string' ? v + '\n' : v && typeof v === 'object' ? Object.values(v).map(text).join('') : '';
const count = (s: string, re: RegExp) => s.match(re)?.length ?? 0;

test('find never cuts into the {years} placeholder', () => {
  const c = defaults();
  const all = count(text(c), /\byears\b/gi);
  const placeholders = count(text(c), /\{years\}/g);
  expect(placeholders).toBeGreaterThan(0);

  const ms = findMatches(c, 'years', opts);
  expect(ms).toHaveLength(all - placeholders);
  for (const m of ms) expect(m.before.endsWith('{') || m.after.startsWith('}'), m.path).toBe(false);

  replaceMatches(c, ms, 'yr.');
  expect(c.hero.metrics[0].value).toBe('{years}+ yr.');
  expect(count(text(c), /\{years\}/g)).toBe(placeholders);
  expect(text(c)).not.toContain('{yr.}');
});

test('find can still replace a whole placeholder', () => {
  const c = defaults();
  const ms = findMatches(c, '{years}', opts);
  expect(ms.length).toBe(count(text(c), /\{years\}/g));
  replaceMatches(c, ms, '12');
  expect(c.hero.metrics[0].value).toBe('12+ years');
});

test('find skips the country code', () => {
  const c = defaults();
  expect(c.identity.country_code).toBe('IN');
  const paths = findMatches(c, 'in', opts).map((m) => m.path);
  expect(paths.length).toBeGreaterThan(0);
  expect(paths).not.toContain('identity.country_code');
});

test('messages follow their row when rows are removed or moved', () => {
  const msgs = [
    { path: 'organizations.items[1].logo_url', message: 'a' },
    { path: 'organizations.items[3].url', message: 'b' },
    { path: 'organizations.items', message: 'c' },
    { path: 'organizations.label', message: 'd' },
    { path: 'hero.metrics[3].value', message: 'e' },
  ];
  // Row 1 of 5 removed: rows 2-4 move up one.
  expect(remapRows(msgs, 'organizations.items', [0, 2, 3, 4]).map((m) => m.path)).toEqual([
    'organizations.items[2].url',
    'organizations.items',
    'organizations.label',
    'hero.metrics[3].value',
  ]);
  // Rows 1 and 2 swapped.
  expect(remapRows(msgs, 'organizations.items', [0, 2, 1, 3, 4]).map((m) => m.path)).toEqual([
    'organizations.items[2].logo_url',
    'organizations.items[3].url',
    'organizations.items',
    'organizations.label',
    'hero.metrics[3].value',
  ]);
  // "items[1" is not "items[10".
  expect(remapRows([{ path: 'faq.items[10].q', message: 'x' }], 'faq.items', [...Array(11).keys()].filter((k) => k !== 1)))
    .toEqual([{ path: 'faq.items[9].q', message: 'x' }]);
});
