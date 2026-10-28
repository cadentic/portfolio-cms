import { DOCUMENT, isPlatformBrowser } from '@angular/common';
import { Injectable, PLATFORM_ID, inject, signal } from '@angular/core';

type Choice = 'granted' | 'denied';
const KEY = 'consent.v1';

/**
 * Analytics consent (DPDP Act 2023 / GDPR). Google Consent Mode v2 starts
 * with everything denied; GTM/GA only load after the visitor opts in.
 */
@Injectable({ providedIn: 'root' })
export class ConsentService {
  private doc = inject(DOCUMENT);
  private browser = isPlatformBrowser(inject(PLATFORM_ID));
  readonly choice = signal<Choice | null>(null);
  private gtmId = '';
  private ga4Id = '';

  init(gtmId: string, ga4Id: string) {
    if (!this.browser) return;
    this.gtmId = gtmId;
    this.ga4Id = ga4Id;
    let stored: string | null = null;
    try {
      stored = localStorage.getItem(KEY);
    } catch {
      /* storage blocked */
    }
    this.choice.set(stored === 'granted' || stored === 'denied' ? stored : null);
    if (this.choice() === 'granted') this.load();
  }

  get needed(): boolean {
    return !!(this.gtmId || this.ga4Id);
  }

  set(choice: Choice) {
    this.choice.set(choice);
    try {
      localStorage.setItem(KEY, choice);
    } catch {
      /* ignore */
    }
    if (choice === 'granted') this.load();
    else this.update('denied');
  }

  reset() {
    try {
      localStorage.removeItem(KEY);
    } catch {
      /* ignore */
    }
    this.choice.set(null);
    this.update('denied');
  }

  private gtag(...args: unknown[]) {
    const w = this.doc.defaultView as unknown as { dataLayer: unknown[] };
    w.dataLayer = w.dataLayer || [];
    // gtag() must push the arguments object, as Google's snippet does.
    (function (this: unknown) {
      // eslint-disable-next-line prefer-rest-params
      w.dataLayer.push(arguments);
    }).apply(null, args as []);
  }

  private update(state: Choice) {
    if (!this.browser) return;
    this.gtag('consent', 'update', { analytics_storage: state, ad_storage: 'denied', ad_user_data: 'denied', ad_personalization: 'denied' });
  }

  private loaded = false;
  private load() {
    if (this.loaded || !this.needed) return;
    this.loaded = true;
    this.gtag('consent', 'default', { analytics_storage: 'denied', ad_storage: 'denied', ad_user_data: 'denied',
      ad_personalization: 'denied', wait_for_update: 500 });
    this.update('granted');
    const s = this.doc.createElement('script');
    s.async = true;
    if (this.gtmId) {
      (this.doc.defaultView as unknown as { dataLayer: unknown[] }).dataLayer.push({ 'gtm.start': Date.now(), event: 'gtm.js' });
      s.src = 'https://www.googletagmanager.com/gtm.js?id=' + encodeURIComponent(this.gtmId);
    } else {
      s.src = 'https://www.googletagmanager.com/gtag/js?id=' + encodeURIComponent(this.ga4Id);
      this.gtag('js', new Date());
      this.gtag('config', this.ga4Id, { anonymize_ip: true });
    }
    this.doc.head.appendChild(s);
  }
}
