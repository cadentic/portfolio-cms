import { DatePipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { Brand } from '../core/brand';
import { Page } from '../core/models';
import { PublicApi } from '../core/public-api.service';
import { Seo } from '../core/seo.service';
import { NewsletterFormComponent } from '../shared/newsletter-form.component';

const PAGE_SIZE = 12;

@Component({
  selector: 'app-blog-list',
  imports: [RouterLink, DatePipe, NewsletterFormComponent],
  template: `
    <section class="page-hero">
      <div class="wrap">
        <ol class="breadcrumbs"><li><a routerLink="/">Home</a></li><li aria-current="page">Blog</li></ol>
        <h1>{{ tag() ? 'Articles tagged “' + tag() + '”' : 'Notes & insights' }}</h1>
        <p>Oracle DBA, high availability and reliability engineering, written from production experience.</p>
      </div>
    </section>
    <div class="wrap section" style="padding-top:48px">
      @if (loading()) {
        <p aria-live="polite">Loading articles…</p>
      } @else if (items().length === 0) {
        <p>No articles yet. Subscribe below to hear when the first one is published.</p>
      } @else {
        <ul class="article-grid">
          @for (a of items(); track a.id) {
            <li>
              <article class="article-card">
                @if (a.featured_image_url || a.hero.type === 'image') {
                  <img [src]="a.featured_image_url || a.hero.url" [alt]="brand.alt(a.title)" loading="lazy" decoding="async" width="640" height="360" />
                } @else if (a.hero.type === 'youtube' && a.hero.youtube_id) {
                  <img [src]="'https://i.ytimg.com/vi/' + a.hero.youtube_id + '/hqdefault.jpg'" [alt]="brand.alt(a.title)" loading="lazy" width="640" height="360" />
                }
                <div class="body">
                  <div class="date"><time [attr.datetime]="a.published_at">{{ a.published_at | date: 'mediumDate' }}</time> · {{ a.reading_minutes }} min read</div>
                  <h2 style="font-size:1.3rem;margin:4px 0"><a [routerLink]="a.path">{{ a.title }}</a></h2>
                  <p style="font-size:.92rem;margin:0">{{ a.excerpt }}</p>
                </div>
              </article>
            </li>
          }
        </ul>
        <nav class="pager" aria-label="Pagination">
          @if (page() > 1) { <a class="btn btn-outline" routerLink="/blog" [queryParams]="{ page: page() - 1, tag: tag() || null }">← Newer</a> }
          @if (page() * pageSize < total()) { <a class="btn btn-outline" routerLink="/blog" [queryParams]="{ page: page() + 1, tag: tag() || null }">Older →</a> }
        </nav>
      }
      <div style="margin-top:60px"><app-newsletter-form source="blog" /></div>
    </div>
  `,
})
export class BlogListComponent {
  private api = inject(PublicApi);
  private seo = inject(Seo);
  protected brand = inject(Brand);
  readonly pageSize = PAGE_SIZE;
  readonly items = signal<Page[]>([]);
  readonly total = signal(0);
  readonly page = signal(1);
  readonly tag = signal('');
  readonly loading = signal(true);

  constructor() {
    inject(ActivatedRoute).queryParamMap.subscribe((q) => {
      const page = Math.max(1, Number(q.get('page')) || 1);
      this.page.set(page);
      this.tag.set((q.get('tag') ?? '').slice(0, 60));
      this.loading.set(true);
      this.api.articles(PAGE_SIZE, (page - 1) * PAGE_SIZE, this.tag()).subscribe({
        next: (r) => { this.items.set(r.items); this.total.set(r.total); this.loading.set(false); },
        error: () => { this.items.set([]); this.loading.set(false); },
      });
      this.seo.set({
        title: page > 1 ? `Blog, page ${page}` : 'Blog: Oracle DBA notes & insights',
        description: 'Articles on Oracle RAC, Data Guard, RMAN, GoldenGate, EBS and database reliability engineering by Sayantan Chakraborty.',
        path: page > 1 ? `/blog?page=${page}` : '/blog',
        noindex: !!this.tag(),
        jsonLd: [this.seo.breadcrumbs([{ name: 'Home', path: '/' }, { name: 'Blog', path: '/blog' }])],
      });
    });
  }
}
