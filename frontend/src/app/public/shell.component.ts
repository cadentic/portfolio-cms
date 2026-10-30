import { DOCUMENT, isPlatformBrowser } from '@angular/common';
import { Component, PLATFORM_ID, computed, inject, signal } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { NavigationEnd, Router, RouterLink, RouterLinkActive, RouterOutlet } from '@angular/router';
import { catchError, filter, map, of } from 'rxjs';
import { ConsentService } from '../core/consent.service';
import { PublicApi } from '../core/public-api.service';
import { NavCard, profileLinks } from '../core/site-content';
import { SiteContentService } from '../core/site-content.service';
import { ResumeModalComponent } from '../shared/resume-modal.component';
import { ScrollRevealDirective } from '../shared/scroll-reveal.directive';
import { Toast } from '../shared/toast.service';
import { UiState } from '../shared/ui-state.service';

@Component({
  selector: 'app-shell',
  imports: [RouterOutlet, RouterLink, RouterLinkActive, ResumeModalComponent, ScrollRevealDirective],
  host: { '(document:keydown.escape)': 'closeMenus()', '(document:click)': 'outside($event)' },
  template: `
    @let c = site.content();
    @let id = c.identity;
    <a class="skip-link" href="#main">Skip to content</a>
    @if (site.degraded()) {
      <div class="site-banner" role="status">Some content could not be loaded. Refresh to try again.</div>
    }
    <header class="site-header">
      <div class="wrap nav">
        <a class="brand" routerLink="/" aria-label="{{ id.name }}, home">
          <span class="brand-mark" aria-hidden="true">{{ id.initials }}</span>
          <span>{{ id.name }}@if (c.navigation.brand_subtitle) {<small>{{ c.navigation.brand_subtitle }}</small>}</span>
        </a>
        <button class="mobile-toggle" type="button" (click)="mobile.set(!mobile())" [attr.aria-expanded]="mobile()" aria-controls="navlinks">Menu ☰</button>
        <nav class="navlinks" id="navlinks" [class.mobile-open]="mobile()" aria-label="Main navigation">
          @if (c.about.visible) { <a routerLink="/" fragment="about" (click)="closeMenus()">{{ c.about.label }}</a> }
          @if (c.experience.visible) { <a routerLink="/" fragment="experience" (click)="closeMenus()">{{ c.experience.label }}</a> }
          @if (megaCards().length) {
            <div class="menu-wrap">
              <button class="nav-trigger" type="button" (click)="mega.set(!mega())" [attr.aria-expanded]="mega()" aria-controls="mega">Explore ▾</button>
              <div class="mega" id="mega" [class.open]="mega()">
                @for (m of megaCards(); track $index) {
                  <a class="mega-card" [routerLink]="m.link" [fragment]="m.fragment" (click)="closeMenus()"><span aria-hidden="true">{{ m.icon }}</span><strong>{{ m.card.title }}</strong><span>{{ m.card.text }}</span></a>
                }
              </div>
            </div>
          }
          @if (blogEnabled()) { <a routerLink="/blog" routerLinkActive="active" (click)="closeMenus()">Blog</a> }
          <a routerLink="/" fragment="contact" (click)="closeMenus()">{{ c.contact.label || 'Contact' }}</a>
          @if (c.navigation.resume_label && resumeAvailable()) {
            <button class="btn btn-primary" type="button" (click)="ui.resumeOpen.set(true); closeMenus()">{{ c.navigation.resume_label }} ↗</button>
          }
        </nav>
      </div>
    </header>

    <main id="main" tabindex="-1" appScrollReveal><router-outlet /></main>

    <footer class="site-footer">
      <div class="wrap">
        <div class="footer-row">
          <span>© {{ year }} {{ id.name }}@if (id.location) { · {{ id.location }} }</span>
          <div class="footer-links">
            <a href="#main">Back to top ↑</a>
            <a routerLink="/privacy">Privacy</a>
            <a routerLink="/terms">Terms</a>
            <a routerLink="/ai-policy">AI policy</a>
            @if (consent.needed) { <button class="nav-trigger" type="button" style="color:inherit" (click)="consent.reset()">Cookie settings</button> }
          </div>
        </div>
        <!-- Sitemap stays in the server-rendered HTML for crawlers; visitors expand it on demand. -->
        <details class="sitemap">
          <summary>Site map</summary>
          <nav class="sitemap-grid" aria-label="Site map">
            <div><h3>Portfolio</h3><ul>
              <li><a routerLink="/">Home</a></li>
              @if (c.about.visible) { <li><a routerLink="/" fragment="about">{{ c.about.label }}</a></li> }
              @if (c.experience.visible) { <li><a routerLink="/" fragment="experience">{{ c.experience.label }}</a></li> }
              @if (c.work.visible) { <li><a routerLink="/" fragment="work">{{ c.work.label }}</a></li> }
              @if (c.expertise.visible) { <li><a routerLink="/" fragment="expertise">{{ c.expertise.label }}</a></li> }
              <li><a routerLink="/" fragment="contact">{{ c.contact.label || 'Contact' }}</a></li>
            </ul></div>
            <div><h3>Writing</h3><ul>
              <li><a routerLink="/blog">All articles</a></li>
              <li><a routerLink="/videos">Videos</a></li>
              @for (a of recent(); track a.id) { <li><a [routerLink]="a.path">{{ a.title }}</a></li> }
              <li><a href="/sitemap.xml">sitemap.xml</a></li>
              <li><a href="/llms.txt">llms.txt</a></li>
            </ul></div>
            @if (links().length) {
              <div><h3>Elsewhere</h3><ul>
                @for (l of links(); track $index) {
                  <li><a [href]="l.url" rel="me noopener" target="_blank">{{ l.kind === 'youtube' && id.youtube_handle ? 'YouTube ' + id.youtube_handle : l.label }}</a></li>
                }
              </ul></div>
            }
            <div><h3>Legal</h3><ul>
              <li><a routerLink="/privacy">Privacy notice (DPDP)</a></li>
              <li><a routerLink="/terms">Terms of use</a></li>
              <li><a routerLink="/ai-policy">AI policy</a></li>
            </ul></div>
          </nav>
        </details>
      </div>
    </footer>

    @if (ui.resumeOpen()) { <app-resume-modal (close)="ui.resumeOpen.set(false)" /> }

    @if (consent.needed && consent.choice() === null) {
      <div class="consent" role="dialog" aria-labelledby="consent-title" aria-live="polite">
        <strong id="consent-title">Analytics cookies</strong>
        <p>With your permission I use Google Analytics to understand which articles are useful. No advertising cookies. You can change this any time from "Cookie settings" in the footer.</p>
        <div class="actions">
          <button class="btn btn-primary btn-sm" type="button" (click)="consent.set('granted')">Allow analytics</button>
          <button class="btn btn-outline btn-sm" type="button" (click)="consent.set('denied')">Decline</button>
        </div>
      </div>
    }
    @if (toast.message()) { <div class="toast" role="status">{{ toast.message() }}</div> }
  `,
})
export class ShellComponent {
  readonly site = inject(SiteContentService);
  readonly ui = inject(UiState);
  readonly toast = inject(Toast);
  readonly consent = inject(ConsentService);
  private api = inject(PublicApi);
  private doc = inject(DOCUMENT);
  private browser = isPlatformBrowser(inject(PLATFORM_ID));
  readonly year = new Date().getFullYear();
  readonly mobile = signal(false);
  readonly mega = signal(false);
  readonly blogEnabled = toSignal(this.api.site().pipe(map((c) => c.blog_enabled)), { initialValue: false });
  readonly resumeAvailable = toSignal(this.api.site().pipe(map((c) => c.resume_available)), { initialValue: false });
  readonly links = computed(() => profileLinks(this.site.content()));
  /** "Explore" cards; work and career hide with their sections, blog with the blog. */
  readonly megaCards = computed(() => {
    const c = this.site.content();
    const n = c.navigation;
    const cards: Array<{ icon: string; card: NavCard; link: string; fragment?: string }> = [];
    if (c.work.visible) cards.push({ icon: '↗', card: n.mega_work, link: '/', fragment: 'work' });
    cards.push({ icon: '▶', card: n.mega_videos, link: '/videos' });
    if (c.experience.visible) cards.push({ icon: '⌁', card: n.mega_career, link: '/', fragment: 'experience' });
    if (this.blogEnabled()) cards.push({ icon: '✳', card: n.mega_blog, link: '/blog' });
    return cards.filter((m) => m.card.title);
  });
  readonly recent = toSignal(this.api.articles(6).pipe(map((r) => r.items), catchError(() => of([]))), { initialValue: [] });

  constructor() {
    this.site.ensureLoaded();
    this.api.site().subscribe((c) => this.consent.init(c.gtm_id, c.ga4_id));
    inject(Router).events.pipe(filter((e) => e instanceof NavigationEnd)).subscribe(() => {
      this.closeMenus();
      if (this.browser && !this.doc.location.hash) this.doc.getElementById('main')?.focus({ preventScroll: true });
    });
  }

  closeMenus() {
    this.mobile.set(false);
    this.mega.set(false);
  }

  outside(e: Event) {
    const t = e.target as HTMLElement | null;
    if (this.mega() && t && !t.closest('.menu-wrap')) this.mega.set(false);
  }
}
