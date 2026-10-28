import { DatePipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { AdminApi, errMsg } from './admin-api.service';

interface Msg { id: string; name: string; email: string; message: string; status: string; forwarded_at: string | null; created_at: string }
interface BlockedWord { id: string; word: string; hits: number; last_hit_at: string | null; created_at: string }

@Component({
  selector: 'app-admin-messages',
  imports: [DatePipe, FormsModule],
  template: `
    <section class="a-card">
      <div class="a-row between">
        <div><h2>Contact messages</h2>
          <p style="margin:0">New messages wait here for you first. Forward one to your inbox when you want it by email. Messages are deleted automatically after 24 months, as the privacy notice says.</p></div>
        <select [(ngModel)]="status" (change)="reload()" aria-label="Filter by status">
          <option value="new">New</option><option value="read">Forwarded / read</option><option value="archived">Archived</option><option value="">All</option></select>
      </div>
      @if (error()) { <p class="a-err">{{ error() }}</p> }
      @if (notice()) { <p class="a-muted" role="status">{{ notice() }}</p> }
      <ul class="a-list" style="gap:12px;margin-top:14px">
        @for (m of items(); track m.id) {
          <li class="a-block" style="display:grid;gap:8px">
            <div class="a-block-head"><strong>{{ m.name }} <a [href]="'mailto:' + m.email" class="a-muted">&lt;{{ m.email }}&gt;</a></strong>
              <span class="a-muted">{{ m.created_at | date: 'MMM d, y HH:mm' }}</span></div>
            <p style="margin:0;white-space:pre-wrap">{{ m.message }}</p>
            <div class="a-row">
              @if (m.forwarded_at) { <span class="a-muted">Forwarded {{ m.forwarded_at | date: 'MMM d, HH:mm' }}</span> }
              @else { <button class="ab sm primary" type="button" (click)="forward(m)" [disabled]="busy() === m.id">Forward to inbox</button> }
              @if (m.status !== 'archived') { <button class="ab sm" type="button" (click)="archive(m)" [disabled]="busy() === m.id">Archive</button> }
            </div>
          </li>
        } @empty { <li class="a-muted">No messages here.</li> }
      </ul>
      @if (items().length === limit * (page() + 1)) { <button class="ab" type="button" style="margin-top:12px" (click)="more()">Load more</button> }
    </section>

    <section class="a-card">
      <h2>Spam filter: blocked words</h2>
      <p>A message whose name or text contains one of these words or phrases is refused, and the sender is told to stop. Matching ignores case and only matches whole words, so "cialis" does not block "specialist".</p>
      @if (wordError()) { <p class="a-err">{{ wordError() }}</p> }
      <form class="a-row" (ngSubmit)="addWord()">
        <input name="word" [(ngModel)]="newWord" maxlength="100" placeholder="Word or phrase" aria-label="Word or phrase to block" />
        <button class="ab primary" type="submit" [disabled]="!newWord.trim()">Block</button>
      </form>
      <div class="a-table-wrap" style="margin-top:14px"><table class="a-table">
        <thead><tr><th>Word or phrase</th><th>Blocked messages</th><th>Last blocked</th><th><span class="sr-only">Actions</span></th></tr></thead>
        <tbody>
          @for (w of words(); track w.id) {
            <tr>
              <td>{{ w.word }}</td>
              <td>{{ w.hits }}</td>
              <td>{{ w.last_hit_at ? (w.last_hit_at | date: 'MMM d, y') : '—' }}</td>
              <td><button class="ab sm" type="button" (click)="removeWord(w)">Remove</button></td>
            </tr>
          } @empty { <tr><td colspan="4" class="a-muted">No blocked words. Every message is accepted.</td></tr> }
        </tbody>
      </table></div>
    </section>
  `,
})
export class MessagesComponent {
  private api = inject(AdminApi);
  readonly items = signal<Msg[]>([]);
  readonly words = signal<BlockedWord[]>([]);
  readonly error = signal('');
  readonly wordError = signal('');
  readonly notice = signal('');
  readonly busy = signal('');
  readonly page = signal(0);
  readonly limit = 50;
  status = 'new';
  newWord = '';
  constructor() {
    this.fetch();
    this.loadWords();
  }
  private fetch() {
    this.api.get<{ items: Msg[] }>('contact-messages', { limit: this.limit, offset: this.page() * this.limit, status: this.status }).subscribe({
      next: (r) => this.items.update((x) => [...x, ...(r.items ?? [])]), error: (e) => this.error.set(errMsg(e)) });
  }
  reload() {
    this.page.set(0);
    this.items.set([]);
    this.fetch();
  }
  more() {
    this.page.update((p) => p + 1);
    this.fetch();
  }
  forward(m: Msg) {
    this.act(m, 'forward', 'Forwarded to your inbox.');
  }
  archive(m: Msg) {
    this.act(m, 'archive', 'Archived.');
  }
  private act(m: Msg, action: string, done: string) {
    this.error.set('');
    this.notice.set('');
    this.busy.set(m.id);
    this.api.post(`contact-messages/${m.id}/${action}`).subscribe({
      next: () => { this.busy.set(''); this.notice.set(done); this.reload(); },
      error: (e) => { this.busy.set(''); this.error.set(errMsg(e)); },
    });
  }
  private loadWords() {
    this.api.get<{ items: BlockedWord[] }>('contact-blocked-words').subscribe({
      next: (r) => this.words.set(r.items ?? []), error: (e) => this.wordError.set(errMsg(e)) });
  }
  addWord() {
    this.wordError.set('');
    this.api.post('contact-blocked-words', { word: this.newWord }).subscribe({
      next: () => { this.newWord = ''; this.loadWords(); }, error: (e) => this.wordError.set(errMsg(e)) });
  }
  removeWord(w: BlockedWord) {
    this.wordError.set('');
    this.api.delete(`contact-blocked-words/${w.id}`).subscribe({
      next: () => this.loadWords(), error: (e) => this.wordError.set(errMsg(e)) });
  }
}
