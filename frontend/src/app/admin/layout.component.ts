import { Component, inject, signal } from '@angular/core';
import { Meta, Title } from '@angular/platform-browser';
import { Router, RouterLink, RouterLinkActive, RouterOutlet } from '@angular/router';
import { AdminApi } from './admin-api.service';
import { AuthService } from './auth.service';

@Component({
  selector: 'app-admin-layout',
  imports: [RouterOutlet, RouterLink, RouterLinkActive],
  template: `
    <div class="admin">
      <aside class="a-side" aria-label="Admin navigation">
        <div class="a-logo"><b>{{ '{' }}y{{ '}' }}</b> Portfolio Admin</div>
        <nav class="a-nav">
          <a routerLink="/admin" routerLinkActive="active" [routerLinkActiveOptions]="{ exact: true }">⌂ Dashboard</a>
          <div class="group">Content</div>
          <a routerLink="/admin/content" routerLinkActive="active" [queryParams]="{ kind: 'article' }">✎ Blog articles</a>
          <a routerLink="/admin/content" [queryParams]="{ kind: 'page' }">▤ Pages</a>
          <a routerLink="/admin/messages" routerLinkActive="active">✉ Contact messages</a>
          <div class="group">Subscribers &amp; Email</div>
          <a class="sub" routerLink="/admin/email/subscribers" routerLinkActive="active">Subscribers</a>
          <a class="sub" routerLink="/admin/email/campaigns" routerLinkActive="active">Campaigns</a>
          <a class="sub" routerLink="/admin/email/templates" routerLinkActive="active">Templates</a>
          <a class="sub" routerLink="/admin/email/providers" routerLinkActive="active">Providers</a>
          <a class="sub" routerLink="/admin/email/monitor" routerLinkActive="active">Delivery monitor</a>
          <a class="sub" routerLink="/admin/email/suppressions" routerLinkActive="active">Suppressions</a>
          <a class="sub" routerLink="/admin/email/settings" routerLinkActive="active">Settings</a>
          <div class="group">System</div>
          <a routerLink="/admin/mcp" routerLinkActive="active">⚡ MCP connector</a>
          <a routerLink="/admin/audit" routerLinkActive="active">☰ Audit log</a>
          <a href="/" target="_blank" rel="noopener">↗ View site</a>
        </nav>
        <div class="a-status"><span class="ok">●</span> {{ queue() }}</div>
      </aside>
      <div class="a-main">
        <header class="a-top">
          <span class="a-muted">Owner dashboard</span>
          <div class="who">
            <a class="a-bell" routerLink="/admin/email/monitor" [attr.aria-label]="alerts() + ' open alerts'">🔔@if (alerts()) {<span>{{ alerts() }}</span>}</a>
            <span>{{ auth.me()?.email }}</span>
            <button class="ab sm" type="button" (click)="logout()">Sign out</button>
          </div>
        </header>
        <div class="a-content"><router-outlet /></div>
      </div>
    </div>
  `,
})
export class AdminLayoutComponent {
  readonly auth = inject(AuthService);
  private api = inject(AdminApi);
  private router = inject(Router);
  readonly alerts = signal(0);
  readonly queue = signal('Checking system…');

  constructor() {
    inject(Title).setTitle('Admin · Portfolio CMS');
    inject(Meta).updateTag({ name: 'robots', content: 'noindex, nofollow' });
    this.api.get<{ alerts: unknown[]; monitor: { queue_depth: number; dead_letter: number } }>('email/monitor').subscribe({
      next: (r) => {
        this.alerts.set(r.alerts?.length ?? 0);
        this.queue.set(`System online · ${r.monitor.queue_depth} queued · ${r.monitor.dead_letter} dead letters`);
      },
      error: () => this.queue.set('API unreachable'),
    });
  }

  async logout() {
    await this.auth.logout();
    this.router.navigateByUrl('/admin/login');
  }
}
