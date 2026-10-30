import { Component, computed, inject, signal } from '@angular/core';
import { RouterLink } from '@angular/router';
import { Page } from '../core/models';
import { PublicApi } from '../core/public-api.service';
import { Seo } from '../core/seo.service';
import { SiteContentService } from '../core/site-content.service';
import { YoutubeComponent } from '../shared/youtube.component';

interface VideoRow {
  id: string;
  title: string;
  description: string;
  uploadDate: string;
  path?: string;
}

/** The curated video list, then (optionally) articles whose hero is a YouTube video (D19). */
@Component({
  selector: 'app-videos',
  imports: [RouterLink, YoutubeComponent],
  template: `
    @let v = site.content().videos;
    @let channel = site.content().identity.youtube_url;
    <section class="page-hero">
      <div class="wrap">
        <ol class="breadcrumbs"><li><a routerLink="/">Home</a></li><li aria-current="page">Videos</li></ol>
        <h1>{{ v.page_heading || 'Videos' }}</h1>
        @if (v.page_intro) { <p>{{ v.page_intro }}</p> }
        @if (channel && v.page_cta_label) { <a class="btn btn-primary" [href]="channel" target="_blank" rel="me noopener">{{ v.page_cta_label }} ↗</a> }
      </div>
    </section>
    <div class="wrap section" style="padding-top:48px">
      @if (videos().length) {
        <ul class="article-grid">
          @for (row of videos(); track $index) {
            <li>
              <article class="article-card">
                <app-youtube [id]="row.id" [title]="row.title" />
                <div class="body">
                  <h2 style="font-size:1.2rem;margin:4px 0">@if (row.path) {<a [routerLink]="row.path">{{ row.title }}</a>} @else { {{ row.title }} }</h2>
                  @if (row.description) { <p style="font-size:.92rem;margin:0">{{ row.description }}</p> }
                </div>
              </article>
            </li>
          }
        </ul>
      } @else if (loading()) {
        <p aria-live="polite">Loading videos…</p>
      } @else if (v.empty_text) {
        <p>{{ v.empty_text }}</p>
      }
    </div>
  `,
})
export class VideosComponent {
  readonly site = inject(SiteContentService);
  private seo = inject(Seo);
  readonly items = signal<Page[]>([]);
  readonly loading = signal(true);
  readonly videos = computed(() => {
    const v = this.site.content().videos;
    const seen = new Set<string>();
    const rows: VideoRow[] = [];
    for (const x of v.items) {
      if (!x.youtube_id || seen.has(x.youtube_id)) continue;
      seen.add(x.youtube_id);
      rows.push({ id: x.youtube_id, title: x.title, description: x.description, uploadDate: x.published_on });
    }
    if (!v.include_article_videos) return rows;
    for (const a of this.items()) {
      const id = a.hero?.type === 'youtube' ? a.hero.youtube_id : '';
      if (!id || seen.has(id)) continue;
      seen.add(id);
      rows.push({ id, title: a.title, description: a.excerpt, uploadDate: a.published_at ?? '', path: a.path });
    }
    return rows;
  });

  constructor() {
    if (!this.site.content().videos.include_article_videos) {
      this.loading.set(false);
      this.setSeo();
      return;
    }
    inject(PublicApi).articles(50, 0).subscribe({
      next: (r) => { this.items.set(r.items); this.loading.set(false); this.setSeo(); },
      error: () => { this.loading.set(false); this.setSeo(); },
    });
  }

  private setSeo() {
    const s = this.site.content().seo;
    this.seo.set({
      title: s.videos_title,
      description: s.videos_description,
      path: '/videos',
      jsonLd: [
        this.seo.breadcrumbs([{ name: 'Home', path: '/' }, { name: 'Videos', path: '/videos' }]),
        // A VideoObject needs an upload date (D19).
        ...this.videos()
          .filter((r) => r.uploadDate)
          .map((r) => this.seo.video(r.id, r.title, r.description, r.uploadDate)),
      ],
    });
  }
}
