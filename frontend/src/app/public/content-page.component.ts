import { DatePipe } from '@angular/common';
import { Component, inject, input, signal } from '@angular/core';
import { ActivatedRoute, RouterLink, UrlSegment } from '@angular/router';
import { Observable, TimeoutError, catchError, of, switchMap } from 'rxjs';
import { httpStatus } from '../core/http-status';
import { Page } from '../core/models';
import { PublicApi } from '../core/public-api.service';
import { Seo } from '../core/seo.service';
import { SiteContentService } from '../core/site-content.service';
import { BlocksComponent } from '../shared/blocks.component';
import { HeroMediaComponent } from '../shared/hero-media.component';
import { NewsletterFormComponent } from '../shared/newsletter-form.component';

/** Renders a published article (/blog/:slug) or CMS page (/any/nested/path). */
@Component({
  selector: 'app-content-page',
  imports: [DatePipe, RouterLink, BlocksComponent, HeroMediaComponent, NewsletterFormComponent],
  template: `
    @if (page(); as pg) {
      <article>
        <header class="page-hero">
          <div class="wrap" style="max-width:900px">
            <ol class="breadcrumbs">
              <li><a routerLink="/">Home</a></li>
              @if (pg.kind === 'article') { <li><a routerLink="/blog">Blog</a></li> }
              <li aria-current="page">{{ pg.title }}</li>
            </ol>
            <h1>{{ pg.hero.heading || pg.title }}</h1>
            @if (pg.kind === 'page') {
              <!-- Fixed offset, so the server and the browser print the same date. -->
              <p class="meta">Last updated <time [attr.datetime]="pg.updated_at">{{ pg.updated_at | date: 'd MMMM y' : '+0530' }}</time></p>
            }
            @if (pg.hero.subheading || pg.excerpt) { <p>{{ pg.hero.subheading || pg.excerpt }}</p> }
            @if (pg.kind === 'article') {
              <div class="meta">
                <span>By {{ pg.author_name || site.content().identity.name }}</span>
                @if (pg.published_at) { <time [attr.datetime]="pg.published_at">{{ pg.published_at | date: 'longDate' }}</time> }
                <span>{{ pg.reading_minutes }} min read</span>
              </div>
            }
            <app-hero-media [hero]="pg.hero" [title]="pg.title" />
          </div>
        </header>
        <div class="wrap">
          <div class="prose">
            <app-blocks [blocks]="pg.blocks" />
            @if (pg.tags.length) {
              <ul class="tags" aria-label="Tags">@for (t of pg.tags; track t) { <li><a class="chip" routerLink="/blog" [queryParams]="{ tag: t }">#{{ t }}</a></li> }</ul>
            }
          </div>
          @if (pg.kind === 'article') { <div style="max-width:900px;margin:0 auto 80px"><app-newsletter-form source="blog" /></div> }
        </div>
      </article>
    } @else if (unavailable()) {
      <section class="center-card" aria-labelledby="ua-title">
        <h1 id="ua-title" style="font-size:2.4rem">Temporarily unavailable</h1>
        <p>This page could not be loaded just now. Please try again in a minute.</p>
        <a class="btn btn-primary" [href]="retry()">Try again</a>
      </section>
    } @else if (missing()) {
      <section class="center-card" aria-labelledby="nf-title">
        <div class="eyebrow">404</div>
        <h1 id="nf-title" style="font-size:2.4rem">Page not found</h1>
        <p>The page you're looking for doesn't exist or hasn't been published yet.</p>
        <a class="btn btn-primary" routerLink="/">Go to the homepage</a>
        <a class="btn btn-outline" routerLink="/blog" style="margin-left:8px">Browse articles</a>
      </section>
    } @else {
      <p class="wrap section" aria-live="polite">Loading…</p>
    }
  `,
})
export class ContentPageComponent {
  readonly kind = input<'article' | 'page'>('page');
  readonly site = inject(SiteContentService);
  private api = inject(PublicApi);
  private seo = inject(Seo);
  private status = httpStatus();
  readonly page = signal<Page | null>(null);
  readonly missing = signal(false);
  readonly unavailable = signal(false);
  readonly retry = signal('/');

  constructor() {
    const route = inject(ActivatedRoute);
    route.url
      .pipe(
        switchMap((segs: UrlSegment[]) => {
          this.page.set(null);
          this.missing.set(false);
          this.unavailable.set(false);
          const path = segs.map((s) => s.path).join('/');
          const isArticle = route.snapshot.data['kind'] === 'article';
          const req: Observable<Page> = isArticle ? this.api.article(route.snapshot.paramMap.get('slug') ?? '') : this.api.page(path);
          const fullPath = '/' + (isArticle ? 'blog/' + route.snapshot.paramMap.get('slug') : path);
          return req.pipe(catchError((e) => (isDown(e) ? this.down(fullPath) : this.tryRedirect(fullPath))));
        }),
      )
      .subscribe((pg) => {
        if (!pg) return;
        this.page.set(pg);
        this.applySeo(pg);
      });
  }

  private tryRedirect(path: string): Observable<Page | null> {
    return this.api.redirect(path).pipe(
      switchMap((r) => {
        this.status.redirect(r.to, r.status);
        return of(null);
      }),
      catchError((e) => {
        if (isDown(e)) return this.down(path);
        this.missing.set(true);
        this.status.notFound();
        this.seo.set({ title: 'Page not found', description: 'This page does not exist.', path, noindex: true });
        return of(null);
      }),
    );
  }

  /** The API is down or too slow (D16): 503, never cached, never indexed. */
  private down(path: string): Observable<null> {
    this.retry.set(path);
    this.unavailable.set(true);
    this.status.unavailable();
    this.seo.set({ title: 'Temporarily unavailable', description: 'This page could not be loaded. Please try again.', path, noindex: true });
    return of(null);
  }

  private applySeo(pg: Page) {
    const crumbs = [{ name: 'Home', path: '/' }];
    if (pg.kind === 'article') crumbs.push({ name: 'Blog', path: '/blog' });
    crumbs.push({ name: pg.title, path: pg.path });
    const image = pg.seo.og_image || pg.featured_image_url || (pg.hero.type === 'image' ? pg.hero.url : '') ||
      (pg.hero.youtube_id ? `https://i.ytimg.com/vi/${pg.hero.youtube_id}/hqdefault.jpg` : '');
    const description = pg.seo.description || pg.excerpt || pg.title;
    const ld: object[] = [this.seo.breadcrumbs(crumbs), this.seo.person()];
    if (pg.kind === 'article') {
      ld.push({
        '@type': 'BlogPosting', '@id': this.seo.abs(pg.path) + '#article', headline: pg.title, description,
        url: this.seo.abs(pg.path), mainEntityOfPage: this.seo.abs(pg.path), datePublished: pg.first_published_at || pg.published_at,
        dateModified: pg.updated_at, author: { '@id': this.seo.siteUrl + '/#person' }, publisher: { '@id': this.seo.siteUrl + '/#person' },
        image: image ? this.seo.abs(image) : undefined, keywords: pg.tags.join(', ') || undefined, articleSection: pg.categories[0],
        wordCount: pg.reading_minutes * 220, inLanguage: 'en-IN',
      });
    } else {
      ld.push({ '@type': 'WebPage', '@id': this.seo.abs(pg.path), name: pg.title, description, url: this.seo.abs(pg.path) });
    }
    if (pg.hero.type === 'youtube' && pg.hero.youtube_id && pg.published_at) {
      ld.push(this.seo.video(pg.hero.youtube_id, pg.hero.caption || pg.title, description, pg.published_at));
    }
    this.seo.set({
      title: pg.seo.title || pg.title, description, path: pg.path, image, canonical: pg.seo.canonical,
      type: pg.kind === 'article' ? 'article' : 'website', noindex: pg.seo.noindex,
      publishedTime: pg.first_published_at || pg.published_at, modifiedTime: pg.updated_at, tags: pg.tags, jsonLd: ld,
    });
  }
}

/** Network failure (status 0), a 5xx or the request timeout, as opposed to a real 404. */
function isDown(e: unknown): boolean {
  const status = (e as { status?: number })?.status;
  return e instanceof TimeoutError || status === 0 || (status ?? 0) >= 500;
}
