import { DOCUMENT, isPlatformServer } from '@angular/common';
import { PLATFORM_ID, RESPONSE_INIT, inject } from '@angular/core';
import { Router } from '@angular/router';

/** Sets the HTTP status and headers of the server-rendered response. */
export function httpStatus() {
  const init = inject(RESPONSE_INIT, { optional: true });
  const server = isPlatformServer(inject(PLATFORM_ID));
  const router = inject(Router);
  const doc = inject(DOCUMENT);
  // Merges into the existing headers (the engine sets Content-Type) instead of replacing them.
  const setHeader = (name: string, value: string) => {
    if (!init) return;
    const h = new Headers(init.headers);
    h.set(name, value);
    init.headers = h;
  };
  return {
    setHeader,
    notFound() {
      if (init) init.status = 404;
    },
    /** 503 with Retry-After, never cached: the API could not be reached. */
    unavailable() {
      if (!server || !init) return;
      init.status = 503;
      setHeader('Retry-After', '120');
      setHeader('Cache-Control', 'no-store');
    },
    /** Rendered from a stale or fallback copy: keep it out of every cache. */
    noStore() {
      if (server) setHeader('Cache-Control', 'no-store');
    },
    redirect(to: string, status = 301) {
      if (server && init) {
        init.status = status;
        setHeader('Location', to);
      } else if (/^https?:\/\//.test(to)) {
        doc.location.replace(to);
      } else {
        router.navigateByUrl(to, { replaceUrl: true });
      }
    },
  };
}
