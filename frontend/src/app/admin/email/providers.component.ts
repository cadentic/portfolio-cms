import { DatePipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { SITE_URL } from '../../core/tokens';
import { AdminApi, errMsg } from '../admin-api.service';

export interface Provider { id: string; name: string; kind: string; enabled: boolean; priority: number; streams: string[];
  credential_refs: Record<string, string>; webhook_secret_ref: string; sender_email: string; sender_name: string; reply_to: string;
  sending_domain: string; max_attempts: number; rate_limit_per_second: number; settings: Record<string, unknown>;
  circuit_state: string; consecutive_failures: number; last_success_at: string | null; last_failure_at: string | null; last_error: string;
  last_rate_limited_at: string | null; last_health_check_at: string | null; last_health_ok: boolean | null; last_health_message: string;
  credentials_configured: boolean; webhook_secret_configured: boolean }

/** Credential slots each adapter reads. Values are names of server env vars, never the keys themselves. */
const SLOTS: Record<string, string[]> = { brevo: ['api_key'], mailjet: ['api_key', 'secret_key'], smtp: ['username', 'password'] };
const REF = /^EMAIL_[A-Z0-9_]{2,64}$/;

interface Form { id?: string; name: string; kind: string; enabled: boolean; priority: number; streams: string;
  refs: Record<string, string>; webhook_secret_ref: string; sender_email: string; sender_name: string; reply_to: string;
  sending_domain: string; max_attempts: number; rate_limit_per_second: number; sandbox: boolean; host: string; port: string }

@Component({
  selector: 'app-admin-providers',
  imports: [FormsModule, DatePipe],
  template: `
    <section class="a-card">
      <div class="a-row between">
        <div><h2>Email providers</h2><p style="margin:0">Jobs go to the first healthy provider in this order and fail over automatically. API keys stay in server secrets; this page only stores their env var names.</p></div>
        <button class="ab primary" type="button" (click)="edit(null)">Add provider</button>
      </div>
      @if (error()) { <p class="a-err">{{ error() }}</p> }
      @if (notice()) { <p class="a-ok">{{ notice() }}</p> }
      <div class="a-table-wrap" style="margin-top:14px">
        <table class="a-table">
          <thead><tr><th>Order</th><th>Provider</th><th>Status</th><th>Circuit</th><th>Last success</th><th>Health</th><th><span class="sr-only">Actions</span></th></tr></thead>
          <tbody>
            @for (p of items(); track p.id; let i = $index) {
              <tr>
                <td class="a-row" style="gap:4px">
                  <button class="ab sm" type="button" [disabled]="i === 0" (click)="move(i, -1)" [attr.aria-label]="'Move ' + p.name + ' up'">↑</button>
                  <button class="ab sm" type="button" [disabled]="i === items().length - 1" (click)="move(i, 1)" [attr.aria-label]="'Move ' + p.name + ' down'">↓</button>
                </td>
                <td><strong>{{ p.name }}</strong> <span class="a-muted">({{ p.kind }})</span><br>
                  <span class="a-muted">{{ p.sender_email || 'default sender' }} · {{ p.streams.join(', ') || 'all streams' }}</span>
                  @if (!p.credentials_configured) { <br><span class="pill failed">credentials missing</span> }
                  @if (!p.webhook_secret_configured) { <br><span class="pill warning">webhook secret missing</span> }</td>
                <td><span class="pill" [class]="'pill ' + (p.enabled ? 'verified' : 'cancelled')">{{ p.enabled ? 'enabled' : 'disabled' }}</span></td>
                <td><span class="pill" [class]="'pill ' + (p.circuit_state === 'closed' ? 'verified' : p.circuit_state === 'open' ? 'failed' : 'warning')">{{ p.circuit_state }}</span>
                  @if (p.consecutive_failures) { <br><span class="a-muted">{{ p.consecutive_failures }} failures</span> }
                  @if (p.last_error) { <br><span class="a-muted" [title]="p.last_error">{{ p.last_error.slice(0, 60) }}</span> }</td>
                <td>{{ (p.last_success_at | date: 'MMM d, HH:mm') ?? '—' }}</td>
                <td>@if (p.last_health_check_at) { <span class="pill" [class]="'pill ' + (p.last_health_ok ? 'verified' : 'failed')">{{ p.last_health_ok ? 'ok' : 'failing' }}</span><br><span class="a-muted">{{ p.last_health_message }}</span> } @else { <span class="a-muted">not checked</span> }</td>
                <td class="a-row" style="gap:4px;flex-wrap:wrap">
                  <button class="ab sm" type="button" (click)="toggle(p)">{{ p.enabled ? 'Disable' : 'Enable' }}</button>
                  <button class="ab sm" type="button" (click)="health(p)">Check</button>
                  <button class="ab sm" type="button" (click)="testFor.set(p)">Test send</button>
                  <button class="ab sm" type="button" (click)="edit(p)">Edit</button>
                </td>
              </tr>
            } @empty { <tr><td colspan="7" class="a-muted">No providers. Add Brevo as primary and Mailjet as backup.</td></tr> }
          </tbody>
        </table>
      </div>
      <p class="a-muted" style="font-size:.8rem;margin:12px 0 0">Webhook URL for each provider: <span class="a-mono">{{ site }}/api/webhooks/&lt;provider id&gt;</span>. Configure the same signing secret at the provider and in the env var named under "Webhook secret".</p>
    </section>

    @if (testFor(); as p) {
      <div class="a-modal-bg" (click)="$event.target === $event.currentTarget && testFor.set(null)">
        <form class="a-modal a-form" role="dialog" aria-modal="true" aria-labelledby="pt-t" (ngSubmit)="test(p)">
          <h3 id="pt-t" style="margin:0">Send a test through {{ p.name }}</h3>
          <p class="a-muted" style="margin:0">Goes directly to this provider, skipping the queue. Allowed recipients: your own address or the test recipients in Email settings.</p>
          <label>Recipient<input type="email" name="to" [(ngModel)]="testTo" placeholder="Leave empty to send to yourself" /></label>
          @if (testResult()) { <p [class]="testOk() ? 'a-ok' : 'a-err'">{{ testResult() }}</p> }
          <div class="a-row"><button class="ab primary" type="submit" [disabled]="busy()">Send test</button><button class="ab" type="button" (click)="testFor.set(null)">Close</button></div>
        </form>
      </div>
    }

    @if (form(); as f) {
      <div class="a-modal-bg" (click)="$event.target === $event.currentTarget && form.set(null)">
        <form class="a-modal wide a-form" role="dialog" aria-modal="true" aria-labelledby="pf-t" (ngSubmit)="save(f)">
          <h3 id="pf-t" style="margin:0">{{ f.id ? 'Edit ' + f.name : 'Add provider' }}</h3>
          <div class="two">
            <label>Name<input name="name" [(ngModel)]="f.name" required maxlength="80" /></label>
            <label>Type<select name="kind" [(ngModel)]="f.kind" [disabled]="!!f.id">@for (k of kinds(); track k) { <option [value]="k">{{ k }}</option> }</select></label>
          </div>
          <fieldset class="a-block"><legend>Credentials (env var names)</legend>
            @for (slot of slots(f.kind); track slot) {
              <label>{{ slot }}<input [name]="'ref_' + slot" [(ngModel)]="f.refs[slot]" placeholder="EMAIL_{{ f.kind.toUpperCase() }}_{{ slot.toUpperCase() }}" class="a-mono" /></label>
            }
            <label>Webhook secret<input name="wh" [(ngModel)]="f.webhook_secret_ref" class="a-mono" placeholder="EMAIL_{{ f.kind.toUpperCase() }}_WEBHOOK_SECRET" /></label>
            @if (f.kind === 'smtp') {
              <div class="two"><label>SMTP host<input name="host" [(ngModel)]="f.host" /></label><label>Port<input name="port" [(ngModel)]="f.port" inputmode="numeric" /></label></div>
            }
            <p class="a-muted" style="margin:0;font-size:.78rem">Names must look like EMAIL_BREVO_API_KEY. Put the actual values in secret.tfvars (cloud) or .env (local).</p>
          </fieldset>
          <div class="two">
            <label>Sender email (optional)<input type="email" name="se" [(ngModel)]="f.sender_email" /></label>
            <label>Sender name (optional)<input name="sn" [(ngModel)]="f.sender_name" /></label>
            <label>Reply-to (optional)<input type="email" name="rt" [(ngModel)]="f.reply_to" /></label>
            <label>Sending domain<input name="sd" [(ngModel)]="f.sending_domain" placeholder="cadentic.dev" /></label>
            <label>Priority (lower goes first)<input type="number" name="pr" [(ngModel)]="f.priority" min="0" /></label>
            <label>Attempts before failover<input type="number" name="ma" [(ngModel)]="f.max_attempts" min="1" max="10" /></label>
            <label>Rate limit (emails/second)<input type="number" name="rl" [(ngModel)]="f.rate_limit_per_second" min="0.1" step="0.1" /></label>
            <label>Streams (comma separated, empty means all)<input name="st" [(ngModel)]="f.streams" placeholder="transactional, marketing" /></label>
          </div>
          <label class="a-row"><input type="checkbox" name="en" [(ngModel)]="f.enabled" /> Enabled</label>
          <label class="a-row"><input type="checkbox" name="sb" [(ngModel)]="f.sandbox" /> Sandbox mode (provider validates but does not deliver)</label>
          @if (formError()) { <p class="a-err">{{ formError() }}</p> }
          <div class="a-row">
            <button class="ab primary" type="submit" [disabled]="busy()">Save</button>
            @if (f.id) { <button class="ab danger" type="button" (click)="remove(f)">Delete</button> }
            <button class="ab" type="button" (click)="form.set(null)">Cancel</button>
          </div>
        </form>
      </div>
    }
  `,
})
export class ProvidersComponent {
  private api = inject(AdminApi);
  readonly site = inject(SITE_URL);
  readonly items = signal<Provider[]>([]);
  readonly kinds = signal<string[]>(['brevo', 'mailjet', 'smtp']);
  readonly error = signal('');
  readonly notice = signal('');
  readonly busy = signal(false);
  readonly form = signal<Form | null>(null);
  readonly formError = signal('');
  readonly testFor = signal<Provider | null>(null);
  readonly testResult = signal('');
  readonly testOk = signal(false);
  testTo = '';

  constructor() {
    this.load();
  }
  slots(kind: string) {
    return SLOTS[kind] ?? ['api_key'];
  }
  load() {
    this.api.get<{ items: Provider[]; kinds: string[] }>('providers').subscribe({
      next: (r) => { this.items.set(r.items); if (r.kinds?.length) this.kinds.set([...r.kinds].sort()); },
      error: (e) => this.error.set(errMsg(e)),
    });
  }
  edit(p: Provider | null) {
    this.formError.set('');
    const s = p?.settings ?? {};
    this.form.set(p
      ? { id: p.id, name: p.name, kind: p.kind, enabled: p.enabled, priority: p.priority, streams: p.streams.join(', '),
          refs: { ...p.credential_refs }, webhook_secret_ref: p.webhook_secret_ref, sender_email: p.sender_email, sender_name: p.sender_name,
          reply_to: p.reply_to, sending_domain: p.sending_domain, max_attempts: p.max_attempts, rate_limit_per_second: p.rate_limit_per_second,
          sandbox: !!s['sandbox'], host: String(s['host'] ?? ''), port: String(s['port'] ?? '') }
      : { name: '', kind: 'brevo', enabled: true, priority: (this.items().at(-1)?.priority ?? 0) + 10, streams: '', refs: {},
          webhook_secret_ref: '', sender_email: '', sender_name: '', reply_to: '', sending_domain: 'cadentic.dev', max_attempts: 3,
          rate_limit_per_second: 5, sandbox: false, host: '', port: '587' });
  }
  save(f: Form) {
    const refs: Record<string, string> = {};
    for (const slot of this.slots(f.kind)) {
      const v = (f.refs[slot] ?? '').trim();
      if (v && !REF.test(v)) return this.formError.set(`${slot}: env var names must match EMAIL_[A-Z0-9_]`);
      if (v) refs[slot] = v;
    }
    const wh = f.webhook_secret_ref.trim();
    if (wh && !REF.test(wh)) return this.formError.set('Webhook secret: env var names must match EMAIL_[A-Z0-9_]');
    const settings: Record<string, unknown> = { sandbox: f.sandbox };
    if (f.kind === 'smtp') Object.assign(settings, { host: f.host.trim(), port: Number(f.port) || 587 });
    const body = { name: f.name.trim(), kind: f.kind, enabled: f.enabled, priority: Number(f.priority),
      streams: f.streams.split(',').map((s) => s.trim()).filter(Boolean), credential_refs: refs, webhook_secret_ref: wh,
      sender_email: f.sender_email.trim(), sender_name: f.sender_name.trim(), reply_to: f.reply_to.trim(), sending_domain: f.sending_domain.trim(),
      max_attempts: Number(f.max_attempts), rate_limit_per_second: Number(f.rate_limit_per_second), settings };
    this.busy.set(true);
    const req = f.id ? this.api.patch<Provider>('providers/' + f.id, body) : this.api.post<Provider>('providers', body);
    req.subscribe({
      next: () => { this.busy.set(false); this.form.set(null); this.notice.set('Provider saved.'); this.load(); },
      error: (e) => { this.busy.set(false); this.formError.set(errMsg(e)); },
    });
  }
  toggle(p: Provider) {
    if (p.enabled && this.items().filter((x) => x.enabled).length === 1 &&
      !confirm('This is the only enabled provider. Disabling it pauses all email until another is enabled. Continue?')) return;
    this.api.patch('providers/' + p.id, { enabled: !p.enabled }).subscribe({ next: () => this.load(), error: (e) => this.error.set(errMsg(e)) });
  }
  move(i: number, d: number) {
    const ids = this.items().map((p) => p.id);
    [ids[i], ids[i + d]] = [ids[i + d], ids[i]];
    this.api.post('providers/reorder', { ids }).subscribe({ next: () => this.load(), error: (e) => this.error.set(errMsg(e)) });
  }
  health(p: Provider) {
    this.api.post<{ OK: boolean; Message: string }>('providers/' + p.id + '/health').subscribe({
      next: (h) => { this.notice.set(`${p.name}: ${h.OK ? 'healthy' : 'unhealthy'}${h.Message ? ' (' + h.Message + ')' : ''}`); this.load(); },
      error: (e) => this.error.set(errMsg(e)),
    });
  }
  test(p: Provider) {
    this.busy.set(true);
    this.testResult.set('');
    this.api.post<{ ok: boolean; outcome: string; error: string; note: string; provider_message_id: string }>('providers/' + p.id + '/test', { to: this.testTo }).subscribe({
      next: (r) => { this.busy.set(false); this.testOk.set(r.ok);
        this.testResult.set(r.ok ? `Accepted (message id ${r.provider_message_id || 'n/a'}). ${r.note}` : `Not accepted: ${r.outcome}${r.error ? ', ' + r.error : ''}`); this.load(); },
      error: (e) => { this.busy.set(false); this.testOk.set(false); this.testResult.set(errMsg(e)); },
    });
  }
  remove(f: Form) {
    if (!f.id || !confirm(`Delete provider "${f.name}"? Its delivery history is kept.`)) return;
    this.api.delete('providers/' + f.id).subscribe({
      next: () => { this.form.set(null); this.load(); },
      error: (e) => this.formError.set(errMsg(e)),
    });
  }
}
