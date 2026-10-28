import { RenderMode, ServerRoute } from '@angular/ssr';

export const serverRoutes: ServerRoute[] = [
  // The admin area is a client-side app behind Firebase sign-in: never server-rendered or indexed.
  { path: 'admin/**', renderMode: RenderMode.Client },
  { path: 'admin', renderMode: RenderMode.Client },
  // Public pages are rendered per request so new CMS content is live immediately.
  { path: '**', renderMode: RenderMode.Server },
];
