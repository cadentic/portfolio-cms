import { ApplicationConfig, CSP_NONCE, REQUEST_CONTEXT, inject, mergeApplicationConfig } from '@angular/core';
import { provideServerRendering, withRoutes } from '@angular/ssr';
import { appConfig } from './app.config';
import { serverRoutes } from './app.routes.server';
import { API_ORIGIN, SITE_URL } from './core/tokens';

const serverConfig: ApplicationConfig = {
  providers: [
    provideServerRendering(withRoutes(serverRoutes)),
    // server.ts generates a nonce per request and puts it in the CSP header;
    // Angular stamps it on the inline event-replay script it emits.
    { provide: CSP_NONCE, useFactory: () => (inject(REQUEST_CONTEXT, { optional: true }) as { nonce?: string } | null)?.nonce ?? null },
    { provide: API_ORIGIN, useValue: (process.env['API_INTERNAL_URL'] ?? 'http://localhost:8080').replace(/\/$/, '') },
    { provide: SITE_URL, useValue: (process.env['PUBLIC_SITE_URL'] ?? 'https://cadentic.dev').replace(/\/$/, '') },
  ],
};

export const config = mergeApplicationConfig(appConfig, serverConfig);
