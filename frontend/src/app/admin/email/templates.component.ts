import { DatePipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { AdminApi, errMsg } from '../admin-api.service';

interface Template { id: string; slug: string; name: string; category: string; subject: string; preheader: string;
  html_body: string; text_body: string; is_system: boolean; created_at: string; updated_at: string }

const blank = (): Partial<Template> => ({ slug: '', name: '', category: 'marketing', subject: '', preheader: '', html_body: '<p>Hello {{first_name}},</p>\n{{content}}', text_body: '' });

@Component({
  selector: 'app-admin-templates',
  imports: [FormsModule, DatePipe],
  template: `
    <div class="a-split">
      <section class="a-card">
        <div class="a-row between">
          <div><h2>Templates</h2><p style="margin:0">System templates power opt-in, welcome and alert emails. Edit the wording, not the variables.</p></div>
          <button class="ab primary" type="button" (click)="edit(null)">New template</button>
        </div>
        @if (error()) { <p class="a-err">{{ error() }}</p> }
        <div class="a-table-wrap" style="margin-top:14px">
          <table class="a-table">
            <thead><tr><th>Name</th><th>Category</th><th>Subject</th><th>Updated</th></tr></thead>
            <tbody>
              @for (t of items(); track t.id) {
                <tr [class.sel]="current()?.id === t.id">
                  <td><button class="ab sm" type="button" style="background:none;border:0;padding:0" (click)="edit(t)">{{ t.name }}</button>
                    @if (t.is_system) { <span class="pill info" style="margin-left:6px">system</span> }<br><span class="a-muted a-mono">{{ t.slug }}</span></td>
                  <td>{{ t.category }}</td>
                  <td>{{ t.subject }}</td>
                  <td>{{ t.updated_at | date: 'MMM d, y' }}</td>
                </tr>
              } @empty { <tr><td colspan="4" class="a-muted">No templates.</td></tr> }
            </tbody>
          </table>
        </div>
      </section>

      <aside class="a-card" aria-label="Template editor">
        @if (current(); as t) {
          <form class="a-form" (ngSubmit)="save()">
            <h3 style="margin:0">{{ t.id ? 'Edit template' : 'New template' }}</h3>
            <div class="two">
              <label>Name<input name="name" [(ngModel)]="t.name" required maxlength="120" /></label>
              <label>Slug<input name="slug" [(ngModel)]="t.slug" required pattern="[a-z0-9_-]+" [disabled]="!!t.is_system" /></label>
            </div>
            <label>Category<select name="category" [(ngModel)]="t.category" [disabled]="!!t.is_system">
              <option value="marketing">Marketing (adds unsubscribe footer)</option><option value="transactional">Transactional</option></select></label>
            <label>Subject<input name="subject" [(ngModel)]="t.subject" required maxlength="200" /></label>
            <label>Preheader<input name="preheader" [(ngModel)]="t.preheader" maxlength="200" /></label>
            <label>HTML body<textarea name="html" rows="12" class="a-mono" [(ngModel)]="t.html_body"></textarea></label>
            <p class="a-muted" style="margin:0;font-size:.78rem">Variables use double braces, for example <code>{{ '{{first_name}}' }}</code>. Values are escaped. Scripts, iframes and event handlers are removed on save.</p>
            <label>Plain-text body (optional, generated from HTML when empty)<textarea name="text" rows="4" [(ngModel)]="t.text_body"></textarea></label>
            @if (notice()) { <p class="a-ok">{{ notice() }}</p> }
            <div class="a-row">
              <button class="ab primary" type="submit" [disabled]="busy()">Save</button>
              @if (t.id) {
                <button class="ab" type="button" (click)="preview(t.id)">Preview</button>
                @if (!t.is_system) { <button class="ab danger" type="button" (click)="remove(t)">Delete</button> }
              }
              <button class="ab" type="button" (click)="current.set(null)">Close</button>
            </div>
          </form>
          @if (previewHtml()) {
            <div class="a-section"><p class="a-muted" style="margin:0 0 6px">Subject: {{ previewSubject() }}</p>
              <iframe class="a-preview" title="Template preview" sandbox="" [attr.srcdoc]="previewHtml()"></iframe></div>
          }
        } @else {
          <p class="a-muted">Select a template to edit or preview it.</p>
        }
      </aside>
    </div>
  `,
})
export class TemplatesComponent {
  private api = inject(AdminApi);
  readonly items = signal<Template[]>([]);
  readonly current = signal<Partial<Template> | null>(null);
  readonly error = signal('');
  readonly notice = signal('');
  readonly busy = signal(false);
  readonly previewHtml = signal('');
  readonly previewSubject = signal('');

  constructor() {
    this.load();
  }
  load() {
    this.api.get<{ items: Template[] }>('templates').subscribe({ next: (r) => this.items.set(r.items), error: (e) => this.error.set(errMsg(e)) });
  }
  edit(t: Template | null) {
    this.notice.set('');
    this.previewHtml.set('');
    this.current.set(t ? { ...t } : blank());
  }
  save() {
    const t = this.current();
    if (!t) return;
    this.busy.set(true);
    this.error.set('');
    const body = { slug: t.slug, name: t.name, category: t.category, subject: t.subject, preheader: t.preheader, html_body: t.html_body, text_body: t.text_body };
    const req = t.id ? this.api.put<Template>('templates/' + t.id, body) : this.api.post<Template>('templates', body);
    req.subscribe({
      next: (saved) => { this.busy.set(false); this.current.set({ ...saved }); this.notice.set('Saved.'); this.load(); },
      error: (e) => { this.busy.set(false); this.error.set(errMsg(e)); },
    });
  }
  preview(id: string) {
    this.api.get<{ subject: string; html: string }>('templates/' + id + '/preview').subscribe({
      next: (r) => { this.previewSubject.set(r.subject); this.previewHtml.set(r.html); },
      error: (e) => this.error.set(errMsg(e)),
    });
  }
  remove(t: Partial<Template>) {
    if (!t.id || !confirm(`Delete template "${t.name}"? Campaigns using it keep their own copy of the content.`)) return;
    this.api.delete('templates/' + t.id).subscribe({ next: () => { this.current.set(null); this.load(); }, error: (e) => this.error.set(errMsg(e)) });
  }
}
