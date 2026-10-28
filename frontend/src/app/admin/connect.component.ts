import { HttpClient, HttpParams } from '@angular/common/http';
import { Component, inject, signal } from '@angular/core';
import { ActivatedRoute } from '@angular/router';
import { AuthService } from './auth.service';

/** OAuth consent page for MCP clients (e.g. Claude) connecting to the CMS. */
@Component({
  selector: 'app-admin-connect',
  template: `
    <div class="admin a-login" style="display:grid;grid-template-columns:1fr">
      <main class="a-card" aria-labelledby="connect-title" style="width:min(480px,100%)">
        <h1 id="connect-title">Connect {{ client() || 'an app' }}</h1>
        @if (error()) {
          <p class="a-err" role="alert">{{ error() }}</p>
        } @else {
          <p><strong>{{ client() }}</strong> wants to manage your site's content as <strong>{{ auth.me()?.email }}</strong>.</p>
          <ul class="a-list" style="display:block;margin:10px 0 16px;color:var(--a-muted)">
            <li>✓ Create, edit, publish and unpublish pages and articles</li>
            <li>✓ Read subscriber totals, campaign stats and email health</li>
            <li>✗ It cannot send email or export subscriber addresses</li>
          </ul>
          <p class="a-muted">You'll be sent back to <span class="a-mono">{{ host() }}</span>. Revoke access any time under MCP connector.</p>
          <div class="a-row">
            <button class="ab primary" type="button" (click)="approve()" [disabled]="busy() || !host()">Allow access</button>
            <button class="ab" type="button" (click)="deny()" [disabled]="!host()">Deny</button>
          </div>
        }
      </main>
    </div>
  `,
})
export class ConnectComponent {
  readonly auth = inject(AuthService);
  private http = inject(HttpClient);
  private q = inject(ActivatedRoute).snapshot.queryParamMap;
  readonly client = signal('');
  readonly host = signal('');
  readonly error = signal('');
  readonly busy = signal(false);
  private req = Object.fromEntries(['client_id', 'redirect_uri', 'response_type', 'code_challenge', 'code_challenge_method', 'state', 'scope', 'resource']
    .map((k) => [k, this.q.get(k) ?? '']));

  constructor() {
    this.http.get<{ client_name: string; redirect_host: string }>('/api/oauth/client', { params: new HttpParams({ fromObject: this.req }) })
      .subscribe({
        next: (r) => { this.client.set(r.client_name); this.host.set(r.redirect_host); },
        error: (e) => this.error.set(e?.error?.error_description ?? 'This connection request is invalid or has expired.'),
      });
  }

  approve() {
    this.busy.set(true);
    this.http.post<{ redirect: string }>('/api/oauth/approve', this.req).subscribe({
      next: (r) => location.assign(r.redirect),
      error: (e) => { this.busy.set(false); this.error.set(e?.error?.error_description ?? 'Approval failed.'); },
    });
  }

  deny() {
    // Only redirect to a URI the API has validated against the registered client.
    if (!this.host()) return;
    const u = new URL(this.req['redirect_uri']);
    u.searchParams.set('error', 'access_denied');
    if (this.req['state']) u.searchParams.set('state', this.req['state']);
    location.assign(u.toString());
  }
}
