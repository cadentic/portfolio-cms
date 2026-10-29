import { Injectable, signal } from '@angular/core';
import { FieldError, SiteContentAdminDoc, remapRows } from './site-content.model';

/**
 * The site content being edited. Home page, Site profile and Videos edit one
 * document, so the draft (and the server's errors for it) outlives a switch
 * between those screens instead of being lost.
 */
@Injectable({ providedIn: 'root' })
export class SiteDraft {
  readonly doc = signal<SiteContentAdminDoc | null>(null);
  readonly dirty = signal(false);
  readonly errors = signal<FieldError[]>([]);

  // Methods, not computed(): the content is edited in place.
  years(): number | null {
    return this.doc()?.content.identity.experience_years ?? null;
  }

  errorsAt(path: string, withItems = false): FieldError[] {
    return this.errors().filter(
      (e) => e.path === path || (withItems && e.path.startsWith(path + '[')),
    );
  }

  /** Rows of the list at `path` were removed or moved (`from[k]`: old index of row k). */
  moveRows(path: string, from: number[]) {
    this.errors.update((es) => remapRows(es, path, from));
    const d = this.doc();
    if (d?.warnings.some((w) => w.path.startsWith(path + '['))) {
      this.doc.set({ ...d, warnings: remapRows(d.warnings, path, from) });
    }
  }

  discard() {
    this.doc.set(null);
    this.dirty.set(false);
    this.errors.set([]);
  }
}
