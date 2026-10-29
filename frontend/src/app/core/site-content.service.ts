import { DOCUMENT, isPlatformBrowser } from '@angular/common';
import { HttpClient } from '@angular/common/http';
import {
  Injectable,
  PLATFORM_ID,
  TransferState,
  inject,
  makeStateKey,
  signal,
} from '@angular/core';
import { firstValueFrom, timeout } from 'rxjs';
import { httpStatus } from './http-status';
import { SITE_FALLBACK, SiteContent } from './site-content';
import { API_ORIGIN } from './tokens';

type Payload = { content: SiteContent; degraded: boolean };
type Fetched = { content: SiteContent; stale: boolean };

const KEY = makeStateKey<Payload>('site-content');
const PATH = '/api/public/site-content';
const ADMIN = /^\/admin(\/|$)/;
const COLD_MS = 15_000; // no last good copy yet: wait out a cold API and database (D16)
const WARM_MS = 3_000;
const REUSE_MS = 5_000;
const BROWSER_MS = 10_000;

// SSR process memory, shared by all requests. Public data, identical for every visitor.
let lastGood: (Fetched & { at: number }) | null = null;
let inflight: Promise<Fetched> | null = null;

/** One shared fetch per process at a time. Global fetch, so it depends on no request's injector. */
function fetchShared(origin: string): Promise<Fetched> {
  inflight ??= fetch(origin + PATH, {
    headers: { accept: 'application/json' },
    signal: AbortSignal.timeout(lastGood ? WARM_MS : COLD_MS),
  })
    .then(async (r) => {
      if (!r.ok) throw new Error('HTTP ' + r.status);
      const content = (await r.json()) as SiteContent;
      if (!content?.identity || !content.hero) throw new Error('unexpected response');
      const got = { content, stale: r.headers.has('x-content-stale') };
      lastGood = { ...got, at: Date.now() };
      return got;
    })
    .finally(() => (inflight = null));
  return inflight;
}

/**
 * The editable site content (D14-D16). Loaded once per app by an initializer, so components
 * and Seo read content() synchronously. The server hands its copy to the browser in TransferState.
 */
@Injectable({ providedIn: 'root' })
export class SiteContentService {
  private http = inject(HttpClient);
  private state = inject(TransferState);
  private doc = inject(DOCUMENT);
  private origin = inject(API_ORIGIN);
  private browser = isPlatformBrowser(inject(PLATFORM_ID));
  private status = httpStatus();
  private loaded = false;
  readonly content = signal<SiteContent>(SITE_FALLBACK);
  readonly degraded = signal(false);

  /** App initializer. The client-rendered admin does not wait for public content. */
  load(): Promise<void> {
    if (this.browser && ADMIN.test(this.doc.location.pathname)) return Promise.resolve();
    return this.start();
  }

  /** Called by the public shell, for a visit that started in the admin. */
  ensureLoaded(): void {
    if (!this.loaded) void this.start();
  }

  private start(): Promise<void> {
    this.loaded = true;
    return this.browser ? this.loadBrowser() : this.loadServer();
  }

  private async loadServer(): Promise<void> {
    let content = SITE_FALLBACK;
    let degraded = false;
    try {
      const got =
        lastGood && Date.now() - lastGood.at < REUSE_MS ? lastGood : await fetchShared(this.origin);
      content = got.content;
      if (got.stale) this.status.noStore();
    } catch (err) {
      console.error('site content unavailable:', (err as Error).message);
      if (lastGood) {
        content = lastGood.content;
        this.status.noStore();
      } else {
        degraded = true;
        this.status.unavailable();
      }
    }
    this.set(content, degraded);
    this.state.set(KEY, { content, degraded });
  }

  private async loadBrowser(): Promise<void> {
    const ssr = this.state.get(KEY, null);
    if (ssr) {
      this.state.remove(KEY);
      this.set(ssr.content, ssr.degraded);
      // Rendered from the fallback: try once more in the background.
      if (ssr.degraded) {
        this.fetch()
          .then((c) => this.set(c, false))
          .catch(() => undefined);
      }
      return;
    }
    try {
      this.set(await this.fetch(), false);
    } catch {
      this.set(SITE_FALLBACK, true);
    }
  }

  private fetch(): Promise<SiteContent> {
    return firstValueFrom(this.http.get<SiteContent>(PATH).pipe(timeout(BROWSER_MS)));
  }

  private set(content: SiteContent, degraded: boolean) {
    this.content.set(content);
    this.degraded.set(degraded);
  }
}
