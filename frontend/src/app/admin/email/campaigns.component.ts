import { DatePipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { RouterLink } from '@angular/router';
import { AdminApi, errMsg } from '../admin-api.service';

export interface CampaignStats { recipients: number; accepted: number; delivered: number; bounced: number; complained: number; failed: number; pending: number; opened: number; clicked: number }
export interface Campaign { id: string; name: string; kind: string; status: string; subject: string; preheader: string; template_id: string | null;
  content_html: string; content_text: string; segment: { topics?: string[]; sources?: string[] }; blog_post_id: string | null;
  scheduled_at: string | null; approved_by: string; approved_at: string | null; audience_count: number | null; completed_at: string | null;
  created_at: string; updated_at: string; stats?: CampaignStats }

@Component({
  selector: 'app-admin-campaigns',
  imports: [RouterLink, DatePipe, FormsModule],
  template: `
    <section class="a-card">
      <div class="a-row between">
        <div><h2>Campaigns</h2><p style="margin:0">Drafts need your explicit approval before anything is sent.</p></div>
        <div class="a-row">
          <select [(ngModel)]="status" (change)="load()" aria-label="Filter by status">
            <option value="">All statuses</option><option value="draft">Draft</option><option value="scheduled">Scheduled</option>
            <option value="sending">Sending</option><option value="sent">Sent</option><option value="cancelled">Cancelled</option></select>
          <a class="ab primary" routerLink="new">New campaign</a>
        </div>
      </div>
      @if (error()) { <p class="a-err">{{ error() }}</p> }
      <div class="a-table-wrap" style="margin-top:14px">
        <table class="a-table">
          <thead><tr><th>Name</th><th>Status</th><th>Audience</th><th>Delivered</th><th>Bounced</th><th>Updated</th></tr></thead>
          <tbody>
            @for (c of items(); track c.id) {
              <tr>
                <td><a [routerLink]="c.id">{{ c.name }}</a>@if (c.kind === 'blog_notification') { <span class="pill info" style="margin-left:6px">blog</span> }<br><span class="a-muted">{{ c.subject }}</span></td>
                <td><span class="pill" [class]="'pill ' + c.status">{{ c.status }}</span>@if (c.scheduled_at && c.status === 'scheduled') { <br><span class="a-muted">{{ c.scheduled_at | date: 'medium' }}</span> }</td>
                <td>{{ c.audience_count ?? '—' }}</td>
                <td>{{ c.stats?.delivered ?? '—' }}</td>
                <td>{{ c.stats?.bounced ?? '—' }}</td>
                <td>{{ c.updated_at | date: 'MMM d, HH:mm' }}</td>
              </tr>
            } @empty { <tr><td colspan="6" class="a-muted">No campaigns yet. Publishing an article with "notify subscribers" also creates a draft here.</td></tr> }
          </tbody>
        </table>
      </div>
    </section>
  `,
})
export class CampaignsComponent {
  private api = inject(AdminApi);
  readonly items = signal<Campaign[]>([]);
  readonly error = signal('');
  status = '';
  constructor() {
    this.load();
  }
  load() {
    this.api.get<{ items: Campaign[] }>('campaigns', { status: this.status, limit: 100 }).subscribe({
      next: (r) => this.items.set(r.items), error: (e) => this.error.set(errMsg(e)) });
  }
}
