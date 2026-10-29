import { FetchBackend, HttpEvent, HttpRequest } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable, TimeoutError, mergeMap, takeUntil, throwError, timer } from 'rxjs';
import { httpStatus } from './http-status';
import { API_ORIGIN } from './tokens';

/**
 * All API reads of one server render share this budget, counted from the start of the
 * request: the same 15 s as the site content cold start (D16). A read still open then fails
 * with a TimeoutError, which the pages already treat as "API down" (fallbacks, or the 503).
 */
export const RENDER_BUDGET_MS = 15_000;

/** When this server render's API budget runs out. Created as the app boots (one per request). */
@Injectable({ providedIn: 'root' })
export class RenderClock {
  readonly deadline = Date.now() + RENDER_BUDGET_MS;
}

/**
 * Server only: the HTTP backend. Sends relative API calls ("/api/...") straight to the Go API
 * and ends any call still open at the render's deadline. As the backend it runs after
 * Angular's HTTP transfer cache, so the cache keys stay relative and match the browser's
 * requests: hydration reuses the server's responses instead of fetching them again.
 */
@Injectable()
export class ServerApiBackend extends FetchBackend {
  private origin = inject(API_ORIGIN);
  private clock = inject(RenderClock);
  private status = httpStatus();

  override handle(req: HttpRequest<unknown>): Observable<HttpEvent<unknown>> {
    const left = this.clock.deadline - Date.now();
    if (left <= 0) return this.cut();
    const url = this.origin && req.url.startsWith('/') ? this.origin + req.url : req.url;
    return super
      .handle(url === req.url ? req : req.clone({ url }))
      .pipe(takeUntil(timer(left).pipe(mergeMap(() => this.cut()))));
  }

  /** A render that gave up on a read is incomplete: keep it out of every cache. */
  private cut(): Observable<never> {
    this.status.noStore();
    return throwError(() => new TimeoutError());
  }
}
