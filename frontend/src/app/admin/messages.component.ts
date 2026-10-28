import { DatePipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { AdminApi, errMsg } from './admin-api.service';

interface Msg { id: string; name: string; email: string; message: string; status: string; created_at: string }

@Component({
  selector: 'app-admin-messages',
  imports: [DatePipe],
  template: `
    <section class="a-card">
      <h2>Contact messages</h2>
      <p>Messages sent through the contact form. They are deleted automatically after 24 months, as the privacy notice says.</p>
      @if (error()) { <p class="a-err">{{ error() }}</p> }
      <ul class="a-list" style="gap:12px">
        @for (m of items(); track m.id) {
          <li class="a-block" style="display:grid">
            <div class="a-block-head"><strong>{{ m.name }} <a [href]="'mailto:' + m.email" class="a-muted">&lt;{{ m.email }}&gt;</a></strong>
              <span class="a-muted">{{ m.created_at | date: 'MMM d, y HH:mm' }}</span></div>
            <p style="margin:0;white-space:pre-wrap">{{ m.message }}</p>
          </li>
        } @empty { <li class="a-muted">No messages yet.</li> }
      </ul>
      @if (items().length === limit * (page() + 1)) { <button class="ab" type="button" style="margin-top:12px" (click)="more()">Load more</button> }
    </section>
  `,
})
export class MessagesComponent {
  private api = inject(AdminApi);
  readonly items = signal<Msg[]>([]);
  readonly error = signal('');
  readonly page = signal(0);
  readonly limit = 50;
  constructor() {
    this.fetch();
  }
  private fetch() {
    this.api.get<{ items: Msg[] }>('contact-messages', { limit: this.limit, offset: this.page() * this.limit }).subscribe({
      next: (r) => this.items.update((x) => [...x, ...(r.items ?? [])]), error: (e) => this.error.set(errMsg(e)) });
  }
  more() {
    this.page.update((p) => p + 1);
    this.fetch();
  }
}
