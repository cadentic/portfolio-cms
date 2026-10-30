import { DatePipe } from '@angular/common';
import { Component, computed, inject } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { RouterLink } from '@angular/router';
import { catchError, map, of } from 'rxjs';
import { PublicApi } from '../core/public-api.service';
import { Seo } from '../core/seo.service';
import { profileLinks, sectionNumbers } from '../core/site-content';
import { SiteContentService } from '../core/site-content.service';
import { ContactFormComponent } from '../shared/contact-form.component';
import { NewsletterFormComponent } from '../shared/newsletter-form.component';
import { UiState } from '../shared/ui-state.service';
import { YoutubeComponent } from '../shared/youtube.component';

/** The home page, driven entirely by the editable site content. */
@Component({
  selector: 'app-home',
  imports: [RouterLink, DatePipe, NewsletterFormComponent, ContactFormComponent, YoutubeComponent],
  template: `
    @let c = site.content();
    @let h = c.hero;
    @let n = numbers();
    <section class="hero" aria-labelledby="hero-title">
      <div class="wrap hero-grid">
        <div>
          @if (h.eyebrow) { <div class="eyebrow">{{ h.eyebrow }}</div> }
          <h1 id="hero-title">{{ h.title }} @if (h.title_highlight) {<em>{{ h.title_highlight }}</em>}</h1>
          <p class="hero-lead">{{ h.lead }}</p>
          @let primary = h.primary_cta_label && c.work.visible;
          @let resume = h.resume_cta_label && resumeAvailable();
          @if (primary || resume) {
            <div class="hero-actions">
              @if (primary) { <a class="btn btn-primary" routerLink="/" fragment="work">{{ h.primary_cta_label }} <span aria-hidden="true">↗</span></a> }
              @if (resume) { <button class="btn btn-dark-outline" type="button" (click)="ui.resumeOpen.set(true)">{{ h.resume_cta_label }} ↗</button> }
            </div>
          }
          @if (h.availability_visible && h.availability_text) {
            <div class="availability"><span class="dot" aria-hidden="true"></span> {{ h.availability_text }}</div>
          }
        </div>
        <aside class="hero-panel" aria-label="Professional snapshot">
          <div class="hero-photo">
            <img [src]="c.identity.photo_url" width="104" height="104" [alt]="'Portrait of ' + c.identity.name" fetchpriority="high" />
            <div><strong>{{ c.identity.name }}</strong><span>{{ c.identity.short_role }}</span></div>
          </div>
          @for (m of h.metrics; track $index) {
            <div class="metric"><strong>{{ m.value }}</strong><span>{{ m.label }}</span></div>
          }
          <div class="code-lines" aria-hidden="true"><span class="green">SQL&gt;</span> SELECT <span class="blue">status</span> FROM v$instance;<br>OPEN<br><span class="green">DGMGRL&gt;</span> show configuration;<br>SUCCESS</div>
        </aside>
      </div>
    </section>

    @if (c.about.visible) {
      @let a = c.about;
      <section class="section" id="about" aria-labelledby="about-title">
        <div class="wrap about-grid">
          <div>
            <div class="eyebrow">{{ n.about }} / {{ a.label }}</div>
            @if (a.stamp_kicker || a.stamp_lines.length || a.stamp_text) {
              <div class="about-stamp">
                @if (a.stamp_kicker) { <span class="kicker">{{ a.stamp_kicker }}</span> }
                @if (a.stamp_lines.length) {
                  <span class="big">@for (l of a.stamp_lines; track $index) {@if (!$first) {<br>}{{ l }} }</span>
                }
                @if (a.stamp_text) { <p>{{ a.stamp_text }}</p> }
              </div>
            }
          </div>
          <div class="about-copy">
            @if (a.kicker) { <div class="kicker">{{ a.kicker }}</div> }
            <h2 id="about-title">{{ a.heading }}</h2>
            @if (a.show_summary && c.identity.summary) { <p>{{ c.identity.summary }}</p> }
            @for (para of a.paragraphs; track $index) { <p>{{ para }}</p> }
            @if (a.skills.length) {
              <ul class="chips" aria-label="Core skills">@for (s of a.skills; track $index) { <li class="chip">{{ s }}</li> }</ul>
            }
          </div>
        </div>
      </section>
    }

    @if (c.experience.visible) {
      @let e = c.experience;
      <section class="section dark-section" id="experience" aria-labelledby="exp-title">
        <div class="wrap experience-grid">
          <div>
            <div class="eyebrow">{{ n.experience }} / {{ e.label }}</div>
            <h2 id="exp-title">{{ e.heading }}</h2>
            @if (e.intro) { <p>{{ e.intro }}</p> }
            @if (e.cta_label) { <a class="btn btn-dark-outline" routerLink="/" fragment="contact">{{ e.cta_label }} ↗</a> }
          </div>
          <div>
            @if (e.roles.length) {
              <ol class="timeline">
                @for (r of e.roles; track $index) {
                  <li class="timeline-item">
                    <div class="timeline-meta">{{ r.period }}</div>
                    <h3>{{ r.title }}</h3>
                    @if (r.org) { <div class="org">{{ r.org }}</div> }
                    @if (r.points.length) { <ul>@for (pt of r.points; track $index) { <li>{{ pt }}</li> }</ul> }
                  </li>
                }
              </ol>
            }
            @if (e.earlier_roles.length) {
              <details class="timeline-more">
                <summary>{{ e.earlier_label || 'Show earlier roles' }}</summary>
                <ol class="timeline">
                  @for (r of e.earlier_roles; track $index) {
                    <li class="timeline-item"><div class="timeline-meta">{{ r.period }}</div><h3>{{ r.title }}</h3>@if (r.org) {<div class="org">{{ r.org }}</div>}</li>
                  }
                </ol>
              </details>
            }
          </div>
        </div>
      </section>
    }

    @if (c.work.visible) {
      @let w = c.work;
      <section class="section" id="work" aria-labelledby="work-title">
        <div class="wrap">
          <div class="section-head"><div><div class="eyebrow">{{ n.work }} / {{ w.label }}</div><h2 id="work-title">{{ w.heading }}</h2></div>
            @if (w.intro) { <p>{{ w.intro }}</p> }</div>
          <ul class="project-grid" style="list-style:none;padding:0;margin:0">
            @for (it of w.items; track $index; let i = $index) {
              <li class="project-card">
                <div class="project-visual" [class.light]="i % 2 === 1"><span>{{ (i + 1).toString().padStart(2, '0') }}@if (it.tag) { / {{ it.tag.toUpperCase() }} }</span></div>
                <h3>{{ it.title }}</h3><p>{{ it.text }}</p>
              </li>
            }
          </ul>
        </div>
      </section>
    }

    @if (c.organizations.visible) {
      @let o = c.organizations;
      <section class="clients" id="organizations" aria-labelledby="org-title">
        <div class="wrap">
          <div class="client-head"><div class="eyebrow">{{ n.organizations }} / {{ o.label }}</div><h2 id="org-title">{{ o.heading }}</h2>
            @if (o.intro) { <p>{{ o.intro }}</p> }</div>
          <ul class="client-logos">
            @for (org of o.items; track $index) {
              <li class="client-logo">
                @if (org.url) {
                  <a [href]="org.url" target="_blank" rel="noopener nofollow">
                    @if (org.logo_url) { <img [src]="org.logo_url" [alt]="org.name" loading="lazy" decoding="async" width="160" height="48" /> } @else { {{ org.name }} }
                    @if (org.note) { <small>{{ org.note }}</small> }
                  </a>
                } @else {
                  @if (org.logo_url) { <img [src]="org.logo_url" [alt]="org.name" loading="lazy" decoding="async" width="160" height="48" /> } @else { {{ org.name }} }
                  @if (org.note) { <small>{{ org.note }}</small> }
                }
              </li>
            }
          </ul>
        </div>
      </section>
    }

    @if (c.expertise.visible) {
      @let x = c.expertise;
      <section class="section" id="expertise" aria-labelledby="expertise-title">
        <div class="wrap">
          <div class="section-head"><div><div class="eyebrow">{{ n.expertise }} / {{ x.label }}</div><h2 id="expertise-title">{{ x.heading }}</h2></div>
            @if (x.intro) { <p>{{ x.intro }}</p> }</div>
          <ul class="project-grid" style="list-style:none;padding:0;margin:0">
            @for (it of x.items; track $index) {
              <li class="project-card"><h3>{{ it.title }}</h3>@if (it.text) {<p>{{ it.text }}</p>}
                @if (it.chips.length) { <ul class="chips">@for (chip of it.chips; track $index) { <li class="chip">{{ chip }}</li> }</ul> }</li>
            }
          </ul>
        </div>
      </section>
    }

    @if (c.videos.visible) {
      @let v = c.videos;
      <section class="section dark-section" id="videos" aria-labelledby="videos-title">
        <div class="wrap video-grid">
          <app-youtube [id]="featured()?.id" [title]="featured()?.title || v.heading" />
          <div>
            <div class="eyebrow">{{ n.videos }} / {{ v.label }}</div>
            <h2 id="videos-title">{{ v.heading }}</h2>
            @if (v.intro) { <p>{{ v.intro }}</p> }
            @if (c.identity.youtube_url && (v.channel_cta_label || c.identity.youtube_handle)) {
              <a class="btn btn-primary" [href]="c.identity.youtube_url" target="_blank" rel="noopener">{{ v.channel_cta_label }} {{ c.identity.youtube_handle }} ↗</a>
            }
          </div>
        </div>
      </section>
    }

    @if (c.blog.visible) {
      @if (latest(); as a) {
        @let b = c.blog;
        <section class="section blog-band" id="blog" aria-labelledby="blog-title">
          <div class="wrap blog-layout">
            <div><div class="eyebrow">{{ n.blog }} / {{ b.label }}</div><h2 id="blog-title">{{ b.heading }}</h2>
              @if (b.intro) { <p>{{ b.intro }}</p> }
              @if (b.cta_label) { <a class="btn btn-outline" routerLink="/blog">{{ b.cta_label }} ↗</a> }</div>
            <article class="blog-feature">
              <div class="date">Latest article · {{ a.published_at | date: 'mediumDate' }}</div>
              <h3>{{ a.title }}</h3><p>{{ a.excerpt }}</p>
              <a class="btn btn-primary" [routerLink]="a.path">Read the article ↗</a>
            </article>
          </div>
        </section>
      }
    }

    @if (c.newsletter.visible) {
      <section id="newsletter" class="section" aria-label="Newsletter">
        <div class="wrap"><app-newsletter-form source="website" /></div>
      </section>
    }

    @if (c.faq.visible && c.faq.items.length) {
      <section class="section" id="faq" aria-labelledby="faq-title" style="padding-top:0">
        <div class="wrap" style="max-width:860px">
          @if (c.faq.label) { <div class="eyebrow">{{ c.faq.label }}</div> }
          <h2 id="faq-title">{{ c.faq.heading }}</h2>
          @for (f of c.faq.items; track $index) {
            <details class="project-card" style="min-height:0;margin-bottom:10px"><summary style="cursor:pointer;font-weight:750">{{ f.q }}</summary><p style="margin:10px 0 0">{{ f.a }}</p></details>
          }
        </div>
      </section>
    }

    <section class="section" id="contact" aria-labelledby="contact-title" style="padding-top:0">
      <div class="wrap contact-grid">
        <div class="contact-card">
          <div class="eyebrow">{{ n.contact }} / {{ c.contact.label }}</div>
          <h2 id="contact-title">{{ c.contact.heading }}</h2>
          @if (c.contact.intro) { <p>{{ c.contact.intro }}</p> }
          @if (c.contact.show_links && links().length) {
            <div class="contact-links">
              @for (l of links(); track $index) {
                <a class="btn btn-dark-outline" [href]="l.url" target="_blank" rel="me noopener">{{ l.label }} ↗</a>
              }
            </div>
          }
        </div>
        <app-contact-form />
      </div>
    </section>
  `,
})
export class HomeComponent {
  readonly site = inject(SiteContentService);
  readonly ui = inject(UiState);
  private api = inject(PublicApi);
  private seo = inject(Seo);
  readonly resumeAvailable = toSignal(this.api.site().pipe(map((c) => c.resume_available)), { initialValue: false });
  private articles = toSignal(this.api.articles(12).pipe(map((r) => r.items), catchError(() => of([]))), { initialValue: [] });
  readonly latest = () => this.articles()[0];
  readonly numbers = computed(() => sectionNumbers(this.site.content(), !!this.latest()));
  readonly links = computed(() => profileLinks(this.site.content()));
  /** The chosen featured video, else the newest article with a YouTube hero (D19). */
  readonly featured = computed(() => {
    const f = this.site.content().videos.featured;
    if (f.youtube_id) return { id: f.youtube_id, title: f.title };
    const a = this.articles().find((x) => x.hero?.type === 'youtube' && x.hero.youtube_id);
    return a ? { id: a.hero.youtube_id!, title: a.title } : null;
  });

  constructor() {
    const c = this.site.content();
    const ld: object[] = [
      { '@type': 'ProfilePage', '@id': this.seo.siteUrl + '/#profile', url: this.seo.siteUrl + '/', mainEntity: { '@id': this.seo.siteUrl + '/#person' } },
      this.seo.person(),
    ];
    if (c.faq.visible && c.faq.items.length) ld.push(this.seo.faq(c.faq.items));
    const f = c.videos.featured;
    // A VideoObject needs an upload date (D19), and the band must be on the page.
    if (c.videos.visible && f.youtube_id && f.published_on) {
      ld.push(this.seo.video(f.youtube_id, f.title, f.description, f.published_on));
    }
    this.seo.set({ title: c.seo.home_title, description: c.seo.home_description, path: '/', type: 'profile', jsonLd: ld });
  }
}
