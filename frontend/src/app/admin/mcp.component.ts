import { DatePipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { AdminApi, errMsg } from './admin-api.service';

interface Conn { client_id: string; client_name: string; email: string; last_used_at: string | null; created_at: string }

@Component({
  selector: 'app-admin-mcp',
  imports: [DatePipe],
  template: `
    <section class="a-card">
      <h2>AI connector (MCP)</h2>
      <p>Connect Claude or another MCP client to create and publish pages and articles in plain language. The client signs in with your owner account through OAuth; no API keys are shared.</p>
      <div class="a-block" style="margin:12px 0">
        <span class="a-muted" style="font-size:.8rem">Connector URL</span>
        <div class="a-row"><code class="a-mono" style="flex:1;word-break:break-all">{{ url() }}</code>
          <button class="ab sm" type="button" (click)="copy()">{{ copied() ? 'Copied' : 'Copy' }}</button></div>
      </div>
      <ol style="font-size:.86rem;line-height:1.7;margin:0 0 12px;padding-left:18px">
        <li>In Claude, open Settings → Connectors → Add custom connector and paste the URL above.</li>
        <li>Claude opens a sign-in window. Sign in here with your owner account and approve the request.</li>
        <li>Ask, for example: “Draft an article about Oracle RAC patching with a YouTube hero and publish it.”</li>
      </ol>
      @if (error()) { <p class="a-err">{{ error() }}</p> }
      @if (notice()) { <p class="a-ok">{{ notice() }}</p> }
      <h3>Active connections</h3>
      <div class="a-table-wrap"><table class="a-table">
        <thead><tr><th>Client</th><th>Signed in as</th><th>Connected</th><th>Last used</th></tr></thead>
        <tbody>
          @for (c of items(); track c.client_id) {
            <tr><td>{{ c.client_name || c.client_id }}</td><td>{{ c.email }}</td><td>{{ c.created_at | date: 'MMM d, y' }}</td><td>{{ (c.last_used_at | date: 'MMM d, HH:mm') ?? 'never' }}</td></tr>
          } @empty { <tr><td colspan="4" class="a-muted">No connected clients.</td></tr> }
        </tbody>
      </table></div>
      @if (items().length) { <button class="ab danger" type="button" style="margin-top:12px" (click)="revokeAll()">Disconnect all clients</button> }
    </section>
  `,
})
export class McpComponent {
  private api = inject(AdminApi);
  readonly items = signal<Conn[]>([]);
  readonly url = signal('');
  readonly error = signal('');
  readonly notice = signal('');
  readonly copied = signal(false);
  constructor() {
    this.load();
  }
  load() {
    this.api.get<{ items: Conn[]; mcp_url: string }>('mcp/connections').subscribe({
      next: (r) => { this.items.set(r.items ?? []); this.url.set(r.mcp_url); }, error: (e) => this.error.set(errMsg(e)) });
  }
  copy() {
    navigator.clipboard?.writeText(this.url()).then(() => { this.copied.set(true); setTimeout(() => this.copied.set(false), 2000); });
  }
  revokeAll() {
    if (!confirm('Disconnect every MCP client? They will need to sign in again.')) return;
    this.api.post('mcp/revoke-all').subscribe({ next: () => { this.notice.set('All clients disconnected.'); this.load(); }, error: (e) => this.error.set(errMsg(e)) });
  }
}
