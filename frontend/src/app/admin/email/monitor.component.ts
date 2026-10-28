import { DatePipe, DecimalPipe, PercentPipe } from '@angular/common';
import { Component, DestroyRef, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { AdminApi, errMsg } from '../admin-api.service';
import { Provider } from './providers.component';

interface ProviderStats { provider_id: string; provider_name: string; attempts: number; accepted: number; failures: number; rate_limited: number;
  unknown: number; delivered: number; bounced: number; complained: number; error_rate: number }
interface Monitor { window_hours: number; queue_depth: number; processing: number; oldest_pending_at: string | null; status_counts: Record<string, number>;
  retries: number; failovers: number; failover_reasons: Array<{ reason: string; count: number }>; dead_letter: number; acceptance_unknown: number;
  bounce_rate: number; complaint_rate: number; providers: ProviderStats[]; unprocessed_events: number }
interface Alert { id: string; severity: string; kind: string; message: string; occurrences: number; first_seen_at: string; last_seen_at: string; acknowledged_at: string | null }
interface Job { id: string; category: string; kind: string; status: string; to_email: string; subject: string; attempt_count: number; cycle: number;
  failover_count: number; provider_message_id: string; last_error: string; next_attempt_at: string; unknown_since: string | null;
  reconcile_attempts: number; created_at: string; accepted_at: string | null; delivered_at: string | null; campaign_id: string | null }
interface Attempt { id: string; provider_name: string; provider_kind: string; attempt_number: number; provider_attempt: number; cycle: number;
  outcome: string | null; http_status: number | null; provider_message_id: string; error_code: string; error_message: string;
  retry_after_ms: number | null; failover_reason: string; started_at: string; finished_at: string | null }
interface JobEvent { id: string; provider_kind: string; event_type: string; occurred_at: string; received_at: string; processed: boolean }

@Component({
  selector: 'app-admin-monitor',
  imports: [FormsModule, DatePipe, DecimalPipe, PercentPipe],
  template: `
    @if (error()) { <p class="a-err">{{ error() }}</p> }
    @if (notice()) { <p class="a-ok">{{ notice() }}</p> }
    @if (data(); as m) {
      <div class="a-stats">
        <div class="a-card a-stat"><div class="label">Queue depth</div><div class="value">{{ m.queue_depth | number }}</div><div class="sub">{{ m.processing }} processing@if (m.oldest_pending_at) {, oldest {{ m.oldest_pending_at | date: 'HH:mm' }}}</div></div>
        <div class="a-card a-stat"><div class="label">Failovers ({{ m.window_hours }}h)</div><div class="value">{{ m.failovers | number }}</div><div class="sub">{{ m.retries }} retries</div></div>
        <div class="a-card a-stat"><div class="label">Acceptance unknown</div><div class="value">{{ m.acceptance_unknown | number }}</div><div class="sub">held until confirmed, never resent blindly</div></div>
        <div class="a-card a-stat"><div class="label">Dead letter</div><div class="value">{{ m.dead_letter | number }}</div><div class="sub">needs review</div></div>
        <div class="a-card a-stat"><div class="label">Bounce / complaint</div><div class="value">{{ m.bounce_rate | percent: '1.1-2' }}</div><div class="sub">{{ m.complaint_rate | percent: '1.2-3' }} complaints</div></div>
      </div>

      @if (alerts().length) {
        <section class="a-card" style="margin-bottom:16px">
          <h3 style="margin:0 0 8px">Open alerts</h3>
          <ul class="a-list">
            @for (a of alerts(); track a.id) {
              <li><span><span class="pill" [class]="'pill ' + a.severity">{{ a.severity }}</span> {{ a.message }} <span class="a-muted">×{{ a.occurrences }}, last {{ a.last_seen_at | date: 'MMM d HH:mm' }}</span></span>
                <button class="ab sm" type="button" (click)="ack(a)">Acknowledge</button></li>
            }
          </ul>
        </section>
      }

      <div class="a-split">
        <section class="a-card">
          <h3 style="margin:0 0 8px">Providers (last {{ m.window_hours }}h)</h3>
          <div class="a-table-wrap"><table class="a-table">
            <thead><tr><th>Provider</th><th>Circuit</th><th>Attempts</th><th>Accepted</th><th>Failures</th><th>429s</th><th>Unknown</th><th>Error rate</th></tr></thead>
            <tbody>
              @for (p of m.providers; track p.provider_id) {
                <tr><td>{{ p.provider_name }}</td><td><span class="pill" [class]="'pill ' + circuit(p.provider_id)">{{ circuitLabel(p.provider_id) }}</span></td>
                  <td>{{ p.attempts }}</td><td>{{ p.accepted }}</td><td>{{ p.failures }}</td><td>{{ p.rate_limited }}</td><td>{{ p.unknown }}</td><td>{{ p.error_rate | percent: '1.0-1' }}</td></tr>
              } @empty { <tr><td colspan="8" class="a-muted">No sends in this window.</td></tr> }
            </tbody>
          </table></div>
        </section>
        <aside class="a-card">
          <h3 style="margin:0 0 8px">Failover reasons</h3>
          <ul class="a-list">
            @for (r of m.failover_reasons; track r.reason) { <li><span>{{ r.reason }}</span><strong>{{ r.count }}</strong></li> }
            @empty { <li class="a-muted">No failovers.</li> }
          </ul>
          <div class="a-section"><ul class="a-list">
            @for (s of statusList(m); track s[0]) { <li><span class="pill" [class]="'pill ' + s[0]">{{ s[0] }}</span><strong>{{ s[1] }}</strong></li> }
          </ul></div>
          @if (m.unprocessed_events) { <p class="a-warn" style="margin-top:10px">{{ m.unprocessed_events }} webhook events are waiting to be matched to a job.</p> }
        </aside>
      </div>
    }

    <section class="a-card" style="margin-top:16px">
      <div class="a-row between">
        <h3 style="margin:0">Email jobs</h3>
        <div class="a-row">
          <select [(ngModel)]="status" (change)="loadJobs(0)" aria-label="Filter by status">
            <option value="">All statuses</option>
            @for (s of statuses; track s) { <option [value]="s">{{ s }}</option> }
          </select>
          <select [(ngModel)]="category" (change)="loadJobs(0)" aria-label="Filter by category">
            <option value="">All categories</option><option value="transactional">Transactional</option><option value="marketing">Marketing</option></select>
          <input type="search" [(ngModel)]="emailQ" (keyup.enter)="loadJobs(0)" placeholder="Recipient email" aria-label="Recipient email" />
          <button class="ab" type="button" (click)="exportJobs()">Export CSV</button>
          @if (status === 'dead_letter' && jobs().length) { <button class="ab primary" type="button" (click)="replayAll()">Replay all dead letters</button> }
        </div>
      </div>
      <div class="a-table-wrap" style="margin-top:12px"><table class="a-table">
        <thead><tr><th>Recipient</th><th>Kind</th><th>Status</th><th>Attempts</th><th>Failovers</th><th>Last error</th><th>Created</th></tr></thead>
        <tbody>
          @for (j of jobs(); track j.id) {
            <tr [class.sel]="detail()?.job?.id === j.id" (click)="open(j.id)" style="cursor:pointer">
              <td><button class="ab sm" type="button" style="background:none;border:0;padding:0" (click)="open(j.id)">{{ j.to_email }}</button><br><span class="a-muted">{{ j.subject }}</span></td>
              <td>{{ j.kind }}<br><span class="a-muted">{{ j.category }}</span></td>
              <td><span class="pill" [class]="'pill ' + j.status">{{ j.status }}</span></td>
              <td>{{ j.attempt_count }} (cycle {{ j.cycle }})</td>
              <td>{{ j.failover_count }}</td>
              <td class="a-muted" [title]="j.last_error">{{ j.last_error.slice(0, 60) }}</td>
              <td>{{ j.created_at | date: 'MMM d HH:mm:ss' }}</td>
            </tr>
          } @empty { <tr><td colspan="7" class="a-muted">No jobs match.</td></tr> }
        </tbody>
      </table></div>
      <div class="a-pager"><span>{{ offset + 1 }}–{{ offset + jobs().length }} of {{ total() }}</span>
        <span class="a-row"><button class="ab sm" type="button" [disabled]="offset === 0" (click)="loadJobs(offset - limit)">‹ Prev</button>
          <button class="ab sm" type="button" [disabled]="offset + limit >= total()" (click)="loadJobs(offset + limit)">Next ›</button></span></div>
    </section>

    @if (detail(); as d) {
      <div class="a-modal-bg" (click)="$event.target === $event.currentTarget && detail.set(null)">
        <div class="a-modal wide" role="dialog" aria-modal="true" aria-labelledby="job-t">
          <div class="a-row between"><h3 id="job-t" style="margin:0">Job to {{ d.job.to_email }}</h3><button class="ab sm" type="button" (click)="detail.set(null)">Close</button></div>
          <dl class="a-kv" style="margin-top:12px">
            <dt>Status</dt><dd><span class="pill" [class]="'pill ' + d.job.status">{{ d.job.status }}</span></dd>
            <dt>Subject</dt><dd>{{ d.job.subject }}</dd>
            <dt>Provider message id</dt><dd class="a-mono">{{ d.job.provider_message_id || '—' }}</dd>
            <dt>Next attempt</dt><dd>{{ d.job.next_attempt_at | date: 'medium' }}</dd>
            @if (d.job.unknown_since) { <dt>Unknown since</dt><dd>{{ d.job.unknown_since | date: 'medium' }} ({{ d.job.reconcile_attempts }} checks)</dd> }
            <dt>Job id</dt><dd class="a-mono">{{ d.job.id }}</dd>
          </dl>
          <div class="a-row" style="margin-top:12px;flex-wrap:wrap">
            @if (d.job.status === 'failed' || d.job.status === 'dead_letter' || d.job.status === 'cancelled') { <button class="ab" type="button" (click)="act(d.job, 'retry')">Retry</button> }
            @if (d.job.status === 'queued' || d.job.status === 'retry_scheduled') { <button class="ab danger" type="button" (click)="act(d.job, 'cancel')">Cancel</button> }
            @if (d.job.status === 'acceptance_unknown') {
              <button class="ab" type="button" (click)="resolve(d.job, 'reconcile')">Check provider again</button>
              <button class="ab" type="button" (click)="resolve(d.job, 'accepted')">Provider has it (mark accepted)</button>
              <button class="ab danger" type="button" (click)="resolve(d.job, 'resend')">Resend (may duplicate)</button>
            }
          </div>
          @if (d.job.status === 'acceptance_unknown') {
            <p class="a-warn" style="margin-top:10px">A provider may already have accepted this email. The worker keeps checking the provider and only resends once it is sure the first send never arrived. Use "Resend" only if you have checked the provider dashboard.</p>
          }
          <h4>Attempts</h4>
          <div class="a-table-wrap"><table class="a-table">
            <thead><tr><th>#</th><th>Provider</th><th>Outcome</th><th>HTTP</th><th>Error</th><th>Failover</th><th>Started</th></tr></thead>
            <tbody>
              @for (a of d.attempts; track a.id) {
                <tr><td>{{ a.attempt_number }} <span class="a-muted">c{{ a.cycle }}</span></td><td>{{ a.provider_name }}</td>
                  <td><span class="pill" [class]="'pill ' + (a.outcome ?? 'processing')">{{ a.outcome ?? 'in flight' }}</span></td>
                  <td>{{ a.http_status ?? '—' }}</td><td class="a-muted">{{ a.error_code }} {{ a.error_message.slice(0, 80) }}</td>
                  <td>{{ a.failover_reason || '—' }}</td><td>{{ a.started_at | date: 'HH:mm:ss' }}</td></tr>
              } @empty { <tr><td colspan="7" class="a-muted">No attempts yet.</td></tr> }
            </tbody>
          </table></div>
          <h4>Provider events</h4>
          <ul class="a-list">
            @for (e of d.events; track e.id) { <li><span>{{ e.provider_kind }} · {{ e.event_type }}</span><span class="a-muted">{{ e.occurred_at | date: 'MMM d HH:mm:ss' }}</span></li> }
            @empty { <li class="a-muted">No webhook events yet.</li> }
          </ul>
          @if (d.html) {
            <h4>Content</h4>
            <iframe class="a-preview" title="Email content" sandbox="" [attr.srcdoc]="d.html"></iframe>
          }
        </div>
      </div>
    }
  `,
})
export class MonitorComponent {
  private api = inject(AdminApi);
  readonly data = signal<Monitor | null>(null);
  readonly providers = signal<Provider[]>([]);
  readonly alerts = signal<Alert[]>([]);
  readonly jobs = signal<Job[]>([]);
  readonly total = signal(0);
  readonly detail = signal<{ job: Job; attempts: Attempt[]; events: JobEvent[]; html: string } | null>(null);
  readonly error = signal('');
  readonly notice = signal('');
  readonly statuses = ['queued', 'processing', 'retry_scheduled', 'accepted', 'delivered', 'deferred', 'acceptance_unknown', 'failed', 'bounced', 'complained', 'cancelled', 'dead_letter'];
  status = '';
  category = '';
  emailQ = '';
  offset = 0;
  readonly limit = 50;

  constructor() {
    this.load();
    this.loadJobs(0);
    const t = setInterval(() => this.load(), 15000);
    inject(DestroyRef).onDestroy(() => clearInterval(t));
  }
  load() {
    this.api.get<{ monitor: Monitor; providers: Provider[]; alerts: Alert[] }>('email/monitor', { hours: 24 }).subscribe({
      next: (r) => { this.data.set(r.monitor); this.providers.set(r.providers ?? []); this.alerts.set(r.alerts ?? []); },
      error: (e) => this.error.set(errMsg(e)),
    });
  }
  loadJobs(offset: number) {
    this.offset = Math.max(0, offset);
    this.api.get<{ items: Job[]; total: number }>('email/jobs', { status: this.status, category: this.category, email: this.emailQ.trim(), limit: this.limit, offset: this.offset })
      .subscribe({ next: (r) => { this.jobs.set(r.items ?? []); this.total.set(r.total); }, error: (e) => this.error.set(errMsg(e)) });
  }
  statusList(m: Monitor) {
    return Object.entries(m.status_counts ?? {}).sort((a, b) => b[1] - a[1]);
  }
  circuit(id: string) {
    const s = this.providers().find((p) => p.id === id)?.circuit_state;
    return s === 'closed' ? 'verified' : s === 'open' ? 'failed' : 'warning';
  }
  circuitLabel(id: string) {
    return this.providers().find((p) => p.id === id)?.circuit_state ?? '—';
  }
  open(id: string) {
    this.api.get<{ job: Job; attempts: Attempt[]; events: JobEvent[]; html: string }>('email/jobs/' + id).subscribe({
      next: (d) => this.detail.set({ ...d, attempts: d.attempts ?? [], events: d.events ?? [] }), error: (e) => this.error.set(errMsg(e)) });
  }
  private after(msg: string, j?: Job) {
    this.notice.set(msg);
    this.load();
    this.loadJobs(this.offset);
    if (j) this.open(j.id);
  }
  act(j: Job, action: 'retry' | 'cancel') {
    if (action === 'cancel' && !confirm('Cancel this email? It will not be sent.')) return;
    this.api.post('email/jobs/' + j.id + '/' + action).subscribe({ next: () => this.after(action === 'retry' ? 'Job re-queued.' : 'Job cancelled.', j), error: (e) => this.error.set(errMsg(e)) });
  }
  resolve(j: Job, resolution: 'accepted' | 'resend' | 'reconcile') {
    let confirmed = false;
    if (resolution === 'resend') {
      confirmed = confirm('The provider may already have this email. Resending can deliver it twice. Have you checked the provider dashboard and want to resend?');
      if (!confirmed) return;
    }
    this.api.post('email/jobs/' + j.id + '/resolve', { resolution, confirm: confirmed }).subscribe({
      next: () => this.after('Updated.', j), error: (e) => this.error.set(errMsg(e)) });
  }
  replayAll() {
    if (!confirm('Replay every dead-letter job? Each goes through the provider chain again.')) return;
    this.api.post<{ replayed: number }>('email/jobs/replay-dead-letters', { ids: [], confirm: true }).subscribe({
      next: (r) => this.after(`${r.replayed} jobs re-queued.`), error: (e) => this.error.set(errMsg(e)) });
  }
  ack(a: Alert) {
    this.api.post('alerts/' + a.id + '/ack').subscribe({ next: () => this.load(), error: (e) => this.error.set(errMsg(e)) });
  }
  exportJobs() {
    this.api.download('email/jobs/export.csv', 'delivery-report.csv', { status: this.status, category: this.category, email: this.emailQ.trim() });
  }
}
