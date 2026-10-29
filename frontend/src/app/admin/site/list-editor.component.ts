import { NgTemplateOutlet } from '@angular/common';
import {
  ChangeDetectionStrategy,
  Component,
  ElementRef,
  Injector,
  TemplateRef,
  afterNextRender,
  computed,
  contentChild,
  inject,
  input,
  output,
} from '@angular/core';
import { SiteDraft } from './site-draft.service';

const FIRST_FIELD = 'input:not([type="file"]), textarea';

/**
 * Rows of a list with add, remove and move. The rows are edited in place, so
 * the caller passes the document's own array. Row fields come from the
 * projected template: <ng-template let-row let-i="index">…</ng-template>.
 */
@Component({
  selector: 'app-list-editor',
  // Eager: the rows are the draft's own array, edited in place.
  changeDetection: ChangeDetectionStrategy.Eager,
  imports: [NgTemplateOutlet],
  template: `
    <div class="a-list-rows">
      @for (row of items(); track $index; let i = $index; let last = $last) {
        <fieldset class="a-block">
          <legend>{{ title() }} {{ i + 1 }}</legend>
          <div class="a-block-head" style="justify-content:flex-end">
            <span class="a-row" style="gap:4px">
              <button
                class="ab sm"
                type="button"
                data-move="up"
                [disabled]="i === 0"
                (click)="move(i, -1)"
                [attr.aria-label]="'Move ' + itemName() + ' ' + (i + 1) + ' up'"
              >
                ↑
              </button>
              <button
                class="ab sm"
                type="button"
                data-move="down"
                [disabled]="last"
                (click)="move(i, 1)"
                [attr.aria-label]="'Move ' + itemName() + ' ' + (i + 1) + ' down'"
              >
                ↓
              </button>
              <button
                class="ab sm danger"
                type="button"
                (click)="remove(i)"
                [attr.aria-label]="'Remove ' + itemName() + ' ' + (i + 1)"
              >
                Remove
              </button>
            </span>
          </div>
          <ng-container *ngTemplateOutlet="rowTemplate(); context: { $implicit: row, index: i }" />
        </fieldset>
      } @empty {
        <p class="a-muted">None yet.</p>
      }
    </div>
    <div class="a-row">
      <button class="ab sm" type="button" [disabled]="full()" (click)="add()">
        + Add {{ itemName() }}
      </button>
      @if (full()) {
        <span class="a-hint">The most allowed is {{ max() }}. Remove one to add another.</span>
      }
    </div>
  `,
})
export class ListEditorComponent<T = unknown> {
  private host = inject<ElementRef<HTMLElement>>(ElementRef);
  private injector = inject(Injector);
  private draft = inject(SiteDraft);
  readonly items = input.required<T[]>();
  /** The list's document path, e.g. "hero.metrics": server messages follow their row. */
  readonly path = input('');
  readonly max = input(Infinity);
  /** Lower case, e.g. "metric": "+ Add metric", "Move metric 2 up". */
  readonly itemName = input.required<string>();
  readonly newItem = input.required<() => T>();
  readonly changed = output<void>();
  readonly rowTemplate = contentChild.required(TemplateRef);
  readonly title = computed(() => {
    const n = this.itemName();
    return n.charAt(0).toUpperCase() + n.slice(1);
  });
  // A method, not computed(): the array is mutated in place, so its identity never changes.
  full() {
    return this.items().length >= this.max();
  }

  add() {
    if (this.full()) return;
    this.items().push(this.newItem()());
    this.changed.emit();
    // Put the cursor in the new row's first field.
    this.focusLater(this.items().length - 1, FIRST_FIELD);
  }
  remove(i: number) {
    const xs = this.items();
    this.moved([...xs.keys()].filter((k) => k !== i));
    xs.splice(i, 1);
    this.changed.emit();
    // Into the row that took its place, not onto that row's Remove button (Enter would remove it
    // too); the Add button when none are left.
    if (xs.length) this.focusLater(Math.min(i, xs.length - 1), FIRST_FIELD);
    else this.focusLater(null, ':scope > .a-row > button');
  }
  move(i: number, dir: number) {
    const xs = this.items();
    const j = i + dir;
    if (j < 0 || j >= xs.length) return;
    const from = [...xs.keys()];
    [from[i], from[j]] = [j, i];
    this.moved(from);
    [xs[i], xs[j]] = [xs[j], xs[i]];
    this.changed.emit();
    // Keep focus on the moved row, so pressing ↑ again moves it further.
    const edge = j === 0 || j === xs.length - 1;
    this.focusLater(
      j,
      edge
        ? `[data-move="${dir < 0 ? 'down' : 'up'}"]`
        : `[data-move="${dir < 0 ? 'up' : 'down'}"]`,
    );
  }

  private moved(from: number[]) {
    if (this.path()) this.draft.moveRows(this.path(), from);
  }

  /** Focuses `selector` in row `row`, or in the whole list editor when `row` is null. */
  private focusLater(row: number | null, selector: string) {
    afterNextRender(
      () => {
        const host = this.host.nativeElement;
        const rows = host.querySelectorAll(':scope > .a-list-rows > fieldset');
        const scope = row === null ? host : rows[row];
        scope?.querySelector<HTMLElement>(selector)?.focus();
      },
      { injector: this.injector },
    );
  }
}
