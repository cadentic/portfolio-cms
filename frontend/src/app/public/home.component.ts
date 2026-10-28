import { DatePipe } from '@angular/common';
import { Component, inject } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { RouterLink } from '@angular/router';
import { catchError, map, of } from 'rxjs';
import { PROFILE } from '../core/profile';
import { PublicApi } from '../core/public-api.service';
import { Seo } from '../core/seo.service';
import { ContactFormComponent } from '../shared/contact-form.component';
import { NewsletterFormComponent } from '../shared/newsletter-form.component';
import { UiState } from '../shared/ui-state.service';
import { YoutubeComponent } from '../shared/youtube.component';

@Component({
  selector: 'app-home',
  imports: [RouterLink, DatePipe, NewsletterFormComponent, ContactFormComponent, YoutubeComponent],
  template: `
    <section class="hero" aria-labelledby="hero-title">
      <div class="wrap hero-grid">
        <div>
          <div class="eyebrow">Oracle DBA · Database Reliability Engineer · Bangalore</div>
          <h1 id="hero-title">Keeping critical databases <em>available, fast and recoverable.</em></h1>
          <p class="hero-lead">I'm {{ p.name }}. For 11+ years I've run Oracle RAC, Data Guard, GoldenGate and EBS estates for enterprise, government and healthcare teams, with 24x7 operations and tested disaster recovery.</p>
          <div class="hero-actions">
            <a class="btn btn-primary" routerLink="/" fragment="work">Explore selected work <span aria-hidden="true">↗</span></a>
            <button class="btn btn-dark-outline" type="button" (click)="ui.resumeOpen.set(true)">Verify email to get resume ↗</button>
          </div>
          <div class="availability"><span class="dot" aria-hidden="true"></span> Open to DBA / DRE roles and consulting engagements</div>
        </div>
        <aside class="hero-panel" aria-label="Professional snapshot">
          <div class="hero-photo">
            <img [src]="p.photo" width="104" height="104" [alt]="'Portrait of ' + p.name" fetchpriority="high" />
            <div><strong>{{ p.name }}</strong><span>{{ p.shortRole }}</span></div>
          </div>
          @for (m of p.metrics; track m.value) {
            <div class="metric"><strong>{{ m.value }}</strong><span>{{ m.label }}</span></div>
          }
          <div class="code-lines" aria-hidden="true"><span class="green">SQL&gt;</span> SELECT <span class="blue">status</span> FROM v$instance;<br>OPEN<br><span class="green">DGMGRL&gt;</span> show configuration;<br>SUCCESS</div>
        </aside>
      </div>
    </section>

    <section class="section" id="about" aria-labelledby="about-title">
      <div class="wrap about-grid">
        <div>
          <div class="eyebrow">01 / About</div>
          <div class="about-stamp"><span class="kicker">Principles</span><span class="big">Measure.<br>Protect.<br>Recover.</span><p>Observable systems, rehearsed recovery and calm incident response.</p></div>
        </div>
        <div class="about-copy">
          <div class="kicker">A little context</div>
          <h2 id="about-title">Deep Oracle expertise, with the whole platform in mind.</h2>
          <p>{{ p.summary }}</p>
          <p>Most recently I led cloud infrastructure services for Equifax production at UST, keeping GoldenGate replication within sub-second SLAs. Before that I ran Oracle and PostgreSQL platforms at IIT Delhi and Ndedge Informatics, and spent years implementing Oracle EBS for public-sector and manufacturing clients.</p>
          <ul class="chips" aria-label="Core skills">@for (s of p.skills; track s) { <li class="chip">{{ s }}</li> }</ul>
        </div>
      </div>
    </section>

    <section class="section dark-section" id="experience" aria-labelledby="exp-title">
      <div class="wrap experience-grid">
        <div>
          <div class="eyebrow">02 / Experience</div>
          <h2 id="exp-title">Built through real production challenges.</h2>
          <p>From Oracle EBS rollouts in 2010 to leading 24x7 database reliability for a global credit bureau.</p>
          <a class="btn btn-dark-outline" routerLink="/" fragment="contact">Discuss my experience ↗</a>
        </div>
        <div>
          <ol class="timeline">
            @for (e of p.experience; track e.period) {
              <li class="timeline-item">
                <div class="timeline-meta">{{ e.period }}</div>
                <h3>{{ e.title }}</h3>
                <div class="org">{{ e.org }}</div>
                <ul>@for (pt of e.points; track pt) { <li>{{ pt }}</li> }</ul>
              </li>
            }
          </ol>
          <details class="timeline-more">
            <summary>Show earlier roles (2010 – 2023)</summary>
            <ol class="timeline">
              @for (e of p.earlierExperience; track e.period) {
                <li class="timeline-item"><div class="timeline-meta">{{ e.period }}</div><h3>{{ e.title }}</h3><div class="org">{{ e.org }}</div></li>
              }
            </ol>
          </details>
        </div>
      </div>
    </section>

    <section class="section" id="work" aria-labelledby="work-title">
      <div class="wrap">
        <div class="section-head"><div><div class="eyebrow">03 / Selected work</div><h2 id="work-title">Outcomes that hold up in production.</h2></div>
          <p>Client details stay confidential; the scale, the approach and the measured results are real.</p></div>
        <ul class="project-grid" style="list-style:none;padding:0;margin:0">
          @for (a of p.achievements; track a.title; let i = $index) {
            <li class="project-card">
              <div class="project-visual" [class.light]="i % 2 === 1"><span>{{ (i + 1).toString().padStart(2, '0') }} / {{ a.tag.toUpperCase() }}</span></div>
              <h3>{{ a.title }}</h3><p>{{ a.text }}</p>
            </li>
          }
        </ul>
      </div>
    </section>

    <section class="clients" id="organizations" aria-labelledby="org-title">
      <div class="wrap">
        <div class="client-head"><div class="eyebrow">04 / Career ecosystem</div><h2 id="org-title">Organisations &amp; engagements</h2>
          <p>A selection of employers and client engagements from my career. Names only; no endorsement implied.</p></div>
        <ul class="client-logos">@for (o of p.organizations; track o.name) { <li class="client-logo">{{ o.name }}<small>{{ o.note }}</small></li> }</ul>
      </div>
    </section>

    <section class="section" id="expertise" aria-labelledby="expertise-title">
      <div class="wrap">
        <div class="section-head"><div><div class="eyebrow">05 / Expertise</div><h2 id="expertise-title">What I bring to the team.</h2></div>
          <p>Grouped by the problems they solve rather than by tool lists.</p></div>
        <ul class="project-grid" style="list-style:none;padding:0;margin:0">
          @for (x of p.expertise; track x.title) {
            <li class="project-card"><h3>{{ x.title }}</h3><p>{{ x.text }}</p>
              <ul class="chips">@for (c of x.chips; track c) { <li class="chip">{{ c }}</li> }</ul></li>
          }
        </ul>
      </div>
    </section>

    <section class="section dark-section" id="videos" aria-labelledby="videos-title">
      <div class="wrap video-grid">
        <app-youtube [id]="featuredVideo()?.id" [title]="featuredVideo()?.title ?? 'Oracle DBA walkthroughs'" />
        <div>
          <div class="eyebrow">06 / Videos</div>
          <h2 id="videos-title">Watch the walkthroughs.</h2>
          <p>Hands-on videos on Oracle RAC, Data Guard, RMAN recovery and day-to-day DBA work.</p>
          <a class="btn btn-primary" [href]="p.youtube" target="_blank" rel="noopener">Subscribe on YouTube &#64;csayantan ↗</a>
        </div>
      </div>
    </section>

    @if (latest(); as a) {
      <section class="section blog-band" id="blog" aria-labelledby="blog-title">
        <div class="wrap blog-layout">
          <div><div class="eyebrow">07 / Notes &amp; insights</div><h2 id="blog-title">Ideas worth sharing.</h2>
            <p>Articles on Oracle administration, reliability engineering and lessons from production.</p>
            <a class="btn btn-outline" routerLink="/blog">All articles ↗</a></div>
          <article class="blog-feature">
            <div class="date">Latest article · {{ a.published_at | date: 'mediumDate' }}</div>
            <h3>{{ a.title }}</h3><p>{{ a.excerpt }}</p>
            <a class="btn btn-primary" [routerLink]="a.path">Read the article ↗</a>
          </article>
        </div>
      </section>
    }

    <section class="section" aria-label="Newsletter">
      <div class="wrap"><app-newsletter-form source="website" [consentText]="consentText()" /></div>
    </section>

    <section class="section" id="faq" aria-labelledby="faq-title" style="padding-top:0">
      <div class="wrap" style="max-width:860px">
        <div class="eyebrow">Frequently asked</div>
        <h2 id="faq-title">Quick answers</h2>
        @for (f of p.faq; track f.q) {
          <details class="project-card" style="min-height:0;margin-bottom:10px"><summary style="cursor:pointer;font-weight:750">{{ f.q }}</summary><p style="margin:10px 0 0">{{ f.a }}</p></details>
        }
      </div>
    </section>

    <section class="section" id="contact" aria-labelledby="contact-title" style="padding-top:0">
      <div class="wrap contact-grid">
        <div class="contact-card">
          <div class="eyebrow">08 / Contact</div>
          <h2 id="contact-title">Let's talk about the work ahead.</h2>
          <p>Have a role, a migration or a reliability problem in mind? Send a note with the context that matters.</p>
          <div class="contact-links">
            <a class="btn btn-dark-outline" [href]="p.linkedin" target="_blank" rel="me noopener">LinkedIn ↗</a>
            <a class="btn btn-dark-outline" [href]="p.github" target="_blank" rel="me noopener">GitHub ↗</a>
            <a class="btn btn-dark-outline" [href]="p.youtube" target="_blank" rel="me noopener">YouTube ↗</a>
          </div>
        </div>
        <app-contact-form />
      </div>
    </section>
  `,
})
export class HomeComponent {
  readonly p = PROFILE;
  readonly ui = inject(UiState);
  private api = inject(PublicApi);
  private seo = inject(Seo);
  readonly consentText = toSignal(this.api.site().pipe(map((c) => c.consent_text)), { initialValue: '' });
  private articles = toSignal(this.api.articles(12).pipe(map((r) => r.items), catchError(() => of([]))), { initialValue: [] });
  readonly latest = () => this.articles()[0];
  readonly featuredVideo = () => {
    const a = this.articles().find((x) => x.hero?.type === 'youtube' && x.hero.youtube_id);
    return a ? { id: a.hero.youtube_id!, title: a.title } : null;
  };

  constructor() {
    this.seo.set({
      title: `${PROFILE.name} · Oracle DBA & Database Reliability Engineer`,
      description: 'Oracle Database Administrator and Database Reliability Engineer in Bangalore with 11+ years of RAC, Data Guard, GoldenGate, RMAN and Oracle EBS experience. Portfolio, articles and videos.',
      path: '/',
      type: 'profile',
      jsonLd: [
        { '@type': 'ProfilePage', '@id': this.seo.siteUrl + '/#profile', url: this.seo.siteUrl + '/', mainEntity: { '@id': this.seo.siteUrl + '/#person' } },
        this.seo.person(),
        this.seo.faq(PROFILE.faq),
      ],
    });
  }
}
