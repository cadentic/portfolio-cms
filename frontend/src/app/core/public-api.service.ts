import { HttpClient, HttpParams } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable, catchError, of, shareReplay, timeout } from 'rxjs';
import { Page, Paged, SiteConfig } from './models';

const fallbackConfig: SiteConfig = {
  site_url: '', blog_enabled: false, gtm_id: '', ga4_id: '', newsletter_topics: ['articles', 'videos', 'career'],
  consent_text: 'I agree to receive the newsletter by email. I can unsubscribe at any time.',
  privacy_policy_url: '/privacy', privacy_policy_version: '', resume_available: true, verification_ttl_hours: 48,
};

/** Same as the site content cold-start budget (D16): render slowly rather than fail. */
const PAGE_TIMEOUT_MS = 15_000;

@Injectable({ providedIn: 'root' })
export class PublicApi {
  private http = inject(HttpClient);
  private config$?: Observable<SiteConfig>;

  site(): Observable<SiteConfig> {
    this.config$ ??= this.http.get<SiteConfig>('/api/public/site').pipe(catchError(() => of(fallbackConfig)), shareReplay(1));
    return this.config$;
  }

  articles(limit = 12, offset = 0, tag = ''): Observable<Paged<Page>> {
    let params = new HttpParams().set('limit', limit).set('offset', offset);
    if (tag) params = params.set('tag', tag);
    return this.http.get<Paged<Page>>('/api/public/articles', { params });
  }

  article(slug: string): Observable<Page> {
    return this.http.get<Page>('/api/public/articles/' + encodeURIComponent(slug)).pipe(timeout(PAGE_TIMEOUT_MS));
  }

  page(path: string): Observable<Page> {
    return this.http
      .get<Page>('/api/public/pages/' + path.split('/').map(encodeURIComponent).join('/'))
      .pipe(timeout(PAGE_TIMEOUT_MS));
  }

  redirect(path: string): Observable<{ to: string; status: number }> {
    return this.http.get<{ to: string; status: number }>('/api/public/redirect', { params: { path } });
  }

  subscribe(body: { email: string; name: string; consent: boolean; topics: string[]; source: string; website: string }) {
    return this.http.post<{ message: string }>('/api/public/subscribe', body);
  }
  resend(email: string) {
    return this.http.post<{ message: string }>('/api/public/subscribe/resend', { email });
  }
  confirm(token: string) {
    return this.http.post<{ email: string; already_confirmed: boolean }>('/api/public/subscribe/confirm', { token });
  }
  unsubscribeInfo(token: string) {
    return this.http.get<{ email: string; status: string }>('/api/public/unsubscribe', { params: { token } });
  }
  unsubscribe(token: string) {
    return this.http.post<{ message: string }>('/api/public/unsubscribe', { token });
  }
  contact(body: { name: string; email: string; message: string; website: string }) {
    return this.http.post<{ message: string }>('/api/public/contact', body);
  }
  resumeRequest(email: string) {
    return this.http.post<{ message: string }>('/api/public/resume/request', { email });
  }
  resumeVerify(email: string, code: string) {
    return this.http.post<{ download_url: string }>('/api/public/resume/verify', { email, code });
  }
}

/** Extracts a user-facing message from an API error. */
export function apiError(err: unknown, fallback = 'Something went wrong. Please try again.'): string {
  const e = err as { status?: number; error?: { error?: string; message?: string } };
  if (e?.status === 0) return 'You appear to be offline. Please check your connection and try again.';
  if (e?.status === 429) return e.error?.error ?? 'Too many attempts. Please wait a moment and try again.';
  return e?.error?.error ?? e?.error?.message ?? fallback;
}
