import { HttpEventType } from '@angular/common/http';
import { Observable, filter, map, tap } from 'rxjs';
import { MediaItem } from '../../core/models';
import { AdminApi } from '../admin-api.service';

export type MediaKind = 'image' | 'video' | 'file';

/** What the file picker offers; the server checks the real type from the bytes. */
export const ACCEPT: Record<MediaKind | 'any' | 'inline', string> = {
  image: 'image/jpeg,image/png,image/gif,image/webp,image/avif',
  video: 'video/mp4,video/webm',
  file: 'application/pdf',
  inline: 'image/jpeg,image/png,image/gif,image/webp,image/avif,application/pdf',
  any: 'image/jpeg,image/png,image/gif,image/webp,image/avif,application/pdf,video/mp4,video/webm',
};

/** Uploads one file to private media storage, reporting progress from 0 to 100. */
export function uploadMedia(api: AdminApi, file: File, alt: string, progress: (pct: number) => void): Observable<MediaItem> {
  return api.uploadWithProgress<MediaItem>('media', file, alt ? { alt } : {}).pipe(
    tap((e) => {
      if (e.type === HttpEventType.UploadProgress && e.total) progress(Math.round((100 * e.loaded) / e.total));
    }),
    filter((e) => e.type === HttpEventType.Response),
    map((e) => (e as { body: MediaItem }).body),
  );
}

export function formatBytes(n: number | null) {
  if (n == null) return '';
  if (n < 1024) return n + ' B';
  if (n < 1024 * 1024) return (n / 1024).toFixed(0) + ' KB';
  return (n / 1024 / 1024).toFixed(1) + ' MB';
}

export function displayName(m: MediaItem) {
  return m.filename || m.url.split('/').pop() || m.url;
}
