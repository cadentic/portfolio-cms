import { Component, input } from '@angular/core';
import { Hero } from '../core/models';
import { YoutubeComponent } from './youtube.component';

/** The optional image / video / YouTube section at the top of a page or article. */
@Component({
  selector: 'app-hero-media',
  imports: [YoutubeComponent],
  template: `
    @switch (hero().type) {
      @case ('image') {
        @if (hero().url) {
          <figure class="hero-media">
            <img [src]="hero().url" [alt]="hero().alt || ''" fetchpriority="high" decoding="async" />
            @if (hero().caption) { <figcaption>{{ hero().caption }}</figcaption> }
          </figure>
        }
      }
      @case ('video') {
        @if (hero().url) {
          <figure class="hero-media">
            <video [src]="hero().url" [poster]="hero().poster || null" controls preload="metadata" playsinline></video>
            @if (hero().caption) { <figcaption>{{ hero().caption }}</figcaption> }
          </figure>
        }
      }
      @case ('youtube') {
        <div class="hero-media"><app-youtube [id]="hero().youtube_id" [title]="hero().caption || title()" /></div>
      }
    }
  `,
})
export class HeroMediaComponent {
  readonly hero = input.required<Hero>();
  readonly title = input('');
}
