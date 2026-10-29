import { Component, computed, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { Block, Hero, MediaItem, Page } from '../../core/models';
import { BlocksComponent } from '../../shared/blocks.component';
import { HeroMediaComponent } from '../../shared/hero-media.component';
import { AdminApi, errMsg } from '../admin-api.service';
import { MediaPickerComponent } from '../media/media-picker.component';

type Kind = 'page' | 'article';
interface Draft { id?: string; kind: Kind; title: string; slug: string; excerpt: string; hero: Hero; blocks: Block[];
  featured_image_url: string; author_name: string; categories: string; tags: string; seo_title: string; seo_description: string;
  og_image: string; canonical: string; noindex: boolean; status: string; path: string; first_published_at: string | null }

const BLOCKS: Array<{ type: Block['type']; label: string }> = [
  { type: 'heading', label: 'Heading' }, { type: 'paragraph', label: 'Paragraph' }, { type: 'list', label: 'List' },
  { type: 'quote', label: 'Quote' }, { type: 'callout', label: 'Callout' }, { type: 'code', label: 'Code' },
  { type: 'divider', label: 'Divider' }, { type: 'html', label: 'HTML' },
];

// Linked from the footer and the newsletter consent: editable, never deleted, unpublished or renamed (D21).
const LEGAL_PATHS = ['/privacy', '/terms', '/ai-policy'];

function slugify(s: string) {
  return s.normalize('NFKD').toLowerCase().replace(/[̀-ͯ]/g, '').replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 80);
}

@Component({
  selector: 'app-admin-page-editor',
  imports: [FormsModule, RouterLink, BlocksComponent, HeroMediaComponent, MediaPickerComponent],
  template: `
    @if (d(); as d) {
      <div class="a-row between" style="margin-bottom:14px">
        <div><a routerLink="/admin/content" class="a-muted">← All content</a>
          <h2 style="margin:4px 0 0">{{ d.id ? d.title || 'Untitled' : 'New ' + d.kind }}</h2>
          @if (d.id) { <span class="pill" [class]="'pill ' + (d.status === 'published' ? 'verified' : 'pending')">{{ d.status }}</span> <span class="a-muted a-mono">{{ d.path }}</span> }</div>
        <div class="a-row">
          <button class="ab" type="button" (click)="mode.set(mode() === 'edit' ? 'preview' : 'edit')">{{ mode() === 'edit' ? 'Preview' : 'Back to editing' }}</button>
          <button class="ab" type="button" [disabled]="busy()" (click)="save()">{{ d.status === 'published' ? 'Save changes (live)' : 'Save draft' }}</button>
          @if (d.status === 'published') {
            @if (!legal()) { <button class="ab" type="button" [disabled]="busy()" (click)="unpublish()">Unpublish</button> }
            <a class="ab" [href]="d.path" target="_blank" rel="noopener">View live</a>
          } @else {
            <button class="ab primary" type="button" [disabled]="busy()" (click)="publishOpen.set(true)">Publish…</button>
          }
        </div>
      </div>
      @if (legal()) {
        <div class="a-warn" style="margin-bottom:14px">
          This legal page is linked from the footer and newsletter consent. You can edit its text; it can't be deleted, unpublished or renamed.
          Every saved version is kept under <a routerLink="/admin/site/legal">Site content → Legal pages</a>.
          @if (d.path === '/privacy') {
            <br />After changing it, update the privacy policy version in <a routerLink="/admin/email/settings">Subscribers &amp; Email → Settings</a>.
          }
        </div>
      }
      @if (error()) { <p class="a-err" role="alert">{{ error() }}</p> }
      @if (notice()) { <p class="a-ok">{{ notice() }}</p> }

      @if (mode() === 'preview') {
        <section class="a-card" style="background:#fff;color:#111923">
          <article class="prose" style="max-width:760px;margin:0 auto">
            <app-hero-media [hero]="d.hero" [title]="d.title" />
            <h1>{{ d.hero.heading || d.title }}</h1>
            @if (d.hero.subheading || d.excerpt) { <p class="lead">{{ d.hero.subheading || d.excerpt }}</p> }
            <app-blocks [blocks]="d.blocks" />
          </article>
        </section>
      } @else {
        <div class="a-split">
          <div style="display:grid;gap:16px">
            <section class="a-card a-form">
              <label>Title<input name="title" [(ngModel)]="d.title" (ngModelChange)="autoSlug($event)" maxlength="200" required /></label>
              <div class="two">
                <label>URL slug<input name="slug" [(ngModel)]="d.slug" (ngModelChange)="slugTouched = true" class="a-mono" [readonly]="legal()" /></label>
                <label>Type<select name="kind" [(ngModel)]="d.kind" [disabled]="!!d.id"><option value="article">Article (/blog/…)</option><option value="page">Page (/…)</option></select></label>
              </div>
              <label>Summary / excerpt<textarea name="excerpt" rows="2" [(ngModel)]="d.excerpt" maxlength="500"></textarea></label>
            </section>

            <section class="a-card a-form">
              <h3 style="margin:0">Hero</h3>
              <div class="a-hero-pick" role="radiogroup" aria-label="Hero type">
                @for (h of heroTypes; track h.v) {
                  <label [class.on]="d.hero.type === h.v"><input type="radio" name="herotype" [value]="h.v" [(ngModel)]="d.hero.type" />{{ h.label }}</label>
                }
              </div>
              @switch (d.hero.type) {
                @case ('image') {
                  <label>Image URL<input name="hurl" [(ngModel)]="d.hero.url" placeholder="https://… or /media/…" /></label>
                  <app-media-picker kind="image" [alt]="d.hero.alt ?? ''" (picked)="d.hero.url = $event.url; d.hero.alt = d.hero.alt || $event.alt" />
                  <label>Alt text (describe the image)<input name="halt" [(ngModel)]="d.hero.alt" required /></label>
                }
                @case ('video') {
                  <label>Video URL (MP4/WebM)<input name="hurl" [(ngModel)]="d.hero.url" placeholder="https://…/video.mp4 or /media/…" /></label>
                  <app-media-picker kind="video" label="Upload video…" (picked)="d.hero.url = $event.url" />
                  <label>Poster image URL<input name="hposter" [(ngModel)]="d.hero.poster" /></label>
                  <app-media-picker kind="image" label="Upload poster…" (picked)="d.hero.poster = $event.url" />
                }
                @case ('youtube') {
                  <label>YouTube URL or video ID<input name="hyt" [(ngModel)]="d.hero.youtube_id" placeholder="https://www.youtube.com/watch?v=…" /></label>
                  <p class="a-muted" style="margin:0;font-size:.78rem">Visitors see a thumbnail first; the privacy-enhanced player loads only when they click play.</p>
                }
              }
              @if (d.hero.type !== 'none') { <label>Caption<input name="hcap" [(ngModel)]="d.hero.caption" /></label> }
              <div class="two">
                <label>Hero heading (optional, defaults to title)<input name="hh" [(ngModel)]="d.hero.heading" /></label>
                <label>Hero subheading<input name="hs" [(ngModel)]="d.hero.subheading" /></label>
              </div>
            </section>

            <section class="a-card a-form">
              <div class="a-row between"><h3 style="margin:0">Content</h3>
                <button class="ab sm" type="button" (click)="mdOpen.set(!mdOpen())">{{ mdOpen() ? 'Close import' : 'Import Markdown' }}</button></div>
              @if (mdOpen()) {
                <label>Paste Markdown<textarea name="md" rows="8" class="a-mono" [(ngModel)]="markdown"></textarea></label>
                <div class="a-row"><button class="ab" type="button" (click)="importMd(false)">Append blocks</button><button class="ab" type="button" (click)="importMd(true)">Replace content</button></div>
              }
              @for (b of d.blocks; track $index; let i = $index) {
                <div class="a-block">
                  <div class="a-block-head">
                    <strong style="font-size:.82rem;text-transform:capitalize">{{ b.type }}</strong>
                    <span class="a-row" style="gap:4px">
                      <button class="ab sm" type="button" [disabled]="i === 0" (click)="move(i, -1)" aria-label="Move block up">↑</button>
                      <button class="ab sm" type="button" [disabled]="i === d.blocks.length - 1" (click)="move(i, 1)" aria-label="Move block down">↓</button>
                      <button class="ab sm danger" type="button" (click)="d.blocks.splice(i, 1)" aria-label="Remove block">✕</button>
                    </span>
                  </div>
                  @switch (b.type) {
                    @case ('heading') {
                      <div class="a-row"><select [name]="'lvl' + i" [(ngModel)]="b.level" aria-label="Heading level" style="width:90px"><option [ngValue]="2">H2</option><option [ngValue]="3">H3</option><option [ngValue]="4">H4</option></select>
                        <input [name]="'t' + i" [(ngModel)]="b.text" aria-label="Heading text" style="flex:1" /></div>
                    }
                    @case ('list') {
                      <select [name]="'ls' + i" [(ngModel)]="b.style" aria-label="List style"><option value="bullet">Bulleted</option><option value="numbered">Numbered</option></select>
                      <textarea [name]="'li' + i" rows="4" [ngModel]="(b.items ?? []).join('\n')" (ngModelChange)="b.items = lines($event)" aria-label="One item per line"></textarea>
                    }
                    @case ('quote') {
                      <textarea [name]="'t' + i" rows="3" [(ngModel)]="b.text" aria-label="Quote"></textarea>
                      <input [name]="'c' + i" [(ngModel)]="b.cite" placeholder="Who said it (optional)" aria-label="Citation" />
                    }
                    @case ('callout') {
                      <select [name]="'tone' + i" [(ngModel)]="b.tone" aria-label="Callout tone"><option value="info">Info</option><option value="success">Success</option><option value="warning">Warning</option></select>
                      <textarea [name]="'t' + i" rows="3" [(ngModel)]="b.text" aria-label="Callout text"></textarea>
                    }
                    @case ('code') {
                      <input [name]="'lang' + i" [(ngModel)]="b.language" placeholder="Language, e.g. sql" aria-label="Code language" />
                      <textarea [name]="'t' + i" rows="6" class="a-mono" [(ngModel)]="b.text" aria-label="Code"></textarea>
                    }
                    @case ('html') {
                      <textarea [name]="'h' + i" rows="5" class="a-mono" [(ngModel)]="b.html" aria-label="HTML"></textarea>
                      <p class="a-muted" style="margin:0;font-size:.75rem">Scripts, iframes, styles and event handlers are stripped on save.</p>
                    }
                    @case ('divider') { <hr style="border-color:var(--a-line);width:100%" /> }
                    @default { <textarea [name]="'t' + i" rows="4" [(ngModel)]="b.text" aria-label="Paragraph"></textarea> }
                  }
                </div>
              } @empty { <p class="a-muted">No content blocks yet. Add one below or import Markdown.</p> }
              <div class="a-row" style="flex-wrap:wrap;gap:6px">
                @for (t of blockTypes; track t.type) { <button class="ab sm" type="button" (click)="add(t.type)">+ {{ t.label }}</button> }
              </div>
              <div class="a-row" style="gap:8px;flex-wrap:wrap"><span class="a-muted" style="font-size:.8rem">Add an image or PDF:</span>
                <app-media-picker kind="inline" (picked)="insertMedia($event)" /></div>
              <p class="a-muted" style="margin:0;font-size:.75rem">Text supports **bold**, *italic*, \`code\` and [links](https://…).</p>
            </section>
          </div>

          <aside style="display:grid;gap:16px;align-content:start">
            <section class="a-card a-form">
              <h3 style="margin:0">Search and sharing</h3>
              <label>SEO title <span class="a-muted">({{ (d.seo_title || d.title).length }}/60)</span><input name="st" [(ngModel)]="d.seo_title" [placeholder]="d.title" /></label>
              <label>Meta description <span class="a-muted">({{ (d.seo_description || d.excerpt).length }}/160)</span><textarea name="sd" rows="3" [(ngModel)]="d.seo_description" [placeholder]="d.excerpt"></textarea></label>
              <label>Social image (Open Graph)<input name="og" [(ngModel)]="d.og_image" placeholder="https://… 1200×630" /></label>
              <app-media-picker kind="image" (picked)="d.og_image = $event.url" />
              <label>Canonical URL (only if published elsewhere first)<input name="can" [(ngModel)]="d.canonical" /></label>
              <label class="a-row"><input type="checkbox" name="ni" [(ngModel)]="d.noindex" /> Hide from search engines</label>
              <div class="a-section" aria-label="Search result preview">
                <div style="color:#8ab4f8;font-size:.95rem">{{ d.seo_title || d.title || 'Page title' }}</div>
                <div class="a-muted a-mono" style="font-size:.72rem">cadentic.dev{{ previewPath() }}</div>
                <div style="font-size:.8rem">{{ (d.seo_description || d.excerpt || 'Add a description so search engines and AI assistants can summarise this page.').slice(0, 160) }}</div>
              </div>
            </section>
            @if (d.kind === 'article') {
              <section class="a-card a-form">
                <h3 style="margin:0">Article details</h3>
                <label>Author<input name="au" [(ngModel)]="d.author_name" /></label>
                <label>Featured image (blog list and email)<input name="fi" [(ngModel)]="d.featured_image_url" /></label>
                <app-media-picker kind="image" (picked)="d.featured_image_url = $event.url" />
                <label>Categories (comma separated)<input name="cat" [(ngModel)]="d.categories" /></label>
                <label>Tags (comma separated)<input name="tag" [(ngModel)]="d.tags" /></label>
              </section>
            }
            @if (d.id && !legal()) { <button class="ab danger" type="button" (click)="remove()">Delete {{ d.kind }}</button> }
          </aside>
        </div>
      }

      @if (publishOpen()) {
        <div class="a-modal-bg" (click)="$event.target === $event.currentTarget ? publishOpen.set(false) : null">
          <div class="a-modal a-form" role="dialog" aria-modal="true" aria-labelledby="pub-t">
            <h3 id="pub-t" style="margin:0">Publish “{{ d.title }}”</h3>
            <p style="margin:0">It goes live at <span class="a-mono">{{ previewPath() }}</span> and is added to the sitemap and llms.txt.</p>
            @if (d.kind === 'article' && !d.first_published_at) {
              <label class="a-row"><input type="checkbox" [(ngModel)]="notify" /> Prepare a newsletter for verified subscribers</label>
              <p class="a-muted" style="margin:0;font-size:.8rem">This only creates a draft campaign. Nothing is emailed until you review and approve it under Subscribers &amp; Email → Campaigns.</p>
            }
            <div class="a-row"><button class="ab primary" type="button" [disabled]="busy()" (click)="publish()">Publish now</button><button class="ab" type="button" (click)="publishOpen.set(false)">Cancel</button></div>
          </div>
        </div>
      }
    } @else if (error()) { <p class="a-err">{{ error() }}</p> }
  `,
})
export class PageEditorComponent {
  private api = inject(AdminApi);
  private router = inject(Router);
  private route = inject(ActivatedRoute);
  readonly d = signal<Draft | null>(null);
  readonly error = signal('');
  readonly notice = signal('');
  readonly busy = signal(false);
  readonly mode = signal<'edit' | 'preview'>('edit');
  readonly mdOpen = signal(false);
  readonly publishOpen = signal(false);
  readonly blockTypes = BLOCKS;
  readonly heroTypes: Array<{ v: Hero['type']; label: string }> = [
    { v: 'none', label: 'No hero' }, { v: 'image', label: 'Image' }, { v: 'video', label: 'Video' }, { v: 'youtube', label: 'YouTube' }];
  readonly legal = computed(() => {
    const d = this.d();
    return !!d?.id && d.kind === 'page' && LEGAL_PATHS.includes(d.path);
  });
  readonly previewPath = computed(() => {
    const d = this.d();
    if (!d) return '';
    return d.path || (d.kind === 'article' ? '/blog/' : '/') + (d.slug || slugify(d.title));
  });
  markdown = '';
  notify = true;
  slugTouched = false;

  constructor() {
    const id = this.route.snapshot.paramMap.get('id')!;
    if (id === 'new') {
      const kind: Kind = this.route.snapshot.queryParamMap.get('kind') === 'page' ? 'page' : 'article';
      this.d.set({ kind, title: '', slug: '', excerpt: '', hero: { type: 'none' }, blocks: [{ type: 'paragraph', text: '' }],
        featured_image_url: '', author_name: '', categories: '', tags: '', seo_title: '', seo_description: '', og_image: '', canonical: '',
        noindex: false, status: 'draft', path: '', first_published_at: null });
    } else {
      this.slugTouched = true;
      this.api.get<Page>('pages/' + id).subscribe({ next: (p) => this.d.set(this.fromPage(p)), error: (e) => this.error.set(errMsg(e)) });
    }
  }

  private fromPage(p: Page): Draft {
    return { id: p.id, kind: p.kind, title: p.title, slug: p.slug, excerpt: p.excerpt, hero: { ...p.hero, type: p.hero?.type || 'none' },
      blocks: (p.blocks ?? []).map((b) => ({ ...b })), featured_image_url: p.featured_image_url, author_name: p.author_name,
      categories: (p.categories ?? []).join(', '), tags: (p.tags ?? []).join(', '), seo_title: p.seo?.title ?? '',
      seo_description: p.seo?.description ?? '', og_image: p.seo?.og_image ?? '', canonical: p.seo?.canonical ?? '',
      noindex: !!p.seo?.noindex, status: p.status, path: p.path, first_published_at: p.first_published_at };
  }
  private body(d: Draft) {
    const list = (s: string) => s.split(',').map((x) => x.trim()).filter(Boolean);
    return { kind: d.kind, title: d.title.trim(), slug: (d.slug || slugify(d.title)).trim(), excerpt: d.excerpt.trim(), hero: d.hero,
      blocks: d.blocks.filter((b) => b.type === 'divider' || (b.text ?? '').trim() || (b.items ?? []).length || (b.html ?? '').trim()),
      featured_image_url: d.featured_image_url.trim(), author_name: d.author_name.trim() || undefined, categories: list(d.categories), tags: list(d.tags),
      seo: { title: d.seo_title.trim(), description: d.seo_description.trim(), og_image: d.og_image.trim(), canonical: d.canonical.trim(), noindex: d.noindex } };
  }
  lines(v: string) {
    return v.split('\n').map((s) => s.trim()).filter(Boolean);
  }
  autoSlug(title: string) {
    const d = this.d();
    if (d && !d.id && !this.slugTouched) d.slug = slugify(title);
  }
  add(type: Block['type']) {
    const b: Block = { type };
    if (type === 'heading') b.level = 2;
    if (type === 'list') { b.style = 'bullet'; b.items = []; }
    if (type === 'callout') b.tone = 'info';
    if (type !== 'divider' && type !== 'html' && type !== 'list') b.text = '';
    this.d()?.blocks.push(b);
  }
  /** Appends an uploaded image (as a figure) or PDF (as a link) to the body. */
  insertMedia(m: MediaItem) {
    const d = this.d();
    if (!d) return;
    const esc = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
    const html = m.kind === 'image'
      ? `<figure><img src="${esc(m.url)}" alt="${esc(m.alt)}" loading="lazy"${m.width ? ` width="${m.width}" height="${m.height}"` : ''} />` +
        (m.caption ? `<figcaption>${esc(m.caption)}</figcaption>` : '') + '</figure>'
      : `<p><a href="${esc(m.url)}" target="_blank" rel="noopener">${esc(m.filename || 'Download PDF')}</a></p>`;
    d.blocks.push({ type: 'html', html });
    this.d.set({ ...d });
  }
  move(i: number, dir: number) {
    const bs = this.d()!.blocks;
    [bs[i], bs[i + dir]] = [bs[i + dir], bs[i]];
  }
  importMd(replace: boolean) {
    if (!this.markdown.trim()) return;
    this.api.post<{ blocks: Block[] }>('pages/markdown', { markdown: this.markdown }).subscribe({
      next: (r) => { const d = this.d()!; d.blocks = replace ? r.blocks : [...d.blocks, ...r.blocks]; this.markdown = ''; this.mdOpen.set(false); this.d.set({ ...d }); },
      error: (e) => this.error.set(errMsg(e)),
    });
  }
  save(then?: () => void) {
    const d = this.d();
    if (!d) return;
    if (!d.title.trim()) return this.error.set('Add a title first.');
    this.busy.set(true);
    this.error.set('');
    const req = d.id ? this.api.patch<Page>('pages/' + d.id, this.body(d)) : this.api.post<Page>('pages', this.body(d));
    req.subscribe({
      next: (p) => {
        this.busy.set(false);
        this.d.set(this.fromPage(p));
        this.slugTouched = true;
        if (!d.id) this.router.navigate(['/admin/content', p.id], { replaceUrl: true });
        if (then) then(); else this.notice.set(p.status === 'published' ? 'Changes saved. They are live now.' : 'Draft saved.');
      },
      error: (e) => { this.busy.set(false); this.error.set(errMsg(e)); },
    });
  }
  publish() {
    this.save(() => {
      const id = this.d()!.id!;
      this.busy.set(true);
      this.api.post<{ page: Page; campaign?: { id: string }; campaign_created?: boolean }>('pages/' + id + '/publish', { notify_subscribers: this.notify }).subscribe({
        next: (r) => {
          this.busy.set(false);
          this.publishOpen.set(false);
          this.d.set(this.fromPage(r.page));
          this.notice.set(r.campaign ? 'Published. A newsletter draft is ready for your review under Campaigns.' : 'Published.');
        },
        error: (e) => { this.busy.set(false); this.error.set(errMsg(e)); },
      });
    });
  }
  unpublish() {
    const d = this.d();
    if (!d?.id || !confirm('Unpublish? The URL will return 404 until you publish again.')) return;
    this.api.post<Page>('pages/' + d.id + '/unpublish').subscribe({
      next: (p) => { this.d.set(this.fromPage(p ?? { ...(d as unknown as Page), status: 'draft' })); this.notice.set('Unpublished.'); },
      error: (e) => this.error.set(errMsg(e)),
    });
  }
  remove() {
    const d = this.d();
    if (!d?.id || !confirm(`Delete “${d.title}” permanently?`)) return;
    this.api.delete('pages/' + d.id).subscribe({ next: () => this.router.navigate(['/admin/content']), error: (e) => this.error.set(errMsg(e)) });
  }
}
