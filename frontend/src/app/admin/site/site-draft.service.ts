import { Injectable, signal } from '@angular/core';
import { FieldError, SiteContentAdminDoc } from './site-content.model';

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

  discard() {
    this.doc.set(null);
    this.dirty.set(false);
    this.errors.set([]);
  }
}
