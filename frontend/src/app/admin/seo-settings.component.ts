import { Component, computed, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { DEFAULT_BRAND_SUFFIX, withBrand } from '../core/brand';
import { AdminApi, errMsg } from './admin-api.service';

interface SiteSettings { brand_suffix: string }

@Component({
  selector: 'app-admin-seo-settings',
  imports: [FormsModule],
  template: `
    @if (error()) { <p class="a-err">{{ error() }}</p> }
    @if (notice()) { <p class="a-ok">{{ notice() }}</p> }
    <form class="a-card a-form" (ngSubmit)="save()">
      <h2 style="margin:0">SEO &amp; branding</h2>
      <label>Brand suffix
        <input name="bs" maxlength="80" [ngModel]="suffix()" (ngModelChange)="suffix.set($event)" [placeholder]="defaultSuffix" />
      </label>
      <p class="a-muted" style="margin:0;font-size:.8rem">
        Added to the end of every page title, meta description, Open Graph and Twitter tag, and image alt text on the public site.
        Text that already ends with it is left alone, so page and media text never repeat it.
        Leave it empty to turn it off (titles then end with just your name).
      </p>
      <div class="a-muted" style="font-size:.85rem">
        <div><strong>Title:</strong> {{ preview().title }}</div>
        <div><strong>Image alt:</strong> {{ preview().alt }}</div>
      </div>
      <div class="a-row">
        <button class="ab primary" type="submit" [disabled]="busy()">Save</button>
        <button class="ab" type="button" (click)="suffix.set(defaultSuffix)">Reset to default</button>
      </div>
    </form>
  `,
})
export class SeoSettingsComponent {
  private api = inject(AdminApi);
  readonly defaultSuffix = DEFAULT_BRAND_SUFFIX;
  readonly suffix = signal(DEFAULT_BRAND_SUFFIX);
  readonly error = signal('');
  readonly notice = signal('');
  readonly busy = signal(false);
  readonly preview = computed(() => ({
    title: withBrand('Blog: Oracle DBA notes & insights', this.suffix().trim()) || '—',
    alt: withBrand('Data Guard switchover diagram', this.suffix().trim()),
  }));

  constructor() {
    this.api.get<SiteSettings>('settings/site').subscribe({
      next: (s) => this.suffix.set(s.brand_suffix ?? ''),
      error: (e) => this.error.set(errMsg(e)),
    });
  }

  save() {
    this.busy.set(true);
    this.api.put<SiteSettings>('settings/site', { brand_suffix: this.suffix().trim() }).subscribe({
      next: (r) => {
        this.suffix.set(r?.brand_suffix ?? '');
        this.busy.set(false);
        this.error.set('');
        this.notice.set('Saved. The public site picks this up within a minute.');
      },
      error: (e) => { this.busy.set(false); this.notice.set(''); this.error.set(errMsg(e)); },
    });
  }
}
