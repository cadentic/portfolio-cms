import { HttpInterceptorFn } from '@angular/common/http';
import { inject } from '@angular/core';
import { from, switchMap } from 'rxjs';
import { AdminToken } from '../core/admin-token';

const needsAuth = (url: string) => url.startsWith('/api/admin/') || url === '/api/oauth/approve';

/** Attaches the owner's Firebase ID token to admin API calls. */
export const adminAuthInterceptor: HttpInterceptorFn = (req, next) => {
  const tokens = inject(AdminToken);
  if (!needsAuth(req.url) || !tokens.getter) return next(req);
  return from(tokens.getter()).pipe(
    switchMap((t) => next(t ? req.clone({ setHeaders: { Authorization: 'Bearer ' + t } }) : req)),
  );
};
