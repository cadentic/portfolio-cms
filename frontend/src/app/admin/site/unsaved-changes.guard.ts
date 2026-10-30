import { CanDeactivateFn } from '@angular/router';

export interface UnsavedChanges {
  dirty(): boolean;
  /** True when the unsaved draft survives navigating to `url`, so leaving loses nothing. */
  keepsDraftFor?(url: string): boolean;
  /** Called once the owner has agreed to leave without saving. */
  discard?(): void;
}

export const unsavedChangesGuard: CanDeactivateFn<UnsavedChanges> = (c, _route, _state, next) => {
  if (!c.dirty() || c.keepsDraftFor?.(next.url)) return true;
  if (!confirm('You have unsaved changes. Leave without saving?')) return false;
  c.discard?.();
  return true;
};
