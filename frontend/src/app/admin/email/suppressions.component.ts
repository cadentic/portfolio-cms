import { DatePipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { AdminApi, errMsg } from '../admin-api.service';

interface Suppression { id: string; email: string; scope: string; reason: string; source: string; note: string; created_at: string; lifted_at: string | null; lifted_by: string }

@Component({
  selector: 'app-admin-suppressions',
  imports: [FormsModule, DatePipe],
  template: `
    <section class="a-card">
      <div class="a-row between">
        <div><h2>Suppression list</h2><p style="margin:0">Addresses here never receive marketing email. Hard bounces and complaints are added automatically.</p></div>
        <div class="a-row">
          <input type="search" [(ngModel)]="q" (keyup.enter)="load(0)" placeholder="Search email" aria-label="Search suppressions" />
          <select [(ngModel)]="reason" (change)="load(0)" aria-label="Filter by reason">
            <option value="">All reasons</option><option value="unsubscribed">Unsubscribed</option><option value="hard_bounce">Hard bounce</option>
            <option value="complaint">Complaint</option><option value="manual">Manual / erased</option><option value="invalid">Invalid</option><option value="blocked">Blocked</option></select>
          <label class="a-row" style="font-size:.82rem"><input type="checkbox" [(ngModel)]="all" (change)="load(0)" /> Include lifted</label>
          <button class="ab primary" type="button" (click)="showAdd.set(true)">Add address</button>
        </div>
      </div>
      @if (error()) { <p class="a-err">{{ error() }}</p> }
      <div class="a-table-wrap" style="margin-top:14px"><table class="a-table">
        <thead><tr><th>Email</th><th>Scope</th><th>Reason</th><th>Source</th><th>Added</th><th><span class="sr-only">Actions</span></th></tr></thead>
        <tbody>
          @for (s of items(); track s.id) {
            <tr>
              <td>{{ s.email }}@if (s.note) { <br><span class="a-muted">{{ s.note }}</span> }</td>
              <td>{{ s.scope }}</td>
              <td><span class="pill" [class]="'pill ' + (s.lifted_at ? 'cancelled' : s.reason === 'complaint' ? 'complained' : s.reason === 'hard_bounce' ? 'bounced' : 'pending')">{{ s.reason }}</span></td>
              <td>{{ s.source }}</td>
              <td>{{ s.created_at | date: 'MMM d, y' }}@if (s.lifted_at) { <br><span class="a-muted">lifted {{ s.lifted_at | date: 'MMM d' }} by {{ s.lifted_by }}</span> }</td>
              <td>@if (!s.lifted_at) { <button class="ab sm" type="button" (click)="lift(s)">Lift</button> }</td>
            </tr>
          } @empty { <tr><td colspan="6" class="a-muted">No suppressed addresses.</td></tr> }
        </tbody>
      </table></div>
      <div class="a-pager"><span>{{ total() }} total</span>
        <span class="a-row"><button class="ab sm" type="button" [disabled]="offset === 0" (click)="load(offset - limit)">‹ Prev</button>
          <button class="ab sm" type="button" [disabled]="offset + limit >= total()" (click)="load(offset + limit)">Next ›</button></span></div>
    </section>

    @if (showAdd()) {
      <div class="a-modal-bg" (click)="$event.target === $event.currentTarget ? showAdd.set(false) : null">
        <form class="a-modal a-form" role="dialog" aria-modal="true" aria-labelledby="sup-t" (ngSubmit)="add()">
          <h3 id="sup-t" style="margin:0">Suppress an address</h3>
          <label>Email<input type="email" name="email" [(ngModel)]="form.email" required /></label>
          <label>Scope<select name="scope" [(ngModel)]="form.scope">
            <option value="marketing">Marketing only (confirmation and security email still allowed)</option>
            <option value="all">All email</option></select></label>
          <label>Note<input name="note" [(ngModel)]="form.note" maxlength="200" /></label>
          @if (formError()) { <p class="a-err">{{ formError() }}</p> }
          <div class="a-row"><button class="ab primary" type="submit">Add</button><button class="ab" type="button" (click)="showAdd.set(false)">Cancel</button></div>
        </form>
      </div>
    }
  `,
})
export class SuppressionsComponent {
  private api = inject(AdminApi);
  readonly items = signal<Suppression[]>([]);
  readonly total = signal(0);
  readonly error = signal('');
  readonly formError = signal('');
  readonly showAdd = signal(false);
  q = '';
  reason = '';
  all = false;
  offset = 0;
  readonly limit = 50;
  form = { email: '', scope: 'marketing', note: '' };

  constructor() {
    this.load(0);
  }
  load(offset: number) {
    this.offset = Math.max(0, offset);
    this.api.get<{ items: Suppression[]; total: number }>('suppressions', { q: this.q.trim(), reason: this.reason, all: this.all || null, limit: this.limit, offset: this.offset })
      .subscribe({ next: (r) => { this.items.set(r.items ?? []); this.total.set(r.total); }, error: (e) => this.error.set(errMsg(e)) });
  }
  add() {
    this.formError.set('');
    this.api.post('suppressions', this.form).subscribe({
      next: () => { this.showAdd.set(false); this.form = { email: '', scope: 'marketing', note: '' }; this.load(0); },
      error: (e) => this.formError.set(errMsg(e)),
    });
  }
  lift(s: Suppression) {
    const warn = s.reason === 'complaint' || s.reason === 'hard_bounce'
      ? `${s.email} was suppressed because of a ${s.reason.replace('_', ' ')}. Sending again can hurt your sender reputation. Lift anyway?`
      : `Lift the suppression for ${s.email}? They still need an active, consented subscription to get newsletters.`;
    if (!confirm(warn)) return;
    this.api.post('suppressions/' + s.id + '/lift').subscribe({ next: () => this.load(this.offset), error: (e) => this.error.set(errMsg(e)) });
  }
}
