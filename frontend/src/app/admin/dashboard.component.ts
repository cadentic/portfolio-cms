import { DatePipe, DecimalPipe, PercentPipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { RouterLink } from '@angular/router';
import { AdminApi, errMsg } from './admin-api.service';

interface Dash {
  subscribers: { total: number; verified: number; pending: number; unsubscribed: number; bounced: number; growth_pct: number; new_30d: number };
  monitor: { queue_depth: number; failovers: number; dead_letter: number; acceptance_unknown: number; bounce_rate: number; complaint_rate: number };
  campaigns: Array<{ id: string; name: string; status: string; updated_at: string; audience_count: number | null }>;
  providers: Array<{ id: string; name: string; kind: string; priority: number; enabled: boolean; circuit_state: string; last_health_ok: boolean | null; credentials_configured: boolean }>;
  alerts: Array<{ id: string; severity: string; message: string; last_seen_at: string }>;
  activity: Array<{ kind: string; label: string; detail: string; at: string }> | null;
}

@Component({
  selector: 'app-admin-dashboard',
  imports: [RouterLink, DatePipe, DecimalPipe, PercentPipe],
  template: `
    <h1>Dashboard</h1>
    <p>Everything that needs your attention, at a glance.</p>
    @if (error()) { <p class="a-err">{{ error() }}</p> }
    @if (d(); as d) {
      <div class="a-stats" style="margin-top:16px">
        <div class="a-card a-stat"><div class="label">Total subscribers</div><div class="value">{{ d.subscribers.total | number }}</div><div class="sub"><span class="up">+{{ d.subscribers.new_30d }}</span> in 30 days</div></div>
        <div class="a-card a-stat"><div class="label">Verified</div><div class="value">{{ d.subscribers.verified | number }}</div><div class="sub">{{ d.subscribers.pending }} pending confirmation</div></div>
        <div class="a-card a-stat"><div class="label">Queue</div><div class="value">{{ d.monitor.queue_depth }}</div><div class="sub">{{ d.monitor.failovers }} failovers (24h)</div></div>
        <div class="a-card a-stat"><div class="label">Needs attention</div><div class="value">{{ d.monitor.dead_letter + d.monitor.acceptance_unknown }}</div><div class="sub">dead letters + unknown acceptance</div></div>
        <div class="a-card a-stat"><div class="label">Bounce / complaint</div><div class="value">{{ d.monitor.bounce_rate | percent: '1.1-1' }}</div><div class="sub">{{ d.monitor.complaint_rate | percent: '1.2-2' }} complaints</div></div>
      </div>
      <div class="a-grid" style="grid-template-columns:repeat(auto-fit,minmax(280px,1fr))">
        <section class="a-card">
          <h2>Quick actions</h2>
          <div class="a-row" style="margin-top:10px">
            <a class="ab primary" routerLink="/admin/content/new" [queryParams]="{ kind: 'article' }">New article</a>
            <a class="ab" routerLink="/admin/content/new" [queryParams]="{ kind: 'page' }">New page</a>
            <a class="ab" routerLink="/admin/email/campaigns/new">New campaign</a>
            <a class="ab" routerLink="/admin/email/subscribers" [queryParams]="{ import: 1 }">Import CSV</a>
          </div>
        </section>
        <section class="a-card">
          <div class="a-row between"><h2>Provider status</h2><a class="a-muted" routerLink="/admin/email/providers">Manage</a></div>
          <ul class="a-list" style="margin-top:8px">
            @for (p of d.providers; track p.id) {
              <li><span>{{ p.name }} <span class="a-muted">(priority {{ p.priority }})</span></span>
                <span class="pill" [class]="'pill ' + (!p.enabled || !p.credentials_configured ? 'cancelled' : p.circuit_state === 'closed' ? 'healthy' : p.circuit_state)">
                  {{ !p.enabled ? 'disabled' : !p.credentials_configured ? 'no credentials' : p.circuit_state === 'closed' ? 'healthy' : 'circuit ' + p.circuit_state }}</span></li>
            }
          </ul>
        </section>
        <section class="a-card">
          <div class="a-row between"><h2>Recent campaigns</h2><a class="a-muted" routerLink="/admin/email/campaigns">All</a></div>
          <ul class="a-list" style="margin-top:8px">
            @for (c of d.campaigns; track c.id) {
              <li><a [routerLink]="['/admin/email/campaigns', c.id]">{{ c.name }}</a><span class="pill" [class]="'pill ' + c.status">{{ c.status }}</span></li>
            } @empty { <li class="a-muted">No campaigns yet.</li> }
          </ul>
        </section>
        <section class="a-card">
          <div class="a-row between"><h2>Open alerts</h2><a class="a-muted" routerLink="/admin/email/monitor">Monitor</a></div>
          <ul class="a-list" style="margin-top:8px">
            @for (a of d.alerts; track a.id) {
              <li><span><span class="pill" [class]="'pill ' + a.severity">{{ a.severity }}</span> {{ a.message }}</span></li>
            } @empty { <li class="a-muted">No open alerts. 🎉</li> }
          </ul>
        </section>
        <section class="a-card">
          <h2>Recent activity</h2>
          <ul class="a-list" style="margin-top:8px">
            @for (x of d.activity ?? []; track $index) {
              <li><span>{{ x.kind === 'failover' ? 'Provider failover' : x.kind === 'campaign' ? 'Campaign' : 'Subscriber' }}: {{ x.label }} <span class="a-muted">{{ x.detail }}</span></span><span class="a-muted">{{ x.at | date: 'short' }}</span></li>
            } @empty { <li class="a-muted">Nothing yet.</li> }
          </ul>
        </section>
      </div>
    }
  `,
})
export class DashboardComponent {
  private api = inject(AdminApi);
  readonly d = signal<Dash | null>(null);
  readonly error = signal('');
  constructor() {
    this.api.get<Dash>('dashboard').subscribe({ next: (d) => this.d.set(d), error: (e) => this.error.set(errMsg(e)) });
  }
}
