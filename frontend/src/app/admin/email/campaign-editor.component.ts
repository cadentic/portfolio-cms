import { DatePipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { AdminApi, errMsg } from '../admin-api.service';
import { Campaign } from './campaigns.component';

interface Audience { count: number; audience_hash: string; sample: string[] }

@Component({
  selector: 'app-admin-campaign-editor',
  imports: [FormsModule, RouterLink, DatePipe],
  template: `
    <a routerLink="/admin/email/campaigns" class="a-muted">← All campaigns</a>
    @if (error()) { <p class="a-err" style="margin-top:10px">{{ error() }}</p> }
    @if (notice()) { <p class="a-ok" style="margin-top:10px">{{ notice() }}</p> }
    <div class="a-split" style="margin-top:12px">
      <section class="a-card a-form">
        <div class="a-row between"><h2>{{ isNew ? 'New campaign' : c.name }}</h2>@if (!isNew) { <span class="pill" [class]="'pill ' + c.status">{{ c.status }}</span> }</div>
        <fieldset [disabled]="!editable()" style="border:0;padding:0;margin:0;display:grid;gap:12px">
          <div class="two">
            <label class="f">Internal name<input name="name" [(ngModel)]="c.name" required /></label>
            <label class="f">Template<select name="tpl" [(ngModel)]="c.template_id"><option [ngValue]="null">Newsletter (default)</option>
              @for (t of templates(); track t.id) { <option [ngValue]="t.id">{{ t.name }}</option> }</select></label>
          </div>
          <label class="f">Subject line<input name="subject" [(ngModel)]="c.subject" required maxlength="200" /></label>
          <label class="f">Preheader (inbox preview text)<input name="pre" [(ngModel)]="c.preheader" maxlength="200" /></label>
          <label class="f">Content (HTML; use {{ '{{first_name}}' }} for personalisation. Header, footer and unsubscribe link are added automatically)
            <textarea class="code" name="html" rows="14" [(ngModel)]="c.content_html"></textarea></label>
          <div class="two">
            <label class="f">Audience: topics (comma separated, empty = everyone)<input name="topics" [(ngModel)]="topics" placeholder="articles, videos" /></label>
            <label class="f">Audience: sources (comma separated, empty = all except the Google Contacts list; enter google_contacts to mail that list)<input name="sources" [(ngModel)]="sources" placeholder="website, blog" /></label>
          </div>
        </fieldset>
        <div class="a-row">
          @if (editable()) { <button class="ab primary" type="button" (click)="save()" [disabled]="busy()">{{ isNew ? 'Create draft' : 'Save draft' }}</button> }
          @if (!isNew) {
            <button class="ab" type="button" (click)="loadPreview()">Preview</button>
            <a class="ab" [routerLink]="[]" (click)="report($event)">Export report CSV</a>
            @if (c.status === 'draft') { <button class="ab danger" type="button" (click)="remove()">Delete draft</button> }
            @if (c.status === 'scheduled' || c.status === 'sending') { <button class="ab danger" type="button" (click)="cancel()">Cancel sending</button> }
          }
        </div>
        @if (previewHtml()) {
          <div class="a-section"><h3>Preview: {{ previewSubject() }}</h3>
            <iframe class="a-preview" title="Email preview" sandbox="" [attr.srcdoc]="previewHtml()"></iframe></div>
        }
      </section>

      @if (!isNew) {
        <aside class="a-grid">
          <section class="a-card a-form">
            <h3>Send a test</h3>
            <label class="f">Recipients (must be in your test list in Settings)<input name="test" [(ngModel)]="testTo" placeholder="you@cadentic.dev" /></label>
            <button class="ab" type="button" (click)="test()" [disabled]="busy()">Send test email</button>
          </section>
          @if (c.status === 'draft') {
            <section class="a-card a-form">
              <h3>Approve &amp; send</h3>
              @if (!audience()) {
                <p style="margin:0">Check who will receive it first.</p>
                <button class="ab" type="button" (click)="checkAudience()">Calculate audience</button>
              } @else {
                @let a = audience()!;
                <p style="margin:0"><strong>{{ a.count }}</strong> eligible subscribers (verified, consented, not suppressed).</p>
                <p class="a-muted a-mono" style="margin:0">{{ a.sample.join(', ') }}</p>
                <label class="f">Schedule (optional, your local time)<input type="datetime-local" name="sched" [(ngModel)]="schedule" /></label>
                <label class="a-row"><input type="checkbox" name="similar" [(ngModel)]="allowSimilar" /> Allow even if a similar subject was sent in the last 24 hours</label>
                <label class="a-row"><input type="checkbox" name="confirm" [(ngModel)]="confirmSend" /> I confirm sending "{{ c.subject }}" to {{ a.count }} people.</label>
                <button class="ab primary" type="button" (click)="approve()" [disabled]="!confirmSend || busy() || !a.count">{{ schedule ? 'Schedule' : 'Send now' }}</button>
              }
            </section>
          }
          @if (c.stats; as s) {
            <section class="a-card">
              <h3>Results</h3>
              <dl class="a-kv"><dt>Recipients</dt><dd>{{ s.recipients }}</dd><dt>Accepted</dt><dd>{{ s.accepted }}</dd><dt>Delivered</dt><dd>{{ s.delivered }}</dd>
                <dt>Pending</dt><dd>{{ s.pending }}</dd><dt>Bounced</dt><dd>{{ s.bounced }}</dd><dt>Complaints</dt><dd>{{ s.complained }}</dd><dt>Failed</dt><dd>{{ s.failed }}</dd>
                <dt>Approved</dt><dd>{{ (c.approved_at | date: 'medium') ?? '—' }} {{ c.approved_by }}</dd></dl>
            </section>
          }
        </aside>
      }
    </div>
  `,
})
export class CampaignEditorComponent {
  private api = inject(AdminApi);
  private router = inject(Router);
  readonly id = inject(ActivatedRoute).snapshot.paramMap.get('id')!;
  readonly isNew = this.id === 'new';
  c: Campaign = { id: '', name: '', kind: 'newsletter', status: 'draft', subject: '', preheader: '', template_id: null, content_html: '<p>Hi {{first_name}},</p>\n<p></p>',
    content_text: '', segment: {}, blog_post_id: null, scheduled_at: null, approved_by: '', approved_at: null, audience_count: null, completed_at: null, created_at: '', updated_at: '' };
  topics = '';
  sources = '';
  testTo = '';
  schedule = '';
  allowSimilar = false;
  confirmSend = false;
  readonly templates = signal<Array<{ id: string; name: string; category: string }>>([]);
  readonly audience = signal<Audience | null>(null);
  readonly previewHtml = signal('');
  readonly previewSubject = signal('');
  readonly error = signal('');
  readonly notice = signal('');
  readonly busy = signal(false);

  constructor() {
    this.api.get<{ items: Array<{ id: string; name: string; category: string }> }>('templates').subscribe((r) =>
      this.templates.set(r.items.filter((t) => t.category === 'marketing')));
    if (!this.isNew) this.reload();
  }

  editable() {
    return this.isNew || this.c.status === 'draft';
  }
  private split(s: string) {
    return s.split(',').map((x) => x.trim()).filter(Boolean);
  }
  private fail = (e: unknown) => { this.busy.set(false); this.error.set(errMsg(e)); };

  reload() {
    this.api.get<Campaign>('campaigns/' + this.id).subscribe({
      next: (c) => { this.c = c; this.topics = (c.segment.topics ?? []).join(', '); this.sources = (c.segment.sources ?? []).join(', '); },
      error: this.fail,
    });
  }

  save() {
    this.busy.set(true);
    this.error.set('');
    const body = { name: this.c.name, subject: this.c.subject, preheader: this.c.preheader, template_id: this.c.template_id,
      content_html: this.c.content_html, segment: { topics: this.split(this.topics), sources: this.split(this.sources) } };
    const req = this.isNew ? this.api.post<Campaign>('campaigns', body) : this.api.patch<Campaign>('campaigns/' + this.id, body);
    req.subscribe({
      next: (c) => { this.busy.set(false); this.audience.set(null); this.notice.set('Draft saved.');
        if (this.isNew) this.router.navigate(['/admin/email/campaigns', c.id]); else this.c = c; },
      error: this.fail,
    });
  }

  loadPreview() {
    this.api.get<{ subject: string; html: string }>('campaigns/' + this.id + '/preview').subscribe({
      next: (p) => { this.previewSubject.set(p.subject); this.previewHtml.set(p.html); }, error: this.fail });
  }

  test() {
    this.busy.set(true);
    this.api.post<{ queued: number }>('campaigns/' + this.id + '/test', { recipients: this.split(this.testTo) }).subscribe({
      next: (r) => { this.busy.set(false); this.notice.set(`Test email queued for ${r.queued} recipient(s).`); }, error: this.fail });
  }

  checkAudience() {
    this.api.post<Audience>('campaigns/' + this.id + '/audience').subscribe({ next: (a) => this.audience.set(a), error: this.fail });
  }

  approve() {
    const a = this.audience();
    if (!a) return;
    this.busy.set(true);
    this.api.post<Campaign>('campaigns/' + this.id + '/send', { confirm: this.confirmSend, audience_hash: a.audience_hash, expected_count: a.count,
      scheduled_at: this.schedule ? new Date(this.schedule).toISOString() : null, allow_similar_recent: this.allowSimilar }).subscribe({
      next: (c) => { this.busy.set(false); this.c = c; this.notice.set(c.status === 'scheduled' ? 'Campaign scheduled.' : 'Sending started. Delivery runs in the background.'); },
      error: (e) => { this.fail(e); this.audience.set(null); this.confirmSend = false; },
    });
  }

  cancel() {
    if (!confirm('Cancel this campaign? Messages not yet sent will be cancelled.')) return;
    this.api.post('campaigns/' + this.id + '/cancel').subscribe({ next: () => this.reload(), error: this.fail });
  }
  remove() {
    if (!confirm('Delete this draft?')) return;
    this.api.delete('campaigns/' + this.id).subscribe({ next: () => this.router.navigateByUrl('/admin/email/campaigns'), error: this.fail });
  }
  report(e: Event) {
    e.preventDefault();
    this.api.download('campaigns/' + this.id + '/report.csv', `campaign-${this.id}.csv`);
  }
}
