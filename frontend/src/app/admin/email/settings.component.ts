import { Component, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { AdminApi, errMsg } from '../admin-api.service';

interface Brand { logo_text: string; logo_url: string; accent_color: string; ink_color: string; dark_color: string; font_family: string; header_html: string; footer_html: string }
interface EmailSettings { sender_name: string; sender_email: string; reply_to: string; organization_name: string; postal_address: string;
  privacy_policy_url: string; sending_domains: string[]; double_opt_in: boolean; verification_ttl_hours: number; resend_cooldown_minutes: number;
  max_verification_sends: number; consent_text: string; privacy_policy_version: string; topics: string[]; track_opens: boolean; track_clicks: boolean;
  test_recipients: string[]; auto_notify_on_publish: boolean; duplicate_send_window_hours: number; pending_retention_days: number;
  job_body_retention_days: number; brand: Brand }
interface Delivery { provider_retry_base_seconds: number; provider_retry_max_seconds: number; max_cycles: number; cycle_backoff_base_seconds: number;
  cycle_backoff_max_seconds: number; circuit_failure_threshold: number; circuit_cooldown_seconds: number; worker_concurrency: number;
  lease_seconds: number; send_timeout_seconds: number; unknown_reconcile_seconds: number; unknown_max_reconciles: number;
  unknown_auto_resend: boolean; unknown_resend_grace_minutes: number; outage_alert_minutes: number; campaign_batch_size: number }

const DELIVERY_FIELDS: Array<[keyof Delivery, string]> = [
  ['provider_retry_base_seconds', 'Retry delay on same provider (s)'], ['provider_retry_max_seconds', 'Max retry delay (s)'],
  ['max_cycles', 'Full provider cycles before dead-letter'], ['cycle_backoff_base_seconds', 'Backoff between cycles (s)'],
  ['cycle_backoff_max_seconds', 'Max cycle backoff (s)'], ['circuit_failure_threshold', 'Failures that open a circuit'],
  ['circuit_cooldown_seconds', 'Circuit cooldown (s)'], ['worker_concurrency', 'Worker concurrency'], ['lease_seconds', 'Job lease (s)'],
  ['send_timeout_seconds', 'Provider request timeout (s)'], ['unknown_reconcile_seconds', 'Unknown-acceptance check interval (s)'],
  ['unknown_max_reconciles', 'Unknown-acceptance checks before manual review'], ['unknown_resend_grace_minutes', 'Grace before a safe resend (min)'],
  ['outage_alert_minutes', 'Alert when all providers down for (min)'], ['campaign_batch_size', 'Campaign enqueue batch size'],
];

@Component({
  selector: 'app-admin-email-settings',
  imports: [FormsModule],
  template: `
    @if (error()) { <p class="a-err">{{ error() }}</p> }
    @if (notice()) { <p class="a-ok">{{ notice() }}</p> }
    <div class="a-split">
      @if (email(); as e) {
        <form class="a-card a-form" (ngSubmit)="saveEmail(e)">
          <h2 style="margin:0">Sender and compliance</h2>
          <div class="two">
            <label>Sender name<input name="sn" [(ngModel)]="e.sender_name" required /></label>
            <label>Sender email<input type="email" name="se" [(ngModel)]="e.sender_email" placeholder="newsletter@cadentic.dev" /></label>
            <label>Reply-to<input type="email" name="rt" [(ngModel)]="e.reply_to" /></label>
            <label>Organisation name<input name="org" [(ngModel)]="e.organization_name" /></label>
          </div>
          <label>Postal address shown in marketing footers<input name="addr" [(ngModel)]="e.postal_address" placeholder="City, State, Country" /></label>
          <div class="two">
            <label>Privacy policy URL<input name="pp" [(ngModel)]="e.privacy_policy_url" /></label>
            <label>Privacy policy version<input name="ppv" [(ngModel)]="e.privacy_policy_version" /></label>
          </div>
          <label>Consent text shown on the signup form<textarea name="ct" rows="3" [(ngModel)]="e.consent_text"></textarea></label>
          <p class="a-muted" style="margin:0;font-size:.78rem">Changing the consent text or policy version only affects new signups. Each consent record keeps the exact text the person saw.</p>
          <div class="two">
            <label>Topics (comma separated)<input name="tp" [ngModel]="e.topics.join(', ')" (ngModelChange)="e.topics = list($event)" /></label>
            <label>Test recipients (comma separated)<input name="tr" [ngModel]="e.test_recipients.join(', ')" (ngModelChange)="e.test_recipients = list($event)" /></label>
            <label>Sending domains<input name="sd" [ngModel]="e.sending_domains.join(', ')" (ngModelChange)="e.sending_domains = list($event)" /></label>
            <label>Confirmation link valid for (hours)<input type="number" name="ttl" min="1" max="168" [(ngModel)]="e.verification_ttl_hours" /></label>
            <label>Resend cooldown (min)<input type="number" name="rc" min="1" [(ngModel)]="e.resend_cooldown_minutes" /></label>
            <label>Max confirmation emails per address<input type="number" name="mv" min="1" [(ngModel)]="e.max_verification_sends" /></label>
            <label>Duplicate-send window (hours)<input type="number" name="dw" min="1" [(ngModel)]="e.duplicate_send_window_hours" /></label>
            <label>Delete unconfirmed signups after (days)<input type="number" name="pr" min="1" [(ngModel)]="e.pending_retention_days" /></label>
            <label>Delete stored email bodies after (days)<input type="number" name="jr" min="1" [(ngModel)]="e.job_body_retention_days" /></label>
          </div>
          <label class="a-row"><input type="checkbox" name="doi" [(ngModel)]="e.double_opt_in" /> Double opt-in (strongly recommended)</label>
          <label class="a-row"><input type="checkbox" name="to" [(ngModel)]="e.track_opens" /> Track opens</label>
          <label class="a-row"><input type="checkbox" name="tc" [(ngModel)]="e.track_clicks" /> Track clicks</label>
          <label class="a-row"><input type="checkbox" name="an" [(ngModel)]="e.auto_notify_on_publish" /> Create a newsletter draft whenever an article is first published</label>
          <h3 style="margin:8px 0 0">Brand</h3>
          <div class="two">
            <label>Logo text<input name="lt" [(ngModel)]="e.brand.logo_text" /></label>
            <label>Logo image URL<input name="lu" [(ngModel)]="e.brand.logo_url" /></label>
            <label>Accent colour<input type="color" name="ac" [(ngModel)]="e.brand.accent_color" /></label>
            <label>Text colour<input type="color" name="ic" [(ngModel)]="e.brand.ink_color" /></label>
          </div>
          <div class="a-row"><button class="ab primary" type="submit" [disabled]="busy()">Save email settings</button></div>
        </form>
      }
      @if (delivery(); as d) {
        <form class="a-card a-form" (ngSubmit)="saveDelivery(d)">
          <h2 style="margin:0">Delivery and failover</h2>
          <p class="a-muted" style="margin:0">Values outside safe ranges are clamped on save.</p>
          <div class="two">
            @for (f of fields; track f[0]) {
              <label>{{ f[1] }}<input type="number" [name]="f[0]" min="1" [ngModel]="d[f[0]]" (ngModelChange)="set(d, f[0], $event)" /></label>
            }
          </div>
          <label class="a-row"><input type="checkbox" name="uar" [(ngModel)]="d.unknown_auto_resend" /> Resend automatically when a provider confirms it never received an email (uses the provider's idempotency key)</label>
          <div class="a-row"><button class="ab primary" type="submit" [disabled]="busy()">Save delivery settings</button></div>
        </form>
      }
    </div>
  `,
})
export class EmailSettingsComponent {
  private api = inject(AdminApi);
  readonly email = signal<EmailSettings | null>(null);
  readonly delivery = signal<Delivery | null>(null);
  readonly error = signal('');
  readonly notice = signal('');
  readonly busy = signal(false);
  readonly fields = DELIVERY_FIELDS;

  constructor() {
    this.api.get<EmailSettings>('settings/email').subscribe({ next: (e) => this.email.set(this.fill(e)), error: (e) => this.error.set(errMsg(e)) });
    this.api.get<Delivery>('settings/delivery').subscribe({ next: (d) => this.delivery.set(d), error: (e) => this.error.set(errMsg(e)) });
  }
  private fill(e: EmailSettings): EmailSettings {
    return { ...e, topics: e.topics ?? [], test_recipients: e.test_recipients ?? [], sending_domains: e.sending_domains ?? [] };
  }
  list(v: string) {
    return v.split(',').map((s) => s.trim()).filter(Boolean);
  }
  set(d: Delivery, k: keyof Delivery, v: unknown) {
    (d as unknown as Record<string, number>)[k] = Number(v);
  }
  private done(msg: string) {
    this.busy.set(false);
    this.error.set('');
    this.notice.set(msg);
  }
  private fail(e: unknown) {
    this.busy.set(false);
    this.notice.set('');
    this.error.set(errMsg(e));
  }
  saveEmail(e: EmailSettings) {
    this.busy.set(true);
    this.api.put<EmailSettings>('settings/email', e).subscribe({ next: (r) => { this.email.set(this.fill(r ?? e)); this.done('Email settings saved.'); }, error: (x) => this.fail(x) });
  }
  saveDelivery(d: Delivery) {
    this.busy.set(true);
    this.api.put<Delivery>('settings/delivery', d).subscribe({ next: (r) => { if (r) this.delivery.set(r); this.done('Delivery settings saved.'); }, error: (x) => this.fail(x) });
  }
}
