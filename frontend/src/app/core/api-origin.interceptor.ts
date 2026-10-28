import { HttpInterceptorFn } from '@angular/common/http';
import { inject } from '@angular/core';
import { API_ORIGIN } from './tokens';

/** During SSR, relative API calls are sent straight to the Go API. */
export const apiOriginInterceptor: HttpInterceptorFn = (req, next) => {
  const origin = inject(API_ORIGIN);
  if (origin && req.url.startsWith('/')) {
    return next(req.clone({ url: origin + req.url }));
  }
  return next(req);
};
