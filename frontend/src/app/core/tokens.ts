import { InjectionToken } from '@angular/core';

/** Origin of the Go API for server-side requests (e.g. http://api:8080). Empty in the browser. */
export const API_ORIGIN = new InjectionToken<string>('API_ORIGIN', { factory: () => '' });

/** Public origin used for canonical URLs, Open Graph and JSON-LD. */
export const SITE_URL = new InjectionToken<string>('SITE_URL', { factory: () => 'https://cadentic.dev' });
