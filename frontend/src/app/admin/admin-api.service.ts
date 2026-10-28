import { HttpClient, HttpParams } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';

type Params = Record<string, string | number | boolean | null | undefined>;

/** Thin client for /api/admin endpoints. */
@Injectable({ providedIn: 'root' })
export class AdminApi {
  private http = inject(HttpClient);

  private params(p?: Params) {
    let hp = new HttpParams();
    for (const [k, v] of Object.entries(p ?? {})) {
      if (v !== null && v !== undefined && v !== '') hp = hp.set(k, String(v));
    }
    return hp;
  }
  get<T>(path: string, p?: Params) {
    return this.http.get<T>('/api/admin/' + path, { params: this.params(p) });
  }
  post<T>(path: string, body: unknown = {}) {
    return this.http.post<T>('/api/admin/' + path, body);
  }
  put<T>(path: string, body: unknown) {
    return this.http.put<T>('/api/admin/' + path, body);
  }
  patch<T>(path: string, body: unknown) {
    return this.http.patch<T>('/api/admin/' + path, body);
  }
  delete<T>(path: string) {
    return this.http.delete<T>('/api/admin/' + path);
  }
  upload<T>(path: string, file: File) {
    const fd = new FormData();
    fd.append('file', file);
    return this.http.post<T>('/api/admin/' + path, fd);
  }
  /** Downloads a CSV with the auth header (plain links can't carry it). */
  download(path: string, filename: string, p?: Params) {
    this.http.get('/api/admin/' + path, { params: this.params(p), responseType: 'blob' }).subscribe((blob) => {
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = filename;
      a.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    });
  }
}

export function errMsg(e: unknown): string {
  const x = e as { status?: number; error?: { error?: string } };
  if (x?.status === 401) return 'Your session expired. Sign in again.';
  if (x?.status === 403) return 'This account is not allowed to manage the site.';
  return x?.error?.error ?? 'Request failed. Please try again.';
}
