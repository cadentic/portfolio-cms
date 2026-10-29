import { provideHttpClient, withFetch, withInterceptors } from '@angular/common/http';
import { ApplicationConfig, inject, provideAppInitializer, provideBrowserGlobalErrorListeners } from '@angular/core';
import { provideClientHydration, withEventReplay, withHttpTransferCacheOptions } from '@angular/platform-browser';
import { provideRouter, withComponentInputBinding, withInMemoryScrolling, withRouterConfig } from '@angular/router';
import { routes } from './app.routes';
import { apiOriginInterceptor } from './core/api-origin.interceptor';
import { adminAuthInterceptor } from './admin/auth.interceptor';
import { SiteContentService } from './core/site-content.service';
import { SITE_URL } from './core/tokens';

export const appConfig: ApplicationConfig = {
  providers: [
    provideBrowserGlobalErrorListeners(),
    provideRouter(routes, withComponentInputBinding(),
      withInMemoryScrolling({ anchorScrolling: 'enabled', scrollPositionRestoration: 'enabled' }),
      withRouterConfig({ onSameUrlNavigation: 'reload' })),
    provideHttpClient(withFetch(), withInterceptors([apiOriginInterceptor, adminAuthInterceptor])),
    provideClientHydration(withEventReplay(), withHttpTransferCacheOptions({ includePostRequests: false })),
    // Site content is loaded before the first render, so SSR output carries it (D14).
    provideAppInitializer(() => inject(SiteContentService).load()),
    { provide: SITE_URL, useFactory: () => (typeof location !== 'undefined' ? location.origin : 'https://cadentic.dev') },
  ],
};
