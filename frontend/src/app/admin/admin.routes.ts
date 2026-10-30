import { Routes } from '@angular/router';
import { ownerGuard } from './admin.guard';
import { AdminLayoutComponent } from './layout.component';
import { unsavedChangesGuard } from './site/unsaved-changes.guard';

const siteEditor = () => import('./site/site-content-editor.component').then((m) => m.SiteContentEditorComponent);

export const ADMIN_ROUTES: Routes = [
  { path: 'login', loadComponent: () => import('./login.component').then((m) => m.LoginComponent) },
  { path: 'connect', canActivate: [ownerGuard], loadComponent: () => import('./connect.component').then((m) => m.ConnectComponent) },
  {
    path: '',
    component: AdminLayoutComponent,
    canActivate: [ownerGuard],
    children: [
      { path: '', pathMatch: 'full', loadComponent: () => import('./dashboard.component').then((m) => m.DashboardComponent) },
      {
        path: 'email',
        loadComponent: () => import('./email/email-shell.component').then((m) => m.EmailShellComponent),
        children: [
          { path: '', pathMatch: 'full', redirectTo: 'subscribers' },
          { path: 'subscribers', loadComponent: () => import('./email/subscribers.component').then((m) => m.SubscribersComponent) },
          { path: 'campaigns', loadComponent: () => import('./email/campaigns.component').then((m) => m.CampaignsComponent) },
          { path: 'campaigns/:id', loadComponent: () => import('./email/campaign-editor.component').then((m) => m.CampaignEditorComponent) },
          { path: 'templates', loadComponent: () => import('./email/templates.component').then((m) => m.TemplatesComponent) },
          { path: 'providers', loadComponent: () => import('./email/providers.component').then((m) => m.ProvidersComponent) },
          { path: 'settings', loadComponent: () => import('./email/settings.component').then((m) => m.EmailSettingsComponent) },
          { path: 'monitor', loadComponent: () => import('./email/monitor.component').then((m) => m.MonitorComponent) },
          { path: 'google-contacts', loadComponent: () => import('./email/google-contacts.component').then((m) => m.GoogleContactsComponent) },
          { path: 'suppressions', loadComponent: () => import('./email/suppressions.component').then((m) => m.SuppressionsComponent) },
        ],
      },
      { path: 'site', pathMatch: 'full', redirectTo: 'site/home' },
      { path: 'site/home', loadComponent: siteEditor, data: { area: 'home' }, canDeactivate: [unsavedChangesGuard] },
      { path: 'site/profile', loadComponent: siteEditor, data: { area: 'profile' }, canDeactivate: [unsavedChangesGuard] },
      { path: 'site/videos', loadComponent: siteEditor, data: { area: 'videos' }, canDeactivate: [unsavedChangesGuard] },
      { path: 'site/legal', loadComponent: () => import('./site/legal-pages.component').then((m) => m.LegalPagesComponent) },
      { path: 'content', loadComponent: () => import('./content/pages.component').then((m) => m.PagesComponent) },
      { path: 'content/:id', loadComponent: () => import('./content/page-editor.component').then((m) => m.PageEditorComponent) },
      { path: 'media', loadComponent: () => import('./media/media-library.component').then((m) => m.MediaLibraryComponent) },
      { path: 'messages', loadComponent: () => import('./messages.component').then((m) => m.MessagesComponent) },
      { path: 'seo', loadComponent: () => import('./seo-settings.component').then((m) => m.SeoSettingsComponent) },
      { path: 'mcp', loadComponent: () => import('./mcp.component').then((m) => m.McpComponent) },
      { path: 'audit', loadComponent: () => import('./audit.component').then((m) => m.AuditComponent) },
    ],
  },
];
