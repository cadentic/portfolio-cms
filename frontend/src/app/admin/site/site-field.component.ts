import { ChangeDetectionStrategy, Component, computed, inject, input } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { expandPreview, lim, setValueAt, valueAt } from './site-content.model';
import { SiteDraft } from './site-draft.service';
import { labelFor } from './site-labels';

export type FieldKind = 'text' | 'textarea' | 'lines' | 'number' | 'url' | 'date';

/** A one-per-line field's items: trimmed, empty lines dropped. */
const splitLines = (v: string) =>
  v
    .split('\n')
    .map((s) => s.trim())
    .filter(Boolean);

/** DOM id of the input for a document path: "hero.metrics[0].value" → "f-hero-metrics-0-value". */
export function fieldId(path: string): string {
  return 'f-' + path.replace(/[^A-Za-z0-9_-]+/g, '-').replace(/-$/, '');
}

/**
 * One field of the site content draft, addressed by its path. Limits come from
 * the server (maxlength, min/max, the "Required" marker); there is no HTML
 * `required`, because the server decides conditional rules (D23).
 * Eager: the draft is edited in place (another field, a reset, Replace all), so
 * every check of the editor re-reads it.
 */
@Component({
  selector: 'app-site-field',
  changeDetection: ChangeDetectionStrategy.Eager,
  imports: [FormsModule],
  host: { class: 'a-field' },
  template: `
    <label>
      <span class="a-flabel" [class.req]="limit()?.required">{{
        label() || labelFor(path())
      }}</span>
      @switch (kind()) {
        @case ('textarea') {
          <textarea
            [id]="id()"
            [attr.name]="path()"
            [rows]="rows()"
            [attr.maxlength]="limit()?.max || null"
            [attr.placeholder]="placeholder() || null"
            [attr.aria-describedby]="describedBy()"
            [attr.aria-required]="limit()?.required || null"
            [attr.aria-invalid]="errs().length ? 'true' : null"
            [ngModel]="value()"
            (ngModelChange)="set($event)"
          ></textarea>
        }
        @case ('lines') {
          <textarea
            [id]="id()"
            [attr.name]="path()"
            [rows]="rows()"
            [attr.placeholder]="placeholder() || null"
            [attr.aria-describedby]="describedBy()"
            [attr.aria-invalid]="errs().length ? 'true' : null"
            [ngModel]="linesText()"
            (ngModelChange)="setLines($event)"
          ></textarea>
        }
        @case ('number') {
          <input
            type="number"
            step="1"
            [id]="id()"
            [attr.name]="path()"
            [attr.min]="limit()?.min ?? null"
            [attr.max]="limit()?.max ?? null"
            [attr.aria-describedby]="describedBy()"
            [attr.aria-required]="limit()?.required || null"
            [attr.aria-invalid]="errs().length ? 'true' : null"
            [ngModel]="value()"
            (ngModelChange)="set($event)"
          />
        }
        @case ('url') {
          <input
            type="url"
            [id]="id()"
            [attr.name]="path()"
            [attr.maxlength]="limit()?.max || null"
            [attr.placeholder]="placeholder() || 'https://…'"
            [attr.aria-describedby]="describedBy()"
            [attr.aria-required]="limit()?.required || null"
            [attr.aria-invalid]="errs().length ? 'true' : null"
            [ngModel]="value()"
            (ngModelChange)="set($event)"
          />
        }
        @case ('date') {
          <input
            type="date"
            [id]="id()"
            [attr.name]="path()"
            [attr.aria-describedby]="describedBy()"
            [attr.aria-invalid]="errs().length ? 'true' : null"
            [ngModel]="value()"
            (ngModelChange)="set($event)"
          />
        }
        @default {
          <input
            type="text"
            [id]="id()"
            [attr.name]="path()"
            [attr.maxlength]="limit()?.max || null"
            [attr.placeholder]="placeholder() || null"
            [attr.list]="list() || null"
            [attr.aria-describedby]="describedBy()"
            [attr.aria-required]="limit()?.required || null"
            [attr.aria-invalid]="errs().length ? 'true' : null"
            [ngModel]="value()"
            (ngModelChange)="set($event)"
          />
        }
      }
    </label>
    @if (hintText(); as h) {
      <p class="a-hint" [id]="id() + '-hint'">{{ h }}</p>
    }
    @for (w of lineWarnings(); track $index) {
      <p class="a-hint warn">{{ w }}</p>
    }
    @for (p of previews(); track $index) {
      <p class="a-shows">Shows as: {{ p }}</p>
    }
    @for (e of errs(); track $index) {
      <p class="a-ferr" [id]="id() + '-err' + $index">{{ e.message }}</p>
    }
  `,
})
export class SiteFieldComponent {
  private draft = inject(SiteDraft);
  /** Document path, e.g. "hero.title" or "hero.metrics[0].value". */
  readonly path = input.required<string>();
  /** Defaults to the shared label (site-labels.ts), which messages also use. */
  readonly label = input('');
  readonly hint = input('');
  readonly kind = input<FieldKind>('text');
  readonly rows = input(3);
  readonly placeholder = input('');
  /** id of a <datalist> with suggestions. */
  readonly list = input('');
  readonly labelFor = labelFor;
  readonly id = computed(() => fieldId(this.path()));
  readonly limit = computed(() => lim(this.draft.doc()?.limits, this.path()));
  readonly errs = computed(() => this.draft.errorsAt(this.path(), this.kind() === 'lines'));

  // Values are read on every check, not memoised: the draft is edited in place.
  value(): unknown {
    const c = this.draft.doc()?.content;
    return c ? valueAt(c, this.path()) : undefined;
  }
  set(v: unknown) {
    const c = this.draft.doc()?.content;
    if (c) setValueAt(c, this.path(), v);
  }
  private lines(): string[] {
    const v = this.value();
    return Array.isArray(v) ? (v as string[]) : [];
  }
  /** The text as last typed. */
  private typed = '';
  /**
   * The typed text while it still gives the draft's value, so an emptied line stays where the
   * owner is typing instead of being removed under the caret. A load, reset or Replace all
   * changes the value, and then the list is shown one item per line.
   */
  linesText() {
    const joined = this.lines().join('\n');
    return splitLines(this.typed).join('\n') === joined ? this.typed : joined;
  }
  setLines(v: string) {
    this.typed = v;
    this.set(splitLines(v));
  }

  hintText(): string {
    const l = this.limit();
    const parts = [this.hint()];
    if (this.kind() === 'lines') {
      const each = l?.item ? `, up to ${l.item} characters each` : '';
      parts.push(l?.max ? `One per line: up to ${l.max} lines${each}.` : 'One per line.');
    } else if (l?.max && this.kind() !== 'number' && this.kind() !== 'date') {
      // Show the count only near the limit, so short fields stay quiet.
      const n = String(this.value() ?? '').length;
      if (n >= l.max * 0.8) parts.push(`${n} of ${l.max} characters.`);
    }
    return parts.filter(Boolean).join(' ');
  }
  lineWarnings(): string[] {
    const max = this.kind() === 'lines' ? this.limit()?.item : 0;
    if (!max) return [];
    return this.lines().flatMap((s, i) =>
      s.length > max ? [`Line ${i + 1} is ${s.length} characters; the most is ${max}.`] : [],
    );
  }
  previews(): string[] {
    const years = this.draft.years();
    const v = this.value();
    const texts = typeof v === 'string' ? [v] : Array.isArray(v) ? (v as string[]) : [];
    return texts.filter((t) => t.includes('{years}')).map((t) => expandPreview(t, years));
  }
  describedBy(): string | null {
    const ids = this.hintText() ? [this.id() + '-hint'] : [];
    this.errs().forEach((_, i) => ids.push(this.id() + '-err' + i));
    return ids.join(' ') || null;
  }
}

/** Server errors for a path that has no single input, such as a whole list. */
@Component({
  selector: 'app-site-errors',
  template: `
    @for (e of errs(); track $index) {
      <p class="a-ferr" [id]="$first && anchor() ? id() : null" tabindex="-1">{{ e.message }}</p>
    }
  `,
})
export class SiteErrorsComponent {
  private draft = inject(SiteDraft);
  readonly path = input.required<string>();
  /** False when an input already carries the path's id (the photo and logo pickers). */
  readonly anchor = input(true);
  readonly id = computed(() => fieldId(this.path()));
  readonly errs = computed(() => this.draft.errorsAt(this.path()));
}
