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
      { path: 'resume', loadComponent: () => import('./public/resume.component').then((m) => m.ResumePageComponent) },
      { path: 'subscribe/confirm', loadComponent: () => import('./public/confirm.component').then((m) => m.ConfirmComponent) },
      { path: 'unsubscribe', loadComponent: () => import('./public/unsubscribe.component').then((m) => m.UnsubscribeComponent) },
      // Also serves the legal pages (/privacy, /terms, /ai-policy), which are CMS pages.
      { path: '**', data: { kind: 'page' }, loadComponent: () => import('./public/content-page.component').then((m) => m.ContentPageComponent) },
    ],
  },
];
