import { inject } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { AuthService } from './auth.service';

export const ownerGuard: CanActivateFn = async (_route, state) => {
  const auth = inject(AuthService);
  const router = inject(Router);
  const r = await auth.checkOwner();
  if (r === 'ok') return true;
  return router.createUrlTree(['/admin/login'], { queryParams: { next: state.url, denied: r === 'forbidden' ? 1 : null } });
};
