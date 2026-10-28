import { DOCUMENT } from '@angular/common';
import { Injectable, inject } from '@angular/core';
import { Meta, Title } from '@angular/platform-browser';
import { PROFILE } from './profile';
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

/** Sets title, description, canonical, Open Graph, Twitter and JSON-LD for SSR. */
@Injectable({ providedIn: 'root' })
export class Seo {
  private title = inject(Title);
  private meta = inject(Meta);
  private doc = inject(DOCUMENT);
  readonly siteUrl = inject(SITE_URL).replace(/\/$/, '');

  abs(path: string): string {
    if (!path) return this.siteUrl;
    if (/^https?:\/\//.test(path)) return path;
    return this.siteUrl + (path.startsWith('/') ? path : '/' + path);
  }

  set(s: SeoInput): void {
    const fullTitle = s.title.includes(PROFILE.name) ? s.title : `${s.title} · ${PROFILE.name}`;
    const url = this.abs(s.canonical || s.path);
    const image = this.abs(s.image || PROFILE.photo);
    const description = s.description.slice(0, 300);
    this.title.setTitle(fullTitle);
    const tags: Array<[string, string, string]> = [
      ['name', 'description', description],
      ['name', 'robots', s.noindex ? 'noindex, nofollow' : 'index, follow, max-image-preview:large, max-snippet:-1'],
      ['property', 'og:site_name', PROFILE.name],
      ['property', 'og:type', s.type ?? 'website'],
      ['property', 'og:title', fullTitle],
      ['property', 'og:description', description],
      ['property', 'og:url', url],
      ['property', 'og:image', image],
      ['property', 'og:image:alt', s.title],
      ['property', 'og:locale', 'en_IN'],
      ['name', 'twitter:card', s.image ? 'summary_large_image' : 'summary'],
      ['name', 'twitter:title', fullTitle],
      ['name', 'twitter:description', description],
      ['name', 'twitter:image', image],
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
      this.meta.addTag({ property: 'article:author', content: PROFILE.name });
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
    return { '@type': 'WebSite', '@id': this.siteUrl + '/#website', url: this.siteUrl + '/', name: PROFILE.name,
      inLanguage: 'en-IN', publisher: { '@id': this.siteUrl + '/#person' } };
  }

  person() {
    return {
      '@type': 'Person', '@id': this.siteUrl + '/#person', name: PROFILE.name, url: this.siteUrl + '/',
      image: this.abs(PROFILE.photo), jobTitle: PROFILE.shortRole, description: PROFILE.summary,
      address: { '@type': 'PostalAddress', addressLocality: 'Bangalore', addressCountry: 'IN' },
      sameAs: [PROFILE.linkedin, PROFILE.github, PROFILE.youtube],
      knowsAbout: PROFILE.skills,
      knowsLanguage: PROFILE.languages,
      alumniOf: PROFILE.education.map((e) => ({ '@type': 'CollegeOrUniversity', name: e.school })),
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

  video(id: string, name: string, description: string, uploadDate?: string | null) {
    return { '@type': 'VideoObject', name, description: description || name,
      thumbnailUrl: `https://i.ytimg.com/vi/${id}/hqdefault.jpg`, embedUrl: `https://www.youtube-nocookie.com/embed/${id}`,
      contentUrl: `https://www.youtube.com/watch?v=${id}`, uploadDate: uploadDate ?? undefined };
  }
}
