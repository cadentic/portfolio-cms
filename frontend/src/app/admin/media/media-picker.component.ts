import { Component, inject, input, output, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MediaItem } from '../../core/models';
import { AdminApi, errMsg } from '../admin-api.service';
import { ACCEPT, MediaKind, displayName, uploadMedia } from './media-upload';

/**
 * Upload a new file or choose one from the media library. Emits the chosen
 * item; the caller copies its /media/… URL into the field it belongs to.
 */
@Component({
  selector: 'app-media-picker',
  imports: [FormsModule],
  template: `
    <span class="a-row" style="gap:6px;flex-wrap:wrap">
      <input #fileInput type="file" class="sr-only" tabindex="-1" aria-hidden="true" [accept]="accept[kind()]" (change)="upload($event)" />
      <button class="ab sm" type="button" [disabled]="progress() !== null" (click)="fileInput.click()">
        {{ progress() !== null ? 'Uploading ' + progress() + '%' : label() }}</button>
      <button class="ab sm" type="button" (click)="openLibrary()">Choose from library</button>
    </span>
    @if (error()) { <p class="a-err" role="alert" style="margin:4px 0 0">{{ error() }}</p> }

    @if (open()) {
      <div class="a-modal-bg" (click)="$event.target === $event.currentTarget ? open.set(false) : null">
        <div class="a-modal wide a-form" role="dialog" aria-modal="true" aria-labelledby="mp-t">
          <div class="a-row between"><h3 id="mp-t" style="margin:0">Media library</h3>
            <button class="ab sm" type="button" (click)="open.set(false)" aria-label="Close">✕</button></div>
          <input type="search" [(ngModel)]="q" (keyup.enter)="load()" placeholder="Search by name or alt text" aria-label="Search media" />
          <div class="a-media-grid">
            @for (m of items(); track m.id) {
              <button type="button" class="a-media-tile" (click)="choose(m)" [title]="displayName(m)">
                @if (m.kind === 'image') { <img [src]="m.url" [alt]="m.alt" loading="lazy" /> }
                @else { <span class="a-media-icon">{{ m.kind === 'video' ? '▶' : 'PDF' }}</span> }
                <span class="a-media-name">{{ displayName(m) }}</span>
              </button>
            } @empty { <p class="a-muted">{{ loading() ? 'Loading…' : 'No files yet. Upload one first.' }}</p> }
          </div>
        </div>
      </div>
    }
  `,
})
export class MediaPickerComponent {
  private api = inject(AdminApi);
  /** image, video, file (PDF), inline (image or PDF, for body blocks) or any. */
  readonly kind = input<MediaKind | 'any' | 'inline'>('image');
  readonly label = input('Upload…');
  readonly alt = input('');
  readonly picked = output<MediaItem>();
  readonly progress = signal<number | null>(null);
  readonly error = signal('');
  readonly open = signal(false);
  readonly loading = signal(false);
  readonly items = signal<MediaItem[]>([]);
  readonly accept = ACCEPT;
  readonly displayName = displayName;
  q = '';

  upload(ev: Event) {
    const el = ev.target as HTMLInputElement;
    const file = el.files?.[0];
    el.value = '';
    if (!file) return;
    this.error.set('');
    this.progress.set(0);
    uploadMedia(this.api, file, this.alt(), (p) => this.progress.set(p)).subscribe({
      next: (m) => { this.progress.set(null); this.picked.emit(m); },
      error: (e) => { this.progress.set(null); this.error.set(errMsg(e)); },
    });
  }
  openLibrary() {
    this.open.set(true);
    this.load();
  }
  load() {
    const k = this.kind();
    this.loading.set(true);
    this.api.get<{ items: MediaItem[] }>('media', { kind: k === 'any' || k === 'inline' ? '' : k, q: this.q.trim(), limit: 120 }).subscribe({
      next: (r) => {
        this.loading.set(false);
        this.items.set((r.items ?? []).filter((m) => k !== 'inline' || m.kind !== 'video'));
      },
      error: (e) => { this.loading.set(false); this.error.set(errMsg(e)); this.open.set(false); },
    });
  }
  choose(m: MediaItem) {
    this.open.set(false);
    this.picked.emit(m);
  }
}
