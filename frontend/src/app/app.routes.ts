import { Routes } from '@angular/router';
import { ShellComponent } from './public/shell.component';

export const routes: Routes = [
  { path: 'admin', loadChildren: () => import('./admin/admin.routes').then((m) => m.ADMIN_ROUTES) },
  {
    path: '',
    component: ShellComponent,
    children: [
      { path: '', pathMatch: 'full', loadComponent: () => import('./public/home.component').then((m) => m.HomeComponent) },
      { path: 'blog', loadComponent: () => import('./public/blog-list.component').then((m) => m.BlogListComponent) },
      { path: 'videos', loadComponent: () => import('./public/videos.component').then((m) => m.VideosComponent) },
      { path: 'blog/:slug', data: { kind: 'article' }, loadComponent: () => import('./public/content-page.component').then((m) => m.ContentPageComponent) },
      { path: 'subscribe/confirm', loadComponent: () => import('./public/confirm.component').then((m) => m.ConfirmComponent) },
      { path: 'unsubscribe', loadComponent: () => import('./public/unsubscribe.component').then((m) => m.UnsubscribeComponent) },
      { path: 'privacy', data: { doc: 'privacy' }, loadComponent: () => import('./public/legal.component').then((m) => m.LegalComponent) },
      { path: 'terms', data: { doc: 'terms' }, loadComponent: () => import('./public/legal.component').then((m) => m.LegalComponent) },
      { path: 'ai-policy', data: { doc: 'ai-policy' }, loadComponent: () => import('./public/legal.component').then((m) => m.LegalComponent) },
      { path: '**', data: { kind: 'page' }, loadComponent: () => import('./public/content-page.component').then((m) => m.ContentPageComponent) },
    ],
  },
];
