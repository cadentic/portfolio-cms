import { Injectable } from '@angular/core';

/** Bridge so the HTTP interceptor can get a Firebase ID token without importing Firebase into the public bundle. */
@Injectable({ providedIn: 'root' })
export class AdminToken {
  getter: (() => Promise<string | null>) | null = null;
}
