import { Injectable, inject } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { map } from 'rxjs';
import { DEFAULT_BRAND_SUFFIX, PublicApi } from './public-api.service';

export { DEFAULT_BRAND_SUFFIX };

const norm = (s: string) => s.toLowerCase().replace(/\s+/g, ' ').replace(/[\s.·|,–—-]+$/, '').trim();

/**
 * Appends `suffix` to `text` unless it already ends with it. Text ending in
 * sentence punctuation gets a space, anything else gets `sep`. With `max`, the
 * text (not the suffix) is shortened on a word boundary so the result fits.
 */
export function withBrand(text: string, suffix: string, sep = ' · ', max = 0): string {
  let t = (text ?? '').replace(/\s+/g, ' ').trim();
  if (!suffix) return max && t.length > max ? clip(t, max) : t;
  if (!t) return suffix;
  if (norm(t).endsWith(norm(suffix))) return max && t.length > max ? clip(t, max) : t;
  const join = (s: string) => (/[.!?…]$/.test(s) ? `${s} ` : `${s}${sep}`) + suffix;
  if (max && join(t).length > max) t = clip(t, Math.max(0, max - suffix.length - sep.length));
  return join(t);
}

function clip(t: string, n: number): string {
  if (t.length <= n) return t;
  const cut = t.slice(0, Math.max(0, n - 1));
  const sp = cut.lastIndexOf(' ');
  return (sp > n * 0.6 ? cut.slice(0, sp) : cut).replace(/[\s,;:·|–—-]+$/, '') + '…';
}

const escAttr = (s: string) => s.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
const unescAttr = (s: string) =>
  s.replace(/&quot;/g, '"').replace(/&#39;/g, "'").replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&');

/** Brands the alt attribute of every <img> in an HTML fragment (adding one where it is missing). */
export function withBrandAlts(html: string, suffix: string): string {
  if (!html || !suffix) return html ?? '';
  return html.replace(/<img\b[^>]*>/gi, (tag) => {
    const m = /\salt\s*=\s*("([^"]*)"|'([^']*)'|([^\s"'>]+))/i.exec(tag);
    if (!m) return tag.replace(/\s*\/?>$/, (end) => ` alt="${escAttr(suffix)}"${end}`);
    const current = unescAttr(m[2] ?? m[3] ?? m[4] ?? '');
    return tag.slice(0, m.index) + ` alt="${escAttr(withBrand(current, suffix))}"` + tag.slice(m.index + m[0].length);
  });
}

/** The owner-editable brand suffix, loaded with the public site config. */
@Injectable({ providedIn: 'root' })
export class Brand {
  readonly suffix = toSignal(inject(PublicApi).site().pipe(map((c) => c.brand_suffix ?? DEFAULT_BRAND_SUFFIX)),
    { initialValue: DEFAULT_BRAND_SUFFIX });

  /** Alt text for an image, ending with the brand suffix. Empty input gives the suffix alone. */
  alt(text?: string | null): string {
    return withBrand(text ?? '', this.suffix());
  }

  /** The same for every <img> inside trusted, already-sanitised HTML. */
  html(html?: string | null): string {
    return withBrandAlts(html ?? '', this.suffix());
  }
}
