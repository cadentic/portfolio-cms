import { DatePipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute, Router } from '@angular/router';
import { Observable } from 'rxjs';
import { AdminApi, errMsg } from '../admin-api.service';

interface Status {
  configured: boolean; redirect_url: string; connected: boolean; account_email: string; connected_at: string | null;
  label_resource: string; label_name: string; attested_by: string; attested_at: string | null; paused: boolean; running: boolean;
  last_sync_at: string | null; next_sync_at: string | null; last_result: { summary?: Record<string, number>; trigger?: string } | null;
  last_error: string; list_size: number;
}
interface Label { resource: string; name: string; member_count: number }
interface Result { label: string; summary: Record<string, number>; rows?: Array<{ email: string; name: string; status: string }> }

const STATUS_TEXT: Record<string, string> = {
  new: 'Will be added', added: 'Added', on_list: 'Already on this list', subscribed: 'Already a subscriber',
  opted_out: 'Skipped: unsubscribed, bounced or suppressed', no_email: 'Skipped: no email address',
  invalid: 'Skipped: invalid email', duplicate: 'Skipped: duplicate email',
};

@Component({
  selector: 'app-admin-google-contacts',
  imports: [FormsModule, DatePipe],
  template: `
    <section class="a-card a-form" aria-labelledby="gc-title">
      <h2 id="gc-title">Google Contacts list</h2>
      <p style="margin:0">Contacts with one chosen Google Contacts label are copied into a <strong>separate list</strong> about every 6 hours.
        They never receive the regular newsletter: only campaigns that set the audience source to <span class="a-mono">google_contacts</span> reach them.
        People who unsubscribed, bounced or complained are never added back, and nobody gets an email when they are added.</p>
      @if (flash()) { <p class="a-ok" role="status">{{ flash() }}</p> }
      @if (error()) { <p class="a-err" role="alert">{{ error() }}</p> }

      @if (st(); as s) {
        @if (!s.configured) {
          <p class="a-err">Google sign-in is not set up on the server yet. Create an OAuth client in Google Cloud (see docs/google-contacts.md),
            add <span class="a-mono">{{ s.redirect_url }}</span> as an authorised redirect URI, and set
            <span class="a-mono">GOOGLE_OAUTH_CLIENT_ID</span> and <span class="a-mono">GOOGLE_OAUTH_CLIENT_SECRET</span>.</p>
        } @else if (!s.connected) {
          <p>Connect the Google account whose contacts you want to use. The site only asks for read-only access to contacts.</p>
          <div class="a-row"><button class="ab primary" type="button" [disabled]="busy()" (click)="connect()">Connect Google Contacts</button></div>
        } @else {
          <dl class="a-kv">
            <dt>Google account</dt><dd>{{ s.account_email || 'Connected' }} <span class="a-muted">· since {{ s.connected_at | date: 'MMM d, y' }}</span></dd>
            <dt>Label</dt><dd>{{ s.label_name || 'None chosen yet' }}@if (s.attested_by) { <span class="a-muted"> · consent attested by {{ s.attested_by }} on {{ s.attested_at | date: 'MMM d, y' }}</span> }</dd>
            <dt>On the list</dt><dd>{{ s.list_size }} verified contacts</dd>
            <dt>Automatic sync</dt><dd>@if (s.paused) { <span class="pill pending">paused</span> } @else if (s.label_resource) { every 6 hours · next {{ s.next_sync_at | date: 'MMM d, HH:mm' }} } @else { waiting for a label }</dd>
            <dt>Last sync</dt><dd>@if (s.last_sync_at) { {{ s.last_sync_at | date: 'MMM d, y HH:mm' }} ({{ s.last_result?.trigger || '' }}) @if (s.last_result?.summary; as sum) { · {{ sum['added'] || 0 }} added, {{ (sum['total'] || 0) - (sum['added'] || 0) }} unchanged or skipped } } @else { never }</dd>
          </dl>
          @if (s.last_error) { <p class="a-err">Last sync failed: {{ s.last_error }}</p> }

          <div class="a-section"><h3>Which contacts</h3>
            @if (labels().length) {
              <label class="f">Google Contacts label
                <select name="label" [(ngModel)]="labelChoice">
                  <option value="">Choose a label…</option>
                  @for (l of labels(); track l.resource) { <option [value]="l.resource">{{ l.name }} ({{ l.member_count }})</option> }
                </select></label>
              <label class="a-row"><input type="checkbox" name="attest" [(ngModel)]="attest" />
                I confirm everyone with this label agreed to receive my emails. This is recorded as their consent.</label>
              <div class="a-row"><button class="ab primary" type="button" [disabled]="!labelChoice || !attest || busy()" (click)="saveLabel()">Use this label</button></div>
            } @else {
              <div class="a-row"><button class="ab" type="button" [disabled]="busy()" (click)="loadLabels()">{{ s.label_resource ? 'Change label' : 'Choose a label' }}</button></div>
            }
          </div>

          <div class="a-row">
            <button class="ab" type="button" [disabled]="!s.label_resource || busy()" (click)="preview()">Preview</button>
            <button class="ab primary" type="button" [disabled]="!s.label_resource || busy() || s.running" (click)="syncNow()">{{ s.running ? 'Syncing…' : 'Sync now' }}</button>
            <button class="ab" type="button" [disabled]="busy()" (click)="setPaused(!s.paused)">{{ s.paused ? 'Resume automatic sync' : 'Pause automatic sync' }}</button>
            <button class="ab danger" type="button" [disabled]="busy()" (click)="disconnect()">Disconnect</button>
          </div>
        }
      } @else { <p aria-live="polite">Loading…</p> }
    </section>

    @if (result(); as r) {
      <section class="a-card" style="margin-top:16px" aria-labelledby="gc-res">
        <h2 id="gc-res">{{ r.rows ? 'Preview' : 'Sync result' }} · {{ r.label }}</h2>
        <div class="a-row">@for (k of keys(r.summary); track k) { <span class="pill" [class]="'pill ' + pillClass(k)">{{ label(k) }}: {{ r.summary[k] }}</span> }</div>
        @if (r.rows?.length) {
          <div class="a-table-wrap" style="max-height:360px;overflow:auto;margin-top:10px">
            <table class="a-table"><thead><tr><th>Email</th><th>Name</th><th>Result</th></tr></thead><tbody>
              @for (row of r.rows; track $index) { <tr><td>{{ row.email || '—' }}</td><td>{{ row.name }}</td><td><span class="pill" [class]="'pill ' + pillClass(row.status)">{{ label(row.status) }}</span></td></tr> }
            </tbody></table>
          </div>
        }
      </section>
    }
  `,
})
export class GoogleContactsComponent {
  private api = inject(AdminApi);
  readonly st = signal<Status | null>(null);
  readonly labels = signal<Label[]>([]);
  readonly result = signal<Result | null>(null);
  readonly busy = signal(false);
  readonly error = signal('');
  readonly flash = signal('');
  labelChoice = '';
  attest = false;

  constructor() {
    const q = inject(ActivatedRoute).snapshot.queryParamMap;
    if (q.get('connected')) this.flash.set('Google Contacts connected. Now choose the label to sync.');
    if (q.get('error')) this.error.set(q.get('error')!);
    if (q.get('connected') || q.get('error')) inject(Router).navigate([], { queryParams: {}, replaceUrl: true });
    this.load();
  }

  keys(o: Record<string, number>) {
    return Object.keys(o).filter((k) => k !== 'total').concat('total');
  }
  label(k: string) {
    return k === 'total' ? 'Contacts with the label' : (STATUS_TEXT[k] ?? k);
  }
  pillClass(k: string) {
    return k === 'added' || k === 'new' ? 'ok' : k === 'total' || k === 'on_list' || k === 'subscribed' ? 'info' : 'pending';
  }

  load() {
    this.api.get<Status>('google-contacts').subscribe({
      next: (s) => { this.st.set(s); this.labelChoice = s.label_resource; },
      error: (e) => this.error.set(errMsg(e)),
    });
  }
  private run<T>(obs: Observable<T>, done: (v: T) => void) {
    this.busy.set(true);
    this.error.set('');
    this.flash.set('');
    obs.subscribe({
      next: (v) => { this.busy.set(false); done(v); },
      error: (e) => { this.busy.set(false); this.error.set(errMsg(e)); this.load(); },
    });
  }
  connect() {
    this.run(this.api.post<{ url: string }>('google-contacts/connect'), (r) => location.assign(r.url));
  }
  loadLabels() {
    this.run(this.api.get<{ items: Label[] }>('google-contacts/labels'), (r) => {
      this.labels.set(r.items);
      if (!r.items.length) this.error.set('This Google account has no labels yet. Create one in Google Contacts and add people to it.');
    });
  }
  saveLabel() {
    this.run(this.api.put<Label>('google-contacts/label', { label_resource: this.labelChoice, attest: this.attest }), (l) => {
      this.labels.set([]);
      this.attest = false;
      this.flash.set(`Syncing contacts labelled “${l.name}”. Use Preview to see who would be added.`);
      this.load();
    });
  }
  preview() {
    this.run(this.api.post<Result>('google-contacts/preview'), (r) => this.result.set(r));
  }
  syncNow() {
    this.run(this.api.post<Result>('google-contacts/sync'), (r) => { this.result.set(r); this.load(); });
  }
  setPaused(paused: boolean) {
    this.run(this.api.post('google-contacts/pause', { paused }), () => this.load());
  }
  disconnect() {
    if (!confirm('Disconnect Google Contacts? Contacts already on the list stay there; automatic sync stops.')) return;
    this.run(this.api.post('google-contacts/disconnect'), () => { this.result.set(null); this.labels.set([]); this.load(); });
  }
}
