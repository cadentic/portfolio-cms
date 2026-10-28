import { Component, computed, inject, input, signal } from '@angular/core';
import { DomSanitizer } from '@angular/platform-browser';

/**
 * Click-to-load YouTube embed: no third-party requests or cookies until the
 * visitor presses play (privacy-enhanced youtube-nocookie.com domain).
 */
@Component({
  selector: 'app-youtube',
  template: `
    @if (id()) {
      <div class="yt" [style.background-image]="playing() ? null : 'url(' + thumb() + ')'">
        @if (playing()) {
          <iframe [src]="src()" [title]="title()" loading="lazy"
            allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture" allowfullscreen></iframe>
        } @else {
          <button type="button" (click)="playing.set(true)" [attr.aria-label]="'Play video: ' + title()">
            <span class="play" aria-hidden="true"></span>
            <span class="yt-title">{{ title() }}</span>
          </button>
        }
      </div>
    } @else {
      <div class="yt-placeholder">
        <div>
          <strong>Video coming soon</strong>
          <p>Watch the latest Oracle DBA walkthroughs on <a href="https://www.youtube.com/@csayantan" target="_blank" rel="noopener">YouTube &#64;csayantan</a>.</p>
        </div>
      </div>
    }
  `,
})
export class YoutubeComponent {
  readonly id = input<string | undefined>('');
  readonly title = input('Video');
  readonly playing = signal(false);
  private sanitizer = inject(DomSanitizer);
  readonly thumb = computed(() => `https://i.ytimg.com/vi/${this.id()}/hqdefault.jpg`);
  // The id is validated to [A-Za-z0-9_-]{11} by the API before it reaches here.
  readonly src = computed(() => {
    const id = (this.id() ?? '').replace(/[^A-Za-z0-9_-]/g, '');
    return this.sanitizer.bypassSecurityTrustResourceUrl(`https://www.youtube-nocookie.com/embed/${id}?autoplay=1&rel=0`);
  });

}
