import { DOCUMENT, DatePipe, formatDate } from '@angular/common';
import {
  Component,
  ElementRef,
  Injector,
  LOCALE_ID,
  OnDestroy,
  OnInit,
  afterNextRender,
  inject,
  input,
  output,
  signal,
  viewChild,
} from '@angular/core';
import { Observable } from 'rxjs';
import { Block, Page } from '../../core/models';
import { SectionKey } from '../../core/site-content';
import { BlocksComponent } from '../../shared/blocks.component';
import { AdminApi, errMsg } from '../admin-api.service';
import { AuthService } from '../auth.service';
import {
  ContentVersion,
  GROUPS,
  PageVersionDetail,
  SiteContentAdminDoc,
  SiteContentReset,
  VersionRestore,
} from './site-content.model';

// Sections that several editor groups share get one name of their own.
const SHARED_SECTIONS: Partial<Record<SectionKey, string>> = {
  identity: 'Profile, links & education',
  videos: 'Videos',
  blog: 'Notes & insights and blog page',
  seo: 'Search engines & llms.txt',
};
const PAGE_COLUMNS: Record<string, string> = {
  title: 'Title',
  excerpt: 'Summary',
  hero: 'Hero',
  blocks: 'Text',
  seo: 'Search settings',
  slug: 'Address',
};
const ACTIONS: Record<ContentVersion['action'], string> = {
  seed: 'Original text',
  update: 'Saved',
  reset: 'Reset to defaults',
  restore: 'Restored an older version',
};

/**
 * Saved versions of the site content, or of one legal page (`pageId`), with
 * restore. A restore is itself a new version, so it can be undone here too.
 */
@Component({
  selector: 'app-history-dialog',
  imports: [DatePipe, BlocksComponent],
  template: `
    <div class="a-modal-bg" (click)="$event.target === $event.currentTarget ? closed.emit() : null">
      <div
        #dialog
        class="a-modal wide a-form"
        role="dialog"
        aria-modal="true"
        aria-labelledby="hist-t"
        tabindex="-1"
        (keydown.escape)="closed.emit()"
      >
        <div class="a-row between">
          <h2 id="hist-t" style="margin:0">History{{ title() ? ': ' + title() : '' }}</h2>
          <button class="ab sm" type="button" (click)="closed.emit()" aria-label="Close">✕</button>
        </div>
        @if (error()) {
          <p class="a-err" role="alert" style="margin:0">{{ error() }}</p>
        }
        @if (viewing(); as v) {
          <div class="a-row between">
            <span class="a-muted">Version from {{ v.created_at | date: 'd MMM y, HH:mm' }}</span>
            <span class="a-row" style="gap:6px">
              <button class="ab sm" type="button" (click)="viewing.set(null)">
                ← Back to the list
              </button>
              <button
                class="ab sm primary"
                type="button"
                [disabled]="busy()"
                (click)="restorePage(v)"
              >
                Restore this version
              </button>
            </span>
          </div>
          <section class="a-card" style="background:#fff;color:#111923">
            <article class="prose" style="max-width:760px;margin:0 auto">
              <h1>{{ v.snapshot.title }}</h1>
              <app-blocks [blocks]="blocksOf(v)" />
            </article>
          </section>
        } @else {
          <p style="margin:0">
            @if (pageId()) {
              Every saved change to this page is kept. Restoring an older version replaces the live
              text; you can undo it here.
            } @else {
              Every save is kept here (the newest 200). Restoring a version replaces the site
              content; you can undo it here.
            }
          </p>
          @if (dirty()) {
            <div class="a-warn a-row between" style="margin:0">
              <span>
                Save or discard your changes first: restoring is turned off while you have unsaved
                changes.
              </span>
              <button class="ab sm" type="button" (click)="discardRequested.emit()">
                Discard changes
              </button>
            </div>
          }
          <div class="a-table-wrap">
            <table class="a-table">
              <thead>
                <tr>
                  <th>When</th>
                  <th>Who</th>
                  <th>What changed</th>
                  <th><span class="sr-only">Actions</span></th>
                </tr>
              </thead>
              <tbody>
                @for (v of versions(); track v.id; let first = $first) {
                  <tr>
                    <td style="white-space:nowrap">
                      {{ v.created_at | date: 'd MMM y, HH:mm' }}
                      @if (first) {
                        <br /><span class="pill ok">Current</span>
                      }
                    </td>
                    <td>{{ who(v.actor) }}</td>
                    <td>
                      {{ ACTIONS[v.action] }}{{ v.changed.length ? ': ' + changedText(v) : '' }}
                    </td>
                    <td>
                      <span class="a-row" style="gap:4px;flex-wrap:nowrap">
                        @if (pageId()) {
                          <button class="ab sm" type="button" (click)="view(v)">View</button>
                        }
                        <button
                          class="ab sm"
                          type="button"
                          [disabled]="busy() || dirty()"
                          [attr.title]="dirty() ? 'Save or discard your changes first' : null"
                          (click)="restore(v)"
                        >
                          Restore this version
                        </button>
                      </span>
                    </td>
                  </tr>
                } @empty {
                  <tr>
                    <td colspan="4" class="a-muted">
                      {{ versions() ? 'No saved versions yet.' : 'Loading…' }}
                    </td>
                  </tr>
                }
                @if (!pageId()) {
                  <tr>
                    <td colspan="3">
                      Original site text (defaults)<br />
                      <span class="a-muted"
                        >The text the site started with. Images you uploaded are not deleted.</span
                      >
                    </td>
                    <td>
                      <button
                        class="ab sm"
                        type="button"
                        [disabled]="busy() || dirty()"
                        [attr.title]="dirty() ? 'Save or discard your changes first' : null"
                        (click)="resetAll()"
                      >
                        Restore this version
                      </button>
                    </td>
                  </tr>
                }
              </tbody>
            </table>
          </div>
        }
      </div>
    </div>
  `,
})
export class HistoryDialogComponent implements OnInit, OnDestroy {
  private api = inject(AdminApi);
  private doc = inject(DOCUMENT);
  private auth = inject(AuthService);
  private locale = inject(LOCALE_ID);
  private injector = inject(Injector);
  /** A legal page's id; empty for the site content. */
  readonly pageId = input('');
  readonly title = input('');
  /** Unsaved changes in the editor: restoring would silently drop them. */
  readonly dirty = input(false);
  readonly baseUpdatedAt = input<string | null>(null);
  /** The owner asked to drop the unsaved changes; the editor confirms and reloads. */
  readonly discardRequested = output<void>();
  readonly siteRestored = output<{ doc: SiteContentAdminDoc; label: string }>();
  readonly pageRestored = output<Page>();
  readonly closed = output<void>();
  readonly versions = signal<ContentVersion[] | null>(null);
  readonly viewing = signal<PageVersionDetail | null>(null);
  readonly error = signal('');
  readonly busy = signal(false);
  readonly ACTIONS = ACTIONS;
  private dialog = viewChild.required<ElementRef<HTMLElement>>('dialog');
  /** Gets focus back on close, usually the History button. */
  private opener: Element | null = null;

  ngOnInit() {
    this.opener = this.doc.activeElement;
    afterNextRender(() => this.dialog().nativeElement.focus(), { injector: this.injector });
    this.api.get<{ items: ContentVersion[] }>(this.base(), { limit: 200 }).subscribe({
      next: (r) => this.versions.set(r.items ?? []),
      error: (e) => {
        this.versions.set([]);
        this.error.set(errMsg(e));
      },
    });
  }
  ngOnDestroy() {
    (this.opener as HTMLElement | null)?.focus?.();
  }
  private base() {
    return this.pageId() ? `pages/${this.pageId()}/versions` : 'site-content/versions';
  }

  who(actor: string) {
    if (actor === this.auth.me()?.email) return 'You';
    if (actor.startsWith('mcp:')) return 'AI assistant';
    if (actor === 'migration') return 'Site update';
    return actor;
  }
  changedText(v: ContentVersion) {
    const name = (k: string) =>
      this.pageId()
        ? (PAGE_COLUMNS[k] ?? k)
        : (SHARED_SECTIONS[k as SectionKey] ??
          GROUPS.find((g) => g.fields.includes(k))?.title ??
          k);
    return v.changed.map(name).join(', ');
  }
  blocksOf(v: PageVersionDetail) {
    return (v.snapshot.blocks ?? []) as Block[];
  }
  private when(v: ContentVersion) {
    return formatDate(v.created_at, 'd MMM y, HH:mm', this.locale);
  }

  view(v: ContentVersion) {
    this.error.set('');
    this.api.get<PageVersionDetail>(`${this.base()}/${v.id}`).subscribe({
      next: (d) => this.viewing.set(d),
      error: (e) => this.error.set(errMsg(e)),
    });
  }
  restore(v: ContentVersion) {
    if (this.pageId()) return this.restorePage(v);
    const label = `the version from ${this.when(v)}`;
    if (!confirm(`Replace the current site content with ${label}? You can undo this from History.`))
      return;
    const body: VersionRestore = { base_updated_at: this.baseUpdatedAt() };
    this.run(this.api.post<SiteContentAdminDoc>(`${this.base()}/${v.id}/restore`, body), (doc) =>
      this.siteRestored.emit({ doc, label }),
    );
  }
  resetAll() {
    if (
      !confirm(
        'Replace all site content with the original site text? You can undo this from History.',
      )
    )
      return;
    const body: SiteContentReset = { all: true, base_updated_at: this.baseUpdatedAt() };
    this.run(this.api.post<SiteContentAdminDoc>('site-content/reset', body), (doc) =>
      this.siteRestored.emit({ doc, label: 'the original site text' }),
    );
  }
  restorePage(v: ContentVersion) {
    if (
      !confirm(
        `Replace the live text of “${this.title()}” with the version from ${this.when(v)}? You can undo this from History.`,
      )
    )
      return;
    this.run(this.api.post<Page>(`${this.base()}/${v.id}/restore`), (p) =>
      this.pageRestored.emit(p),
    );
  }
  private run<T>(req: Observable<T>, done: (r: T) => void) {
    this.busy.set(true);
    this.error.set('');
    req.subscribe({
      next: (r) => {
        this.busy.set(false);
        done(r);
      },
      error: (e) => {
        this.busy.set(false);
        this.error.set(errMsg(e));
      },
    });
  }
}
