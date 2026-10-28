import { DatePipe, DecimalPipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute } from '@angular/router';
import { AdminApi, errMsg } from '../admin-api.service';

interface Sub { id: string; email: string; name: string; status: string; source: string; topics: string[]; frequency: string;
  verified_at: string | null; unsubscribed_at: string | null; created_at: string; eligible: boolean }
interface Detail extends Sub {
  consents: Array<{ action: string; purpose: string; lawful_basis: string; source: string; consent_text: string; privacy_policy_version: string; ip: string; actor: string; created_at: string; evidence: Record<string, unknown> }>;
  history: Array<{ job_id: string; kind: string; subject: string; status: string; created_at: string }>;
  suppressions: string[];
}
interface Stats { total: number; verified: number; pending: number; unsubscribed: number; bounced: number; complained: number; growth_pct: number; new_30d: number }
interface ImportPreview { token: string; summary: Record<string, number>; rows: Array<{ line: number; email: string; name: string; status: string; error?: string }> }

@Component({
  selector: 'app-admin-subscribers',
  imports: [FormsModule, DatePipe, DecimalPipe],
  template: `
    @if (stats(); as s) {
      <div class="a-stats">
        <div class="a-card a-stat"><div class="label">Total subscribers</div><div class="value">{{ s.total | number }}</div><div class="sub"><span class="up">↑ {{ s.growth_pct | number: '1.0-1' }}%</span> vs. previous 30 days</div></div>
        <div class="a-card a-stat"><div class="label">Verified</div><div class="value">{{ s.verified | number }}</div><div class="sub">{{ pct(s.verified, s.total) }}% verification rate</div></div>
        <div class="a-card a-stat"><div class="label">Pending confirmation</div><div class="value">{{ s.pending | number }}</div><div class="sub">{{ pct(s.pending, s.total) }}% awaiting</div></div>
        <div class="a-card a-stat"><div class="label">Unsubscribed</div><div class="value">{{ s.unsubscribed | number }}</div><div class="sub">{{ pct(s.unsubscribed, s.total) }}% opt-out rate</div></div>
        <div class="a-card a-stat"><div class="label">Bounced / complained</div><div class="value">{{ s.bounced + s.complained | number }}</div><div class="sub">suppressed automatically</div></div>
      </div>
    }
    <div class="a-split">
      <section class="a-card">
        <div class="a-row between">
          <div><h2>Subscribers</h2><p style="margin:0">Search, filter and export. Consent is recorded for every subscriber.</p></div>
          <div class="a-row">
            <input type="search" placeholder="Search email or name…" aria-label="Search subscribers" [(ngModel)]="q" (keyup.enter)="load(1)" style="width:220px" />
            <button class="ab" type="button" (click)="showAdd.set(true)">Add subscriber</button>
            <button class="ab" type="button" (click)="showImport.set(true)">Import CSV</button>
            <button class="ab primary" type="button" (click)="export()">Export CSV</button>
          </div>
        </div>
        <div class="a-filters">
          <label class="f">Status<select [(ngModel)]="status" (change)="load(1)">
            <option value="">All</option><option value="verified">Verified</option><option value="pending">Pending</option>
            <option value="unsubscribed">Unsubscribed</option><option value="bounced">Bounced</option><option value="complained">Complained</option></select></label>
          <label class="f">Source<select [(ngModel)]="source" (change)="load(1)">
            <option value="">All</option><option value="website">Website</option><option value="blog">Blog</option><option value="import">Import</option>
            <option value="admin">Added by admin</option><option value="wordpress">WordPress</option></select></label>
          <label class="f">Subscribed from<input type="date" [(ngModel)]="from" (change)="load(1)" /></label>
          <label class="f">Sort by<select [(ngModel)]="sort" (change)="load(1)">
            <option value="">Newest first</option><option value="oldest">Oldest first</option><option value="email">Email A–Z</option><option value="status">Status</option></select></label>
        </div>
        @if (error()) { <p class="a-err">{{ error() }}</p> }
        <div class="a-table-wrap">
          <table class="a-table">
            <thead><tr><th>Email</th><th>Name</th><th>Status</th><th>Subscribed</th><th>Source</th><th><span class="sr-only">Actions</span></th></tr></thead>
            <tbody>
              @for (s of items(); track s.id) {
                <tr [class.sel]="selected()?.id === s.id">
                  <td><button class="ab sm" style="background:none;border:0;padding:0" type="button" (click)="open(s.id)">{{ s.email }}</button></td>
                  <td>{{ s.name }}</td>
                  <td><span class="pill" [class]="'pill ' + s.status">{{ s.status }}</span></td>
                  <td>{{ s.created_at | date: 'MMM d, y HH:mm' }}</td>
                  <td>{{ s.source }}</td>
                  <td><button class="ab sm" type="button" (click)="open(s.id)">Details</button></td>
                </tr>
              } @empty { <tr><td colspan="6" class="a-muted">No subscribers match these filters.</td></tr> }
            </tbody>
          </table>
        </div>
        <div class="a-pager">
          <span>Showing {{ (page() - 1) * size + (items().length ? 1 : 0) }}–{{ (page() - 1) * size + items().length }} of {{ total() | number }}</span>
          <span class="a-row">
            <button class="ab sm" type="button" [disabled]="page() <= 1" (click)="load(page() - 1)">‹ Prev</button>
            <span>Page {{ page() }} of {{ pages() }}</span>
            <button class="ab sm" type="button" [disabled]="page() >= pages()" (click)="load(page() + 1)">Next ›</button>
          </span>
        </div>
      </section>

      <aside class="a-card" aria-label="Subscriber details">
        @if (selected(); as d) {
          <div class="a-row between"><h2 style="word-break:break-all">{{ d.email }}</h2><span class="pill" [class]="'pill ' + d.status">{{ d.status }}</span></div>
          <p style="margin:2px 0 10px">{{ d.name || 'No name' }} · subscribed {{ d.created_at | date: 'MMM d, y HH:mm' }}</p>
          <div class="a-row">
            @if (d.status !== 'unsubscribed') { <button class="ab sm" type="button" (click)="unsubscribe(d)">Unsubscribe</button> }
            <button class="ab sm danger" type="button" (click)="erase(d)">Erase (DPDP)</button>
          </div>
          <div class="a-section"><h3>Subscription</h3>
            <dl class="a-kv"><dt>Source</dt><dd>{{ d.source }}</dd><dt>Verified</dt><dd>{{ (d.verified_at | date: 'medium') ?? '—' }}</dd>
              <dt>Unsubscribed</dt><dd>{{ (d.unsubscribed_at | date: 'medium') ?? '—' }}</dd><dt>Frequency</dt><dd>{{ d.frequency }}</dd>
              <dt>Topics</dt><dd>{{ d.topics.join(', ') || 'All' }}</dd><dt>Eligible</dt><dd>{{ d.eligible ? 'Yes, receives marketing' : 'No' }}</dd>
              <dt>Suppressions</dt><dd>{{ d.suppressions.join(', ') || 'None' }}</dd></dl></div>
          <div class="a-section"><h3>Consent &amp; privacy</h3>
            <ul class="a-list">
              @for (c of d.consents; track $index) {
                <li style="display:block"><strong>{{ c.action }}</strong> · {{ c.lawful_basis }} · {{ c.created_at | date: 'medium' }}<br>
                  <span class="a-muted">{{ c.source }}{{ c.ip ? ' · IP ' + c.ip : '' }}{{ c.privacy_policy_version ? ' · policy ' + c.privacy_policy_version : '' }}{{ c.actor ? ' · by ' + c.actor : '' }}</span></li>
              } @empty { <li class="a-muted">No consent records.</li> }
            </ul></div>
          <div class="a-section"><h3>Email history</h3>
            <ul class="a-list">
              @for (h of d.history; track h.job_id) {
                <li><span>{{ h.subject }} <span class="a-muted">({{ h.kind }})</span></span><span><span class="pill" [class]="'pill ' + h.status">{{ h.status }}</span> {{ h.created_at | date: 'MMM d' }}</span></li>
              } @empty { <li class="a-muted">No emails sent.</li> }
            </ul></div>
        } @else {
          <p>Select a subscriber to see consent records and email history.</p>
        }
      </aside>
    </div>

    @if (showAdd()) {
      <div class="a-modal-bg" (click)="$event.target === $event.currentTarget && showAdd.set(false)">
        <form class="a-modal a-form" (ngSubmit)="add()" role="dialog" aria-modal="true" aria-labelledby="add-t">
          <h2 id="add-t">Add subscriber</h2>
          <p>Only add people who have agreed to receive the newsletter. By default they get a confirmation email (double opt-in).</p>
          <div class="two">
            <label class="f">Email<input name="em" type="email" required [(ngModel)]="addForm.email" /></label>
            <label class="f">Name<input name="nm" [(ngModel)]="addForm.name" /></label>
          </div>
          <label class="f">Lawful basis<select name="lb" [(ngModel)]="addForm.lawful_basis"><option value="consent">Consent</option></select></label>
          <label class="f">How and when was consent obtained? (recorded as evidence)<textarea name="ev" required [(ngModel)]="addForm.consent_evidence" placeholder="e.g. Signed up at the AIOUG meetup on 12 Oct 2026, paper form #14"></textarea></label>
          <label class="a-row"><input type="checkbox" name="doi" [(ngModel)]="addForm.send_confirmation" /> Send a confirmation email (recommended)</label>
          @if (formError()) { <p class="a-err">{{ formError() }}</p> }
          <div class="a-row"><button class="ab primary" type="submit" [disabled]="busy()">Add subscriber</button><button class="ab" type="button" (click)="showAdd.set(false)">Cancel</button></div>
        </form>
      </div>
    }

    @if (showImport()) {
      <div class="a-modal-bg">
        <div class="a-modal wide a-form" role="dialog" aria-modal="true" aria-labelledby="imp-t">
          <h2 id="imp-t">Import subscribers from CSV</h2>
          @if (!preview()) {
            <p>CSV with an <span class="a-mono">email</span> column, optionally <span class="a-mono">name</span> and <span class="a-mono">topics</span> (separated by ;). Nothing is imported until you review and confirm.</p>
            <input type="file" accept=".csv,text/csv" (change)="file = $any($event.target).files?.[0] ?? null" aria-label="CSV file" />
            @if (formError()) { <p class="a-err">{{ formError() }}</p> }
            <div class="a-row"><button class="ab primary" type="button" [disabled]="!file || busy()" (click)="previewImport()">Validate file</button><button class="ab" type="button" (click)="closeImport()">Cancel</button></div>
          } @else if (importResult()) {
            <p class="a-ok">{{ importResult() }}</p>
            <div class="a-row"><button class="ab primary" type="button" (click)="closeImport()">Done</button></div>
          } @else {
            @let p = preview()!;
            <div class="a-row">@for (k of keys(p.summary); track k) { <span class="pill" [class]="'pill ' + (k === 'valid' ? 'ok' : k === 'invalid' ? 'failed' : 'pending')">{{ k }}: {{ p.summary[k] }}</span> }</div>
            <div class="a-table-wrap" style="max-height:260px;overflow:auto">
              <table class="a-table"><thead><tr><th>Line</th><th>Email</th><th>Name</th><th>Result</th></tr></thead><tbody>
                @for (r of p.rows.slice(0, 200); track r.line) { <tr><td>{{ r.line }}</td><td>{{ r.email }}</td><td>{{ r.name }}</td><td><span class="pill" [class]="'pill ' + (r.status === 'valid' ? 'ok' : r.status === 'invalid' ? 'failed' : 'pending')">{{ r.status }}</span> {{ r.error }}</td></tr> }
              </tbody></table>
            </div>
            <label class="f">How was consent obtained for these contacts? (required, stored as evidence)<textarea [(ngModel)]="imp.consent_evidence" name="iev"></textarea></label>
            <label class="a-row"><input type="checkbox" [(ngModel)]="imp.send_confirmation" name="idoi" /> Send each new contact a confirmation email (double opt-in). Untick only if you hold verifiable consent.</label>
            <label class="a-row"><input type="checkbox" [(ngModel)]="imp.confirm" name="iconf" /> I confirm these people agreed to receive the newsletter.</label>
            @if (formError()) { <p class="a-err">{{ formError() }}</p> }
            <div class="a-row"><button class="ab primary" type="button" [disabled]="!imp.confirm || busy()" (click)="commitImport()">Import {{ p.summary['valid'] || 0 }} subscribers</button><button class="ab" type="button" (click)="closeImport()">Cancel</button></div>
          }
        </div>
      </div>
    }
  `,
})
export class SubscribersComponent {
  private api = inject(AdminApi);
  readonly size = 25;
  readonly items = signal<Sub[]>([]);
  readonly total = signal(0);
  readonly page = signal(1);
  readonly stats = signal<Stats | null>(null);
  readonly selected = signal<Detail | null>(null);
  readonly error = signal('');
  readonly formError = signal('');
  readonly busy = signal(false);
  readonly showAdd = signal(false);
  readonly showImport = signal(!!inject(ActivatedRoute).snapshot.queryParamMap.get('import'));
  readonly preview = signal<ImportPreview | null>(null);
  readonly importResult = signal('');
  q = '';
  status = '';
  source = '';
  from = '';
  sort = '';
  file: File | null = null;
  addForm = { email: '', name: '', lawful_basis: 'consent', consent_evidence: '', send_confirmation: true };
  imp = { consent_evidence: '', send_confirmation: true, confirm: false };

  constructor() {
    this.load(1);
    this.loadStats();
  }

  pages() {
    return Math.max(1, Math.ceil(this.total() / this.size));
  }
  pct(a: number, b: number) {
    return b ? ((a / b) * 100).toFixed(1) : '0.0';
  }
  keys(o: Record<string, number>) {
    return Object.keys(o);
  }
  private filters() {
    return { q: this.q, status: this.status, source: this.source, from: this.from, sort: this.sort };
  }

  load(page: number) {
    this.page.set(page);
    this.api.get<{ items: Sub[]; total: number }>('subscribers', { ...this.filters(), page, page_size: this.size }).subscribe({
      next: (r) => { this.items.set(r.items); this.total.set(r.total); this.error.set(''); },
      error: (e) => this.error.set(errMsg(e)),
    });
  }
  loadStats() {
    this.api.get<Stats>('subscribers/stats').subscribe((s) => this.stats.set(s));
  }
  open(id: string) {
    this.api.get<Detail>('subscribers/' + id).subscribe({ next: (d) => this.selected.set(d), error: (e) => this.error.set(errMsg(e)) });
  }
  export() {
    this.api.download('subscribers/export.csv', `subscribers-${new Date().toISOString().slice(0, 10)}.csv`, this.filters());
  }
  unsubscribe(d: Detail) {
    if (!confirm(`Unsubscribe ${d.email}? They will stop receiving newsletters.`)) return;
    this.api.post('subscribers/' + d.id + '/unsubscribe').subscribe(() => { this.open(d.id); this.load(this.page()); this.loadStats(); });
  }
  erase(d: Detail) {
    if (prompt(`Erasing deletes ${d.email}'s personal data permanently (DPDP right to erasure). Only a suppression entry for the address is kept, so they are never emailed again. Type ERASE to confirm.`) !== 'ERASE') return;
    this.api.delete('subscribers/' + d.id).subscribe(() => { this.selected.set(null); this.load(this.page()); this.loadStats(); });
  }
  add() {
    this.busy.set(true);
    this.formError.set('');
    this.api.post<{ id: string }>('subscribers', this.addForm).subscribe({
      next: (r) => { this.busy.set(false); this.showAdd.set(false); this.load(1); this.loadStats(); this.open(r.id);
        this.addForm = { email: '', name: '', lawful_basis: 'consent', consent_evidence: '', send_confirmation: true }; },
      error: (e) => { this.busy.set(false); this.formError.set(errMsg(e)); },
    });
  }
  previewImport() {
    if (!this.file) return;
    this.busy.set(true);
    this.formError.set('');
    this.api.upload<ImportPreview>('subscribers/import/preview', this.file).subscribe({
      next: (p) => { this.busy.set(false); this.preview.set(p); },
      error: (e) => { this.busy.set(false); this.formError.set(errMsg(e)); },
    });
  }
  commitImport() {
    const p = this.preview();
    if (!p) return;
    this.busy.set(true);
    this.formError.set('');
    this.api.post<{ imported: number; skipped: number }>('subscribers/import/commit', { token: p.token, confirm: this.imp.confirm,
      lawful_basis: 'consent', consent_evidence: this.imp.consent_evidence, send_confirmation: this.imp.send_confirmation }).subscribe({
      next: (r) => { this.busy.set(false); this.importResult.set(`Imported ${r.imported} subscribers; skipped ${r.skipped}.`); this.load(1); this.loadStats(); },
      error: (e) => { this.busy.set(false); this.formError.set(errMsg(e)); },
    });
  }
  closeImport() {
    this.showImport.set(false);
    this.preview.set(null);
    this.importResult.set('');
    this.file = null;
    this.imp = { consent_evidence: '', send_confirmation: true, confirm: false };
  }
}
