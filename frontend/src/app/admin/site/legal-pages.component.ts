import { DatePipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { RouterLink } from '@angular/router';
import { forkJoin } from 'rxjs';
import { Page } from '../../core/models';
import { AdminApi, errMsg } from '../admin-api.service';
import { HistoryDialogComponent } from './history-dialog.component';

const LEGAL = [
  { slug: 'privacy', name: 'Privacy notice' },
  { slug: 'terms', name: 'Terms of use' },
  { slug: 'ai-policy', name: 'AI policy' },
];

interface Row {
  slug: string;
  name: string;
  page: Page | null;
}

/** The three protected legal pages (D20, D21): edit, view and history. */
@Component({
  selector: 'app-admin-legal-pages',
  imports: [DatePipe, RouterLink, HistoryDialogComponent],
  template: `
    <h1>Legal pages</h1>
    <p>
      Your privacy notice, terms of use and AI policy. The footer and the newsletter consent link to
      them, so you can edit their text but not delete, unpublish or rename them. Every saved version
      is kept, and you can restore any of them from History.
    </p>
    <p class="a-warn">
      Privacy changes: bump the privacy policy version in
      <a routerLink="/admin/email/settings">Subscribers &amp; Email → Settings</a>, so new sign-ups
      record the new version.
    </p>
    @if (error()) {
      <p class="a-err" role="alert">{{ error() }}</p>
    }
    @if (notice()) {
      <p class="a-ok" role="status">{{ notice() }}</p>
    }
    <section class="a-card" style="margin-top:14px">
      <div class="a-table-wrap">
        <table class="a-table">
          <thead>
            <tr>
              <th>Title</th>
              <th>Status</th>
              <th>Last updated</th>
              <th><span class="sr-only">Actions</span></th>
            </tr>
          </thead>
          <tbody>
            @for (r of rows(); track r.slug) {
              <tr>
                @if (r.page; as p) {
                  <td>
                    <a [routerLink]="['/admin/content', p.id]">{{ p.title }}</a
                    ><br /><span class="a-muted a-mono">{{ p.path }}</span>
                  </td>
                  <td>
                    <span
                      class="pill"
                      [class]="'pill ' + (p.status === 'published' ? 'verified' : 'pending')"
                      >{{ p.status }}</span
                    >
                  </td>
                  <td>{{ p.updated_at | date: 'd MMM y, HH:mm' }}</td>
                  <td>
                    <span class="a-row" style="gap:4px">
                      <a
                        class="ab sm"
                        [routerLink]="['/admin/content', p.id]"
                        [attr.aria-label]="'Edit ' + p.title"
                        >Edit</a
                      >
                      <a
                        class="ab sm"
                        [href]="p.path"
                        target="_blank"
                        rel="noopener"
                        [attr.aria-label]="'View ' + p.title + ' on the site'"
                        >View ↗</a
                      >
                      <button
                        class="ab sm"
                        type="button"
                        (click)="history.set(p)"
                        [attr.aria-label]="'History of ' + p.title"
                      >
                        History
                      </button>
                    </span>
                  </td>
                } @else {
                  <td>
                    {{ r.name }}<br /><span class="a-muted a-mono">/{{ r.slug }}</span>
                  </td>
                  <td colspan="3" class="a-muted">
                    Not set up yet. The API creates this page when it starts after the update;
                    reload this screen in a minute.
                  </td>
                }
              </tr>
            } @empty {
              <tr>
                <td colspan="4" class="a-muted">Loading…</td>
              </tr>
            }
          </tbody>
        </table>
      </div>
    </section>
    @if (history(); as p) {
      <app-history-dialog
        [pageId]="p.id"
        [title]="p.title"
        (pageRestored)="restored($event)"
        (closed)="history.set(null)"
      />
    }
  `,
})
export class LegalPagesComponent {
  private api = inject(AdminApi);
  readonly rows = signal<Row[]>([]);
  readonly error = signal('');
  readonly notice = signal('');
  readonly history = signal<Page | null>(null);

  constructor() {
    this.load();
  }
  load() {
    const find = (slug: string) =>
      this.api.get<{ items: Page[] }>('pages', { kind: 'page', q: slug, limit: 50 });
    forkJoin(LEGAL.map((l) => find(l.slug))).subscribe({
      next: (res) =>
        this.rows.set(
          LEGAL.map((l, i) => ({
            ...l,
            page: (res[i].items ?? []).find((p) => p.slug === l.slug && !p.parent_id) ?? null,
          })),
        ),
      error: (e) => this.error.set(errMsg(e)),
    });
  }
  restored(p: Page) {
    this.history.set(null);
    this.notice.set(`Restored “${p.title}”. The site shows it now.`);
    this.rows.update((rs) => rs.map((r) => (r.page?.id === p.id ? { ...r, page: p } : r)));
  }
}
