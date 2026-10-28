import { Component, computed, inject, signal } from '@angular/core';
import { RouterLink } from '@angular/router';
import { Page } from '../core/models';
import { PROFILE } from '../core/profile';
import { PublicApi } from '../core/public-api.service';
import { Seo } from '../core/seo.service';
import { YoutubeComponent } from '../shared/youtube.component';

/** Videos are articles whose hero is a YouTube video, plus a link to the channel. */
@Component({
  selector: 'app-videos',
  imports: [RouterLink, YoutubeComponent],
  template: `
    <section class="page-hero">
      <div class="wrap">
        <ol class="breadcrumbs"><li><a routerLink="/">Home</a></li><li aria-current="page">Videos</li></ol>
        <h1>Videos</h1>
        <p>Walkthroughs on Oracle RAC, Data Guard, RMAN recovery and day-to-day DBA work, from the YouTube channel &#64;csayantan.</p>
        <a class="btn btn-primary" [href]="p.youtube" target="_blank" rel="me noopener">Open the channel on YouTube ↗</a>
      </div>
    </section>
    <div class="wrap section" style="padding-top:48px">
      @if (loading()) {
        <p aria-live="polite">Loading videos…</p>
      } @else if (videos().length === 0) {
        <p>New videos appear here as they are published. Until then, visit the channel above.</p>
      } @else {
        <ul class="article-grid">
          @for (v of videos(); track v.id) {
            <li>
              <article class="article-card">
                <app-youtube [id]="v.hero.youtube_id" [title]="v.title" />
                <div class="body">
                  <h2 style="font-size:1.2rem;margin:4px 0"><a [routerLink]="v.path">{{ v.title }}</a></h2>
                  <p style="font-size:.92rem;margin:0">{{ v.excerpt }}</p>
                </div>
              </article>
            </li>
          }
        </ul>
      }
    </div>
  `,
})
export class VideosComponent {
  private seo = inject(Seo);
  readonly p = PROFILE;
  readonly items = signal<Page[]>([]);
  readonly loading = signal(true);
  readonly videos = computed(() => this.items().filter((a) => a.hero?.type === 'youtube' && a.hero.youtube_id));

  constructor() {
    inject(PublicApi).articles(50, 0).subscribe({
      next: (r) => { this.items.set(r.items); this.loading.set(false); this.setSeo(); },
      error: () => { this.loading.set(false); this.setSeo(); },
    });
  }
  private setSeo() {
    this.seo.set({
      title: 'Videos: Oracle DBA walkthroughs',
      description: 'Video walkthroughs on Oracle RAC, Data Guard, RMAN, GoldenGate and database reliability by Sayantan Chakraborty (YouTube @csayantan).',
      path: '/videos',
      jsonLd: [
        this.seo.breadcrumbs([{ name: 'Home', path: '/' }, { name: 'Videos', path: '/videos' }]),
        ...this.videos().map((v) => this.seo.video(v.hero.youtube_id!, v.title, v.excerpt, v.published_at)),
      ],
    });
  }
}
