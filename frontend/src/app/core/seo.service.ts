import { DOCUMENT } from '@angular/common';
import { Injectable, inject } from '@angular/core';
import { Meta, Title } from '@angular/platform-browser';
import { Brand, withBrand } from './brand';
import { PublicApi } from './public-api.service';
import { profileLinks } from './site-content';
import { SiteContentService } from './site-content.service';
import { SITE_URL } from './tokens';

export interface SeoInput {
  title: string;
  description: string;
  path: string;
  image?: string;
  type?: 'website' | 'article' | 'profile';
  noindex?: boolean;
  canonical?: string;
  publishedTime?: string | null;
  modifiedTime?: string | null;
  tags?: string[];
  jsonLd?: object[];
}

/**
 * Sets title, description, canonical, Open Graph, Twitter and JSON-LD for SSR.
 * Every title, description and image alt ends with the owner's brand suffix
 * (Admin › SEO & branding), applied here so page and CMS text never repeat it.
 */
@Injectable({ providedIn: 'root' })
export class Seo {
  private title = inject(Title);
  private meta = inject(Meta);
  private doc = inject(DOCUMENT);
  private site = inject(SiteContentService);
  private brand = inject(Brand);
  private last?: SeoInput;
  readonly siteUrl = inject(SITE_URL).replace(/\/$/, '');

  constructor() {
    // Re-apply once the saved suffix arrives (the default is used until then).
    inject(PublicApi).site().subscribe(() => this.last && this.set(this.last));
  }

  abs(path: string): string {
    if (!path) return this.siteUrl;
    if (/^https?:\/\//.test(path)) return path;
    return this.siteUrl + (path.startsWith('/') ? path : '/' + path);
  }

  set(s: SeoInput): void {
    this.last = s;
    const id = this.site.content().identity;
    const suffix = this.brand.suffix();
    const fullTitle = suffix ? withBrand(s.title, suffix)
      : !id.name || s.title.includes(id.name) ? s.title : `${s.title} · ${id.name}`;
    const url = this.abs(s.canonical || s.path);
    const image = this.abs(s.image || id.photo_url);
    const description = withBrand(s.description, suffix, ' · ', 300);
    const imageAlt = this.brand.alt(s.title);
    this.title.setTitle(fullTitle);
    const tags: Array<[string, string, string]> = [
      ['name', 'description', description],
      ['name', 'robots', s.noindex ? 'noindex, nofollow' : 'index, follow, max-image-preview:large, max-snippet:-1'],
      ['property', 'og:site_name', id.name],
      ['property', 'og:type', s.type ?? 'website'],
      ['property', 'og:title', fullTitle],
      ['property', 'og:description', description],
      ['property', 'og:url', url],
      ['property', 'og:image', image],
      ['property', 'og:image:alt', imageAlt],
      ['property', 'og:locale', 'en_IN'],
      ['name', 'twitter:card', s.image ? 'summary_large_image' : 'summary'],
      ['name', 'twitter:title', fullTitle],
      ['name', 'twitter:description', description],
      ['name', 'twitter:image', image],
      ['name', 'twitter:image:alt', imageAlt],
    ];
    for (const [attr, key, content] of tags) {
      this.meta.updateTag({ [attr]: key, content } as never, `${attr}="${key}"`);
    }
    for (const k of ['article:published_time', 'article:modified_time', 'article:author']) {
      this.meta.removeTag(`property="${k}"`);
    }
    this.meta.removeTag('property="article:tag"');
    if (s.type === 'article') {
      if (s.publishedTime) this.meta.addTag({ property: 'article:published_time', content: s.publishedTime });
      if (s.modifiedTime) this.meta.addTag({ property: 'article:modified_time', content: s.modifiedTime });
      this.meta.addTag({ property: 'article:author', content: id.name });
      for (const t of s.tags ?? []) this.meta.addTag({ property: 'article:tag', content: t });
    }
    this.setLink('canonical', url);
    this.setJsonLd([this.website(), ...(s.jsonLd ?? [])]);
  }

  private setLink(rel: string, href: string) {
    let el = this.doc.head.querySelector<HTMLLinkElement>(`link[rel="${rel}"]`);
    if (!el) {
      el = this.doc.createElement('link');
      el.setAttribute('rel', rel);
      this.doc.head.appendChild(el);
    }
    el.setAttribute('href', href);
  }

  private setJsonLd(items: object[]) {
    this.doc.head.querySelectorAll('script[data-seo="ld"]').forEach((n) => n.remove());
    const el = this.doc.createElement('script');
    el.setAttribute('type', 'application/ld+json');
    el.setAttribute('data-seo', 'ld');
    // Escape "<" so content can never close the script element.
    el.textContent = JSON.stringify({ '@context': 'https://schema.org', '@graph': items }).replace(/</g, '\\u003c');
    this.doc.head.appendChild(el);
  }

  website() {
    return { '@type': 'WebSite', '@id': this.siteUrl + '/#website', url: this.siteUrl + '/',
      name: this.site.content().identity.name, inLanguage: 'en-IN', publisher: { '@id': this.siteUrl + '/#person' } };
  }

  person() {
    const c = this.site.content();
    const id = c.identity;
    // Empty values are left out (JSON.stringify drops undefined).
    const list = <T>(xs: T[]) => (xs.length ? xs : undefined);
    return {
      '@type': 'Person', '@id': this.siteUrl + '/#person', name: id.name, url: this.siteUrl + '/',
      image: this.abs(id.photo_url), jobTitle: id.short_role || undefined, description: id.summary || undefined,
      address: id.city || id.country_code
        ? { '@type': 'PostalAddress', addressLocality: id.city || undefined, addressCountry: id.country_code || undefined }
        : undefined,
      sameAs: list(profileLinks(c).map((l) => l.url)),
      knowsAbout: list(c.about.skills),
      knowsLanguage: list(id.languages),
      alumniOf: list(id.education.map((e) => ({ '@type': 'CollegeOrUniversity', name: e.school }))),
    };
  }

  breadcrumbs(items: Array<{ name: string; path: string }>) {
    return { '@type': 'BreadcrumbList', itemListElement: items.map((it, i) => ({
      '@type': 'ListItem', position: i + 1, name: it.name, item: this.abs(it.path) })) };
  }

  faq(items: Array<{ q: string; a: string }>) {
    return { '@type': 'FAQPage', mainEntity: items.map((f) => ({ '@type': 'Question', name: f.q,
      acceptedAnswer: { '@type': 'Answer', text: f.a } })) };
  }

  /** Google requires uploadDate, so callers emit a VideoObject only when they have one (D19). */
  video(id: string, name: string, description: string, uploadDate: string) {
    return { '@type': 'VideoObject', name, description: description || name,
      thumbnailUrl: `https://i.ytimg.com/vi/${id}/hqdefault.jpg`, embedUrl: `https://www.youtube-nocookie.com/embed/${id}`,
      contentUrl: `https://www.youtube.com/watch?v=${id}`, uploadDate };
  }
}
