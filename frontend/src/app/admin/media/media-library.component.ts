import { DatePipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MediaItem } from '../../core/models';
import { AdminApi, errMsg } from '../admin-api.service';
import { ACCEPT, displayName, formatBytes, uploadMedia } from './media-upload';

@Component({
  selector: 'app-admin-media',
  imports: [FormsModule, DatePipe],
  template: `
    <section class="a-card a-form">
      <div><h2 style="margin:0">Media</h2>
        <p style="margin:4px 0 0">Images, PDFs and short videos for your pages. Files are kept in private storage and served from
          <span class="a-mono">/media/…</span> on your own domain.@if (maxMB()) { Up to {{ maxMB() }} MB each. }</p></div>
      <div class="a-drop" [class.over]="over()" (dragover)="$event.preventDefault(); over.set(true)" (dragleave)="over.set(false)" (drop)="drop($event)">
        <label>Alt text for the next upload (describe the image for screen readers)<input [(ngModel)]="alt" maxlength="1000" /></label>
        <div class="a-row" style="flex-wrap:wrap">
          <input #fileInput type="file" class="sr-only" tabindex="-1" aria-hidden="true" multiple [accept]="accept.any" (change)="pick($event)" />
          <button class="ab primary" type="button" [disabled]="progress() !== null" (click)="fileInput.click()">
            {{ progress() !== null ? 'Uploading ' + progress() + '%' : 'Upload files…' }}</button>
          <span class="a-muted">or drop files here. JPEG, PNG, GIF, WebP, AVIF, PDF, MP4, WebM.</span>
        </div>
      </div>
      @if (error()) { <p class="a-err" role="alert">{{ error() }}</p> }
      @if (notice()) { <p class="a-ok">{{ notice() }}</p> }
      <div class="a-filters">
        <label class="f">Type<select [(ngModel)]="kind" (change)="load(0)"><option value="">All</option><option value="image">Images</option><option value="video">Videos</option><option value="file">PDFs</option></select></label>
        <label class="f">Search<input type="search" [(ngModel)]="q" (keyup.enter)="load(0)" placeholder="Name or alt text" /></label>
      </div>
      <div class="a-media-grid">
        @for (m of items(); track m.id) {
          <div class="a-media-card">
            <a [href]="m.url" target="_blank" rel="noopener" class="a-media-thumb">
              @if (m.kind === 'image') { <img [src]="m.url" [alt]="m.alt" loading="lazy" /> }
              @else { <span class="a-media-icon">{{ m.kind === 'video' ? '▶' : 'PDF' }}</span> }
            </a>
            <div class="a-media-name" [title]="displayName(m)">{{ displayName(m) }}</div>
            <div class="a-muted" style="font-size:.72rem">
              {{ formatBytes(m.size_bytes) }}@if (m.width) { · {{ m.width }}×{{ m.height }} } · {{ m.created_at | date: 'MMM d, y' }}
              @if (m.source && m.source !== 'upload') { <span class="pill info">{{ m.source }}</span> }
            </div>
            @if (m.kind === 'image') {
              <input [ngModel]="m.alt" (change)="saveAlt(m, $any($event.target).value)" placeholder="Alt text" aria-label="Alt text" />
            }
            <div class="a-row" style="gap:4px">
              <button class="ab sm" type="button" (click)="copy(m)">Copy URL</button>
              <button class="ab sm danger" type="button" (click)="remove(m)">Delete</button>
            </div>
          </div>
        } @empty { <p class="a-muted">No media yet. Upload a file above.</p> }
      </div>
      <div class="a-pager"><span>{{ total() }} files</span>
        <span class="a-row"><button class="ab sm" type="button" [disabled]="offset === 0" (click)="load(offset - limit)">‹ Prev</button>
          <button class="ab sm" type="button" [disabled]="offset + limit >= total()" (click)="load(offset + limit)">Next ›</button></span></div>
    </section>
  `,
})
export class MediaLibraryComponent {
  private api = inject(AdminApi);
  readonly items = signal<MediaItem[]>([]);
  readonly total = signal(0);
  readonly maxMB = signal(0);
  readonly error = signal('');
  readonly notice = signal('');
  readonly progress = signal<number | null>(null);
  readonly over = signal(false);
  readonly accept = ACCEPT;
  readonly formatBytes = formatBytes;
  readonly displayName = displayName;
  kind = '';
  q = '';
  alt = '';
  offset = 0;
  readonly limit = 48;

  constructor() {
    this.load(0);
  }
  load(offset: number) {
    this.offset = Math.max(0, offset);
    this.api.get<{ items: MediaItem[]; total: number; max_upload_bytes: number }>('media', { kind: this.kind, q: this.q.trim(), limit: this.limit, offset: this.offset })
      .subscribe({
        next: (r) => { this.items.set(r.items ?? []); this.total.set(r.total); this.maxMB.set(Math.floor((r.max_upload_bytes ?? 0) / 1048576)); },
        error: (e) => this.error.set(errMsg(e)),
      });
  }
  pick(ev: Event) {
    const el = ev.target as HTMLInputElement;
    const files = Array.from(el.files ?? []);
    el.value = '';
    this.uploadAll(files);
  }
  drop(ev: DragEvent) {
    ev.preventDefault();
    this.over.set(false);
    this.uploadAll(Array.from(ev.dataTransfer?.files ?? []));
  }
  private uploadAll(files: File[]) {
    if (!files.length || this.progress() !== null) return;
    this.error.set('');
    this.notice.set('');
    let done = 0;
    const failed: string[] = [];
    const next = () => {
      const file = files.shift();
      if (!file) {
        this.progress.set(null);
        this.alt = '';
        if (done) this.notice.set(done === 1 ? 'File uploaded.' : done + ' files uploaded.');
        if (failed.length) this.error.set(failed.join(' '));
        this.load(0);
        return;
      }
      this.progress.set(0);
      uploadMedia(this.api, file, this.alt.trim(), (p) => this.progress.set(p)).subscribe({
        next: () => { done++; next(); },
        error: (e) => { failed.push(`${file.name}: ${errMsg(e)}`); next(); },
      });
    };
    next();
  }
  saveAlt(m: MediaItem, alt: string) {
    this.api.patch<MediaItem>('media/' + m.id, { alt }).subscribe({
      next: (u) => { this.items.update((xs) => xs.map((x) => (x.id === u.id ? u : x))); this.notice.set('Alt text saved.'); },
      error: (e) => this.error.set(errMsg(e)),
    });
  }
  copy(m: MediaItem) {
    navigator.clipboard?.writeText(m.url).then(() => this.notice.set('Copied ' + m.url));
  }
  remove(m: MediaItem, force = false) {
    if (!force && !confirm(`Delete “${displayName(m)}” permanently?`)) return;
    this.api.delete('media/' + m.id + (force ? '?force=true' : '')).subscribe({
      next: () => { this.notice.set('Deleted.'); this.load(this.offset); },
      error: (e) => {
        if (e?.status === 409 && confirm(errMsg(e) + '\n\nDelete anyway? Pages and emails using it will show a broken image; site sections fall back to text or the default photo.')) this.remove(m, true);
        else if (e?.status !== 409) this.error.set(errMsg(e));
      },
    });
  }
}
