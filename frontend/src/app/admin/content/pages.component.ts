import { DatePipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { RouterLink } from '@angular/router';
import { Page } from '../../core/models';
import { AdminApi, errMsg } from '../admin-api.service';

@Component({
  selector: 'app-admin-pages',
  imports: [FormsModule, RouterLink, DatePipe],
  template: `
    <section class="a-card">
      <div class="a-row between">
        <div><h2>Pages and articles</h2><p style="margin:0">Build pages without code. Published content is server-rendered with SEO tags, Open Graph and structured data.</p></div>
        <div class="a-row">
          <a class="ab" routerLink="new" [queryParams]="{ kind: 'page' }">New page</a>
          <a class="ab primary" routerLink="new" [queryParams]="{ kind: 'article' }">New article</a>
        </div>
      </div>
      <div class="a-filters">
        <label class="f">Type<select [(ngModel)]="kind" (change)="load(0)"><option value="">All</option><option value="article">Articles</option><option value="page">Pages</option></select></label>
        <label class="f">Status<select [(ngModel)]="status" (change)="load(0)"><option value="">All</option><option value="draft">Draft</option><option value="published">Published</option></select></label>
        <label class="f">Search<input type="search" [(ngModel)]="q" (keyup.enter)="load(0)" placeholder="Title or slug" /></label>
      </div>
      @if (error()) { <p class="a-err">{{ error() }}</p> }
      <div class="a-table-wrap"><table class="a-table">
        <thead><tr><th>Title</th><th>Type</th><th>Status</th><th>Hero</th><th>Updated</th><th><span class="sr-only">Actions</span></th></tr></thead>
        <tbody>
          @for (p of items(); track p.id) {
            <tr>
              <td><a [routerLink]="p.id">{{ p.title }}</a><br><span class="a-muted a-mono">{{ p.path }}</span>@if (p.source && p.source !== 'admin') { <span class="pill info" style="margin-left:6px">{{ p.source }}</span> }</td>
              <td>{{ p.kind }}</td>
              <td><span class="pill" [class]="'pill ' + (p.status === 'published' ? 'verified' : 'pending')">{{ p.status }}</span>
                @if (p.published_at) { <br><span class="a-muted">{{ p.published_at | date: 'MMM d, y' }}</span> }</td>
              <td>{{ p.hero.type === 'none' ? '—' : p.hero.type }}</td>
              <td>{{ p.updated_at | date: 'MMM d, HH:mm' }}</td>
              <td class="a-row" style="gap:4px">
                <a class="ab sm" [routerLink]="p.id">Edit</a>
                @if (p.status === 'published') { <a class="ab sm" [href]="p.path" target="_blank" rel="noopener">View</a> }
              </td>
            </tr>
          } @empty { <tr><td colspan="6" class="a-muted">Nothing here yet. Create a page, or ask your AI assistant to do it through the MCP connector.</td></tr> }
        </tbody>
      </table></div>
      <div class="a-pager"><span>{{ total() }} total</span>
        <span class="a-row"><button class="ab sm" type="button" [disabled]="offset === 0" (click)="load(offset - limit)">‹ Prev</button>
          <button class="ab sm" type="button" [disabled]="offset + limit >= total()" (click)="load(offset + limit)">Next ›</button></span></div>
    </section>
  `,
})
export class PagesComponent {
  private api = inject(AdminApi);
  readonly items = signal<Page[]>([]);
  readonly total = signal(0);
  readonly error = signal('');
  kind = '';
  status = '';
  q = '';
  offset = 0;
  readonly limit = 25;

  constructor() {
    this.load(0);
  }
  load(offset: number) {
    this.offset = Math.max(0, offset);
    this.api.get<{ items: Page[]; total: number }>('pages', { kind: this.kind, status: this.status, q: this.q.trim(), limit: this.limit, offset: this.offset })
      .subscribe({ next: (r) => { this.items.set(r.items ?? []); this.total.set(r.total); }, error: (e) => this.error.set(errMsg(e)) });
  }
}
