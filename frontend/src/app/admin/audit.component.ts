import { DatePipe, JsonPipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { AdminApi, errMsg } from './admin-api.service';

interface Entry { id: string; actor: string; action: string; entity_type: string; entity_id: string; details: Record<string, unknown> | null; created_at: string }

@Component({
  selector: 'app-admin-audit',
  imports: [DatePipe, JsonPipe, FormsModule],
  template: `
    <section class="a-card">
      <div class="a-row between">
        <div><h2>Audit log</h2><p style="margin:0">Every admin and connector action, with who did it and when.</p></div>
        <select [(ngModel)]="type" (change)="load(0)" aria-label="Filter by entity">
          <option value="">Everything</option>
          @for (t of types; track t) { <option [value]="t">{{ t }}</option> }
        </select>
      </div>
      @if (error()) { <p class="a-err">{{ error() }}</p> }
      <div class="a-table-wrap" style="margin-top:14px"><table class="a-table">
        <thead><tr><th>When</th><th>Who</th><th>Action</th><th>Target</th><th>Details</th></tr></thead>
        <tbody>
          @for (e of items(); track e.id) {
            <tr><td>{{ e.created_at | date: 'MMM d, HH:mm:ss' }}</td><td>{{ e.actor }}</td><td class="a-mono">{{ e.action }}</td>
              <td>{{ e.entity_type }}<br><span class="a-muted a-mono">{{ e.entity_id }}</span></td>
              <td>@if (e.details) { <code class="a-mono" style="white-space:pre-wrap;font-size:.72rem">{{ e.details | json }}</code> }</td></tr>
          } @empty { <tr><td colspan="5" class="a-muted">No entries.</td></tr> }
        </tbody>
      </table></div>
      <div class="a-pager"><span></span>
        <span class="a-row"><button class="ab sm" type="button" [disabled]="offset === 0" (click)="load(offset - limit)">‹ Newer</button>
          <button class="ab sm" type="button" [disabled]="items().length < limit" (click)="load(offset + limit)">Older ›</button></span></div>
    </section>
  `,
})
export class AuditComponent {
  private api = inject(AdminApi);
  readonly items = signal<Entry[]>([]);
  readonly error = signal('');
  readonly types = ['subscriber', 'campaign', 'template', 'provider', 'settings', 'email_job', 'suppression', 'page', 'oauth'];
  type = '';
  offset = 0;
  readonly limit = 50;
  constructor() {
    this.load(0);
  }
  load(offset: number) {
    this.offset = Math.max(0, offset);
    this.api.get<{ items: Entry[] }>('audit', { entity_type: this.type, limit: this.limit, offset: this.offset }).subscribe({
      next: (r) => this.items.set(r.items ?? []), error: (e) => this.error.set(errMsg(e)) });
  }
}
