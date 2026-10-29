import { DOCUMENT, isPlatformBrowser } from '@angular/common';
import { Component, PLATFORM_ID, inject, signal } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { NavigationEnd, Router, RouterLink, RouterLinkActive, RouterOutlet } from '@angular/router';
import { catchError, filter, map, of } from 'rxjs';
import { ConsentService } from '../core/consent.service';
import { PROFILE } from '../core/profile';
import { PublicApi } from '../core/public-api.service';
import { ResumeModalComponent } from '../shared/resume-modal.component';
import { ScrollRevealDirective } from '../shared/scroll-reveal.directive';
import { Toast } from '../shared/toast.service';
import { UiState } from '../shared/ui-state.service';

@Component({
  selector: 'app-shell',
  imports: [RouterOutlet, RouterLink, RouterLinkActive, ResumeModalComponent, ScrollRevealDirective],
  host: { '(document:keydown.escape)': 'closeMenus()', '(document:click)': 'outside($event)' },
  template: `
    <a class="skip-link" href="#main">Skip to content</a>
    <header class="site-header">
      <div class="wrap nav">
        <a class="brand" routerLink="/" aria-label="{{ p.name }}, home">
          <span class="brand-mark" aria-hidden="true">{{ p.initials }}</span>
          <span>{{ p.name }}<small>Oracle DBA · DRE</small></span>
        </a>
        <button class="mobile-toggle" type="button" (click)="mobile.set(!mobile())" [attr.aria-expanded]="mobile()" aria-controls="navlinks">Menu ☰</button>
        <nav class="navlinks" id="navlinks" [class.mobile-open]="mobile()" aria-label="Main navigation">
          <a routerLink="/" fragment="about" (click)="closeMenus()">About</a>
          <a routerLink="/" fragment="experience" (click)="closeMenus()">Experience</a>
          <div class="menu-wrap" #menuWrap>
            <button class="nav-trigger" type="button" (click)="mega.set(!mega())" [attr.aria-expanded]="mega()" aria-controls="mega">Explore ▾</button>
            <div class="mega" id="mega" [class.open]="mega()">
              <a class="mega-card" routerLink="/" fragment="work" (click)="closeMenus()"><span aria-hidden="true">↗</span><strong>Selected work</strong><span>HA, DR, patching and automation outcomes.</span></a>
              <a class="mega-card" routerLink="/videos" (click)="closeMenus()"><span aria-hidden="true">▶</span><strong>Videos</strong><span>Walkthroughs on YouTube &#64;csayantan.</span></a>
              <a class="mega-card" routerLink="/" fragment="experience" (click)="closeMenus()"><span aria-hidden="true">⌁</span><strong>Career timeline</strong><span>Fifteen years from EBS to DRE.</span></a>
              @if (blogEnabled()) {
                <a class="mega-card" routerLink="/blog" (click)="closeMenus()"><span aria-hidden="true">✳</span><strong>Blog &amp; notes</strong><span>Oracle DBA articles and lessons learned.</span></a>
              }
            </div>
          </div>
          @if (blogEnabled()) { <a routerLink="/blog" routerLinkActive="active" (click)="closeMenus()">Blog</a> }
          <a routerLink="/" fragment="contact" (click)="closeMenus()">Contact</a>
          <button class="btn btn-primary" type="button" (click)="ui.resumeOpen.set(true); closeMenus()">Request resume ↗</button>
        </nav>
      </div>
    </header>

    <main id="main" tabindex="-1" appScrollReveal><router-outlet /></main>

    <footer class="site-footer">
      <div class="wrap">
        <div class="footer-row">
          <span>© {{ year }} {{ p.name }} · Bangalore, India</span>
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
              <li><a routerLink="/" fragment="about">About</a></li>
              <li><a routerLink="/" fragment="experience">Experience</a></li>
              <li><a routerLink="/" fragment="work">Selected work</a></li>
              <li><a routerLink="/" fragment="expertise">Expertise</a></li>
              <li><a routerLink="/" fragment="contact">Contact</a></li>
            </ul></div>
            <div><h3>Writing</h3><ul>
              <li><a routerLink="/blog">All articles</a></li>
              <li><a routerLink="/videos">Videos</a></li>
              @for (a of recent(); track a.id) { <li><a [routerLink]="a.path">{{ a.title }}</a></li> }
              <li><a href="/sitemap.xml">sitemap.xml</a></li>
              <li><a href="/llms.txt">llms.txt</a></li>
            </ul></div>
            <div><h3>Elsewhere</h3><ul>
              <li><a [href]="p.linkedin" rel="me noopener" target="_blank">LinkedIn</a></li>
              <li><a [href]="p.github" rel="me noopener" target="_blank">GitHub</a></li>
              <li><a [href]="p.youtube" rel="me noopener" target="_blank">YouTube &#64;csayantan</a></li>
            </ul></div>
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
  readonly p = PROFILE;
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
  readonly recent = toSignal(this.api.articles(6).pipe(map((r) => r.items), catchError(() => of([]))), { initialValue: [] });

  constructor() {
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
