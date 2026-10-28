import { DOCUMENT, isPlatformServer } from '@angular/common';
import { PLATFORM_ID, RESPONSE_INIT, inject } from '@angular/core';
import { Router } from '@angular/router';

/** Sets the HTTP status (and redirect target) of the server-rendered response. */
export function httpStatus() {
  const init = inject(RESPONSE_INIT, { optional: true });
  const server = isPlatformServer(inject(PLATFORM_ID));
  const router = inject(Router);
  const doc = inject(DOCUMENT);
  return {
    notFound() {
      if (init) init.status = 404;
    },
    redirect(to: string, status = 301) {
      if (server && init) {
        init.status = status;
        init.headers = { Location: to };
      } else if (/^https?:\/\//.test(to)) {
        doc.location.replace(to);
      } else {
        router.navigateByUrl(to, { replaceUrl: true });
      }
    },
  };
}
