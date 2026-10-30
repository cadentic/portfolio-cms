import { NgTemplateOutlet, formatDate } from '@angular/common';
import { HttpEventType, HttpResponse } from '@angular/common/http';
import {
  Component,
  Injector,
  LOCALE_ID,
  afterNextRender,
  computed,
  inject,
  signal,
} from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { filter, map, tap } from 'rxjs';
import { SectionKey, SiteContent } from '../../core/site-content';
import { AdminApi, errMsg } from '../admin-api.service';
import { MediaPickerComponent } from '../media/media-picker.component';
import { formatBytes } from '../media/media-upload';
import { HistoryDialogComponent } from './history-dialog.component';
import { ListEditorComponent } from './list-editor.component';
import {
  AREA_TITLES,
  ApiError,
  FieldError,
  GROUPS,
  Group,
  Match,
  ResumeStatus,
  SiteArea,
  SiteContentAdminDoc,
  SiteContentPut,
  SiteContentReset,
  blankEarlierRole,
  blankEducation,
  blankExpertiseItem,
  blankLink,
  blankMetric,
  blankOrganization,
  blankQA,
  blankRole,
  blankVideo,
  blankWorkItem,
  copyFields,
  findMatches,
  groupOf,
  lim,
  replaceMatches,
} from './site-content.model';
import { SiteDraft } from './site-draft.service';
import { uiLabel } from './site-labels';
import { SiteErrorsComponent, SiteFieldComponent, fieldId } from './site-field.component';
import { UnsavedChanges } from './unsaved-changes.guard';

const AREA_INTROS: Record<SiteArea, string> = {
  home: 'The text of your home page, section by section. Open a section to edit it.',
  profile:
    'Your name, photo, links, menu text and resume, used on every page of the site and by search engines.',
  videos: 'The videos on the /videos page and the text around them.',
};

// Hints live here rather than in the template, where "{years}" and "@" would need escaping.
const H = {
  years:
    'Used wherever the text says {years}: the hero, key numbers, About, FAQ, search description and llms.txt.',
  yearsProfile:
    'Type the number once. Wherever your text says {years} (hero, key numbers, About, FAQ, search description and llms.txt), the site shows this number instead. It is the same field as Home page → Hero & key numbers → Years of experience.',
  label: 'Shown after the section number above the heading, e.g. “01 / About”.',
  labelNav:
    'Shown after the section number above the heading, e.g. “01 / About”, and as the link in the menu and the footer site map.',
  labelFooter:
    'Shown after the section number above the heading, e.g. “01 / About”, and as the link in the footer site map.',
  metricValue: 'Short, e.g. “{years}+ years” or “99.99%”.',
  handle: 'Shown next to YouTube links, e.g. @csayantan. The @ is added if you leave it out.',
  handlePlaceholder: '@yourhandle',
  link: 'The full address, starting with https://. Leave empty to hide it.',
  channelCta:
    'Shown as “<this text> @handle ↗” and links to your YouTube channel (Site profile → Links & YouTube handle). Hidden when there is no channel link.',
  videoLink:
    'Paste the link from YouTube’s Share button; it becomes the 11-character video ID when you save.',
  uploadDate: 'Needed for Google video results; it’s shown under the video on YouTube.',
};

/**
 * One editor for the three site content areas (D22). The draft lives in
 * SiteDraft, so switching between Home page, Site profile and Videos keeps it.
 */
@Component({
  selector: 'app-site-content-editor',
  imports: [
    FormsModule,
    NgTemplateOutlet,
    RouterLink,
    HistoryDialogComponent,
    ListEditorComponent,
    MediaPickerComponent,
    SiteErrorsComponent,
    SiteFieldComponent,
  ],
  host: { '(window:beforeunload)': 'beforeUnload($event)' },
  template: `
    <div class="a-site">
      <h1>{{ title }}</h1>
      <p style="margin:0 0 6px">
        {{ intro }} Changes appear on the public site within a minute of saving. Every save is kept
        in History.
      </p>

      <div class="a-savebar">
        <button class="ab primary" type="button" [disabled]="busy() || !doc()" (click)="save()">
          Save changes
        </button>
        @if (dirty()) {
          <span class="pill pending">Unsaved changes</span>
          <button class="ab" type="button" [disabled]="busy()" (click)="discardChanges()">
            Discard changes
          </button>
        }
        <button class="ab" type="button" [disabled]="!doc()" (click)="historyOpen.set(true)">
          History
        </button>
        <a class="ab" [href]="viewUrl" target="_blank" rel="noopener">View site ↗</a>
        <div class="a-findbar" role="search" aria-label="Find and replace">
          <input
            aria-label="Find text"
            placeholder="Find…"
            [(ngModel)]="findText"
            (keydown.enter)="find()"
          />
          <input
            aria-label="Replace with"
            placeholder="Replace with…"
            [(ngModel)]="replText"
            (keydown.enter)="find()"
          />
          <label class="a-row"
            ><input type="checkbox" [(ngModel)]="wholeWord" /> Whole words only</label
          >
          <label class="a-row"
            ><input type="checkbox" [(ngModel)]="caseSensitive" /> Match case</label
          >
          <button class="ab" type="button" [disabled]="!doc()" (click)="find()">Find</button>
        </div>
        @if (notice()) {
          <p class="a-ok a-savebar-msg" role="status">{{ notice() }}</p>
        } @else if (problem()) {
          <p class="a-ferr a-savebar-msg">{{ problem() }}</p>
        }
      </div>

      @if (conflict()) {
        <div class="a-err" role="alert">
          <p style="margin:0 0 8px;color:inherit">{{ conflict() }}</p>
          <button class="ab sm" type="button" (click)="reloadLatest()">Reload latest</button>
        </div>
      }
      @if (error()) {
        <p class="a-err" role="alert">{{ error() }}</p>
      }
      @if (errors().length) {
        <div class="a-err" role="alert">
          <p style="margin:0 0 6px;color:inherit">
            Not saved. Fix {{ errors().length === 1 ? 'this' : 'these' }} and save again:
          </p>
          <ul class="a-msgs">
            @for (e of errors(); track $index) {
              <li><ng-container *ngTemplateOutlet="pathLink; context: { $implicit: e }" /></li>
            }
          </ul>
        </div>
      }
      @if (warnings().length) {
        <div class="a-warn" role="status">
          <p style="margin:0 0 6px;color:inherit">Please check:</p>
          <ul class="a-msgs">
            @for (w of warnings(); track $index) {
              <li><ng-container *ngTemplateOutlet="pathLink; context: { $implicit: w }" /></li>
            }
          </ul>
        </div>
      }
      <ng-template #pathLink let-e>
        @if (groupOf(e.path); as g) {
          @if (isHere(e.path)) {
            <a [href]="'#' + fid(e.path)" (click)="$event.preventDefault(); reveal(e.path)">{{
              uiLabel(e.path)
            }}</a
            >:
          } @else {
            <a [routerLink]="'/admin/site/' + g.area" [fragment]="'sec-' + g.id">{{
              uiLabel(e.path)
            }}</a>
            (on the {{ AREA_TITLES[g.area] }} screen):
          }
        } @else if (e.path) {
          {{ e.path }}:
        }
        {{ e.message }}
      </ng-template>

      @if (matches(); as ms) {
        <div class="a-card a-find" [attr.inert]="resetting() ? '' : null">
          <div class="a-row between">
            <strong role="status">
              @if (ms.length) {
                {{ ms.length }} {{ ms.length === 1 ? 'match' : 'matches' }} for “{{ findFor() }}” in
                all site content
              } @else {
                No matches for “{{ findFor() }}”.
              }
            </strong>
            <span class="a-row" style="gap:6px">
              <button
                class="ab primary sm"
                type="button"
                [disabled]="!ms.length"
                (click)="replaceAll()"
              >
                Replace all {{ ms.length }}
              </button>
              <button class="ab sm" type="button" (click)="matches.set(null)">Cancel</button>
            </span>
          </div>
          @if (yearsTip()) {
            <p class="a-hint" style="margin:8px 0 0">
              Years of experience is set once, in Home page → Hero &amp; key numbers → Years of
              experience.
            </p>
          }
          @if (ms.length) {
            <p class="a-hint" style="margin:8px 0 0">
              @if (replText) {
                Each match is shown crossed out, followed by its replacement.
              } @else {
                The “Replace with” box is empty, so Replace all would delete these.
              }
              Links, video IDs and dates are never changed. Nothing is saved until you click Save
              changes.
            </p>
            <ol class="a-find-list">
              @for (m of ms; track $index) {
                <li>
                  @if (isHere(m.path)) {
                    <a
                      [href]="'#' + fid(m.path)"
                      (click)="$event.preventDefault(); reveal(m.path)"
                      >{{ uiLabel(m.path) }}</a
                    >:
                  } @else {
                    <strong>{{ uiLabel(m.path) }}</strong
                    >:
                  }
                  {{ m.before }}<mark [class.a-del]="!!replText">{{ m.text }}</mark
                  ><ins [hidden]="!replText">{{ replText }}</ins
                  >{{ m.after }}
                </li>
              }
            </ol>
          }
        </div>
      }

      @if (doc(); as d) {
        @let c = d.content;
        <form
          ngNoForm
          novalidate
          class="a-sec-list"
          [attr.inert]="resetting() ? '' : null"
          (submit)="$event.preventDefault()"
          (input)="onEdit($event)"
          (change)="onEdit($event)"
        >
          @if (area === 'profile') {
            <section class="a-card a-form a-key" aria-label="Years of experience">
              <app-site-field
                path="identity.experience_years"
                label="Years of experience"
                kind="number"
                [hint]="H.yearsProfile"
              />
              <p class="a-hint" style="margin:0">
                Example: “{{ '{years}' }}+ years” shows as “{{ c.identity.experience_years }}+
                years”.
              </p>
            </section>
          }

          @for (g of groups; track g.id) {
            <details
              class="a-card a-sec"
              [id]="'sec-' + g.id"
              [open]="isOpen(g.id)"
              (toggle)="onToggle(g.id, $event)"
            >
              <summary>
                <h2>{{ g.title }}</h2>
                <span class="a-row" style="gap:6px">
                  @if (groupErrors(g); as n) {
                    <span class="pill critical">{{ n }} to fix</span>
                  }
                  @if (g.toggle) {
                    @if (isShown(c, g.toggle)) {
                      <span class="pill ok">Shown</span>
                    } @else {
                      <span class="pill draft">Hidden</span>
                    }
                  } @else if (g.id === 'hero' || g.id === 'contact') {
                    <span class="pill info">Always shown</span>
                  }
                  <span class="a-chev" aria-hidden="true">▾</span>
                </span>
              </summary>
              <div class="a-form a-sec-body">
                @if (g.toggle) {
                  <label class="a-row"
                    ><input
                      type="checkbox"
                      [attr.name]="g.toggle + '.visible'"
                      [ngModel]="isShown(c, g.toggle)"
                      (ngModelChange)="setShown(c, g.toggle!, $event)"
                    />
                    Show on the home page</label
                  >
                }
                @switch (g.id) {
                  @case ('hero') {
                    <app-site-field
                      path="identity.experience_years"
                      label="Years of experience"
                      kind="number"
                      [hint]="H.years"
                    />
                    <app-site-field
                      path="hero.eyebrow"
                      label="Line above the headline"
                      hint="Small text above the big headline, e.g. your role and city."
                    />
                    <div class="two">
                      <app-site-field
                        path="hero.title"
                        label="Headline"
                        hint="The big heading at the top of the home page."
                      />
                      <app-site-field
                        path="hero.title_highlight"
                        label="Headline ending"
                        hint="Shown in the accent colour right after the headline. Optional."
                      />
                    </div>
                    <app-site-field
                      path="hero.lead"
                      label="Intro paragraph"
                      kind="textarea"
                      [rows]="4"
                    />
                    <div class="two">
                      <app-site-field
                        path="hero.primary_cta_label"
                        label="Main button text"
                        hint="Scrolls to Selected work. Leave empty to hide the button."
                      />
                      <app-site-field
                        path="hero.resume_cta_label"
                        label="Resume button text"
                        hint="Opens the resume request form. Hidden when empty, or when there is no resume (Site profile → Resume)."
                      />
                    </div>
                    <label class="a-row"
                      ><input
                        type="checkbox"
                        name="hero.availability_visible"
                        [(ngModel)]="c.hero.availability_visible"
                      />
                      Show the availability line</label
                    >
                    <app-site-field
                      path="hero.availability_text"
                      label="Availability line"
                      hint="e.g. “Open to DBA / DRE roles and consulting engagements”. Required while the line is shown."
                    />
                    <h3>Key numbers</h3>
                    <p class="a-hint">Shown in the card next to the headline, under your photo.</p>
                    <app-list-editor
                      [items]="c.hero.metrics"
                      path="hero.metrics"
                      [max]="maxOf('hero.metrics')"
                      itemName="key number"
                      [newItem]="blank.metric"
                      (changed)="markDirty()"
                    >
                      <ng-template let-i="index">
                        <div class="two">
                          <app-site-field
                            [path]="'hero.metrics[' + i + '].value'"
                            label="Value"
                            [hint]="H.metricValue"
                          />
                          <app-site-field
                            [path]="'hero.metrics[' + i + '].label'"
                            label="What it means"
                            hint="e.g. “Oracle production experience”."
                          />
                        </div>
                      </ng-template>
                    </app-list-editor>
                    <app-site-errors path="hero.metrics" />
                  }
                  @case ('about') {
                    <div class="two">
                      <app-site-field
                        path="about.label"
                        label="Section label"
                        [hint]="H.labelNav"
                      />
                      <app-site-field
                        path="about.kicker"
                        label="Line above the heading"
                        hint="Optional."
                      />
                    </div>
                    <app-site-field path="about.heading" label="Heading" />
                    <label class="a-row"
                      ><input
                        type="checkbox"
                        name="about.show_summary"
                        [(ngModel)]="c.about.show_summary"
                      />
                      Start with your summary</label
                    >
                    <p class="a-hint">
                      Your summary is edited in Site profile → Name, role &amp; photo.
                    </p>
                    <app-site-field
                      path="about.paragraphs"
                      label="More paragraphs"
                      kind="lines"
                      [rows]="6"
                      hint="Each line becomes a paragraph."
                    />
                    <app-site-field
                      path="about.skills"
                      label="Skills"
                      kind="lines"
                      [rows]="6"
                      hint="Shown as tags, and listed for search engines."
                    />
                    <h3>Principles card</h3>
                    <p class="a-hint">
                      The small card beside the text. It is hidden when all three are empty.
                    </p>
                    <app-site-field path="about.stamp_kicker" label="Card title" />
                    <app-site-field
                      path="about.stamp_lines"
                      label="Card lines"
                      kind="lines"
                      [rows]="3"
                    />
                    <app-site-field
                      path="about.stamp_text"
                      label="Card text"
                      kind="textarea"
                      [rows]="2"
                    />
                  }
                  @case ('experience') {
                    <div class="two">
                      <app-site-field
                        path="experience.label"
                        label="Section label"
                        [hint]="H.labelNav"
                      />
                      <app-site-field
                        path="experience.cta_label"
                        label="Button text"
                        hint="Scrolls to Contact. Leave empty to hide the button."
                      />
                    </div>
                    <app-site-field path="experience.heading" label="Heading" />
                    <app-site-field
                      path="experience.intro"
                      label="Intro"
                      kind="textarea"
                      [rows]="2"
                    />
                    <h3>Roles</h3>
                    <app-list-editor
                      [items]="c.experience.roles"
                      path="experience.roles"
                      [max]="maxOf('experience.roles')"
                      itemName="role"
                      [newItem]="blank.role"
                      (changed)="markDirty()"
                    >
                      <ng-template let-i="index">
                        @let p = 'experience.roles[' + i + ']';
                        <div class="two">
                          <app-site-field
                            [path]="p + '.period'"
                            label="Period"
                            hint="e.g. “2023 – present”."
                          />
                          <app-site-field [path]="p + '.title'" label="Job title" />
                        </div>
                        <app-site-field [path]="p + '.org'" label="Organisation" />
                        <app-site-field
                          [path]="p + '.points'"
                          label="Highlights"
                          kind="lines"
                          [rows]="4"
                        />
                      </ng-template>
                    </app-list-editor>
                    <app-site-errors path="experience.roles" />
                    <h3>Earlier roles</h3>
                    <app-site-field
                      path="experience.earlier_label"
                      label="“Show earlier roles” link text"
                      hint="What visitors click to see the roles below."
                    />
                    <app-list-editor
                      [items]="c.experience.earlier_roles"
                      path="experience.earlier_roles"
                      [max]="maxOf('experience.earlier_roles')"
                      itemName="earlier role"
                      [newItem]="blank.earlierRole"
                      (changed)="markDirty()"
                    >
                      <ng-template let-i="index">
                        @let p = 'experience.earlier_roles[' + i + ']';
                        <div class="two">
                          <app-site-field [path]="p + '.period'" label="Period" />
                          <app-site-field [path]="p + '.title'" label="Job title" />
                        </div>
                        <app-site-field [path]="p + '.org'" label="Organisation" />
                      </ng-template>
                    </app-list-editor>
                    <app-site-errors path="experience.earlier_roles" />
                  }
                  @case ('work') {
                    <app-site-field
                      path="work.label"
                      label="Section label"
                      [hint]="H.labelFooter"
                    />
                    <app-site-field path="work.heading" label="Heading" />
                    <app-site-field path="work.intro" label="Intro" kind="textarea" [rows]="2" />
                    <h3>Projects</h3>
                    <app-list-editor
                      [items]="c.work.items"
                      path="work.items"
                      [max]="maxOf('work.items')"
                      itemName="project"
                      [newItem]="blank.workItem"
                      (changed)="markDirty()"
                    >
                      <ng-template let-i="index">
                        @let p = 'work.items[' + i + ']';
                        <div class="two">
                          <app-site-field
                            [path]="p + '.tag'"
                            label="Tag"
                            hint="A short category, e.g. “High availability”."
                          />
                          <app-site-field [path]="p + '.title'" label="Title" />
                        </div>
                        <app-site-field
                          [path]="p + '.text'"
                          label="Description"
                          kind="textarea"
                          [rows]="3"
                        />
                      </ng-template>
                    </app-list-editor>
                    <app-site-errors path="work.items" />
                  }
                  @case ('organizations') {
                    <app-site-field
                      path="organizations.label"
                      label="Section label"
                      [hint]="H.label"
                    />
                    <app-site-field path="organizations.heading" label="Heading" />
                    <app-site-field
                      path="organizations.intro"
                      label="Intro"
                      kind="textarea"
                      [rows]="2"
                    />
                    @if (namesOnly(c)) {
                      <p class="a-warn">
                        Your note says 'Names only' but logos are shown; consider updating it.
                      </p>
                    }
                    <h3>Organisations</h3>
                    <datalist id="org-notes">
                      <option value="Employer"></option>
                      <option value="Client engagement"></option>
                      <option value="Project engagement"></option>
                    </datalist>
                    <app-list-editor
                      [items]="c.organizations.items"
                      path="organizations.items"
                      [max]="maxOf('organizations.items')"
                      itemName="organisation"
                      [newItem]="blank.organization"
                      (changed)="markDirty()"
                    >
                      <ng-template let-o let-i="index">
                        @let p = 'organizations.items[' + i + ']';
                        <div class="two">
                          <app-site-field
                            [path]="p + '.name'"
                            label="Name"
                            hint="Also the logo’s alt text."
                          />
                          <app-site-field
                            [path]="p + '.note'"
                            label="Note"
                            list="org-notes"
                            hint="e.g. Employer, Client engagement, Project engagement."
                          />
                        </div>
                        <app-site-field
                          [path]="p + '.url'"
                          label="Website"
                          kind="url"
                          hint="Optional. When set, the tile links to it."
                        />
                        <div class="a-field">
                          <span class="a-flabel">Logo</span>
                          <div class="a-row">
                            @if (o.logo_url) {
                              <img class="a-thumb" [src]="o.logo_url" [alt]="o.name + ' logo'" />
                            }
                            <input
                              class="a-mono a-grow"
                              readonly
                              aria-label="Logo file"
                              [id]="fid(p + '.logo_url')"
                              [value]="o.logo_url"
                              placeholder="No logo: the name is shown"
                            />
                          </div>
                          <div class="a-row">
                            <app-media-picker
                              kind="image"
                              [alt]="o.name"
                              label="Upload logo…"
                              (picked)="o.logo_url = $event.url; markDirty()"
                            />
                            @if (o.logo_url) {
                              <button
                                class="ab sm"
                                type="button"
                                (click)="o.logo_url = ''; markDirty(); focusUpload($event)"
                              >
                                Remove logo
                              </button>
                            }
                          </div>
                          <p class="a-hint">
                            PNG, WebP or JPEG; a transparent background works best. SVG files aren't
                            accepted: open the logo, take a screenshot with Snipping Tool (Windows +
                            Shift + S) and upload that PNG.
                          </p>
                          <app-site-errors [path]="p + '.logo_url'" [anchor]="false" />
                        </div>
                      </ng-template>
                    </app-list-editor>
                    <app-site-errors path="organizations.items" />
                  }
                  @case ('expertise') {
                    <app-site-field
                      path="expertise.label"
                      label="Section label"
                      [hint]="H.labelFooter"
                    />
                    <app-site-field path="expertise.heading" label="Heading" />
                    <app-site-field
                      path="expertise.intro"
                      label="Intro"
                      kind="textarea"
                      [rows]="2"
                    />
                    <h3>Areas</h3>
                    <app-list-editor
                      [items]="c.expertise.items"
                      path="expertise.items"
                      [max]="maxOf('expertise.items')"
                      itemName="area"
                      [newItem]="blank.expertiseItem"
                      (changed)="markDirty()"
                    >
                      <ng-template let-i="index">
                        @let p = 'expertise.items[' + i + ']';
                        <app-site-field [path]="p + '.title'" label="Title" />
                        <app-site-field
                          [path]="p + '.text'"
                          label="Description"
                          kind="textarea"
                          [rows]="2"
                        />
                        <app-site-field
                          [path]="p + '.chips'"
                          label="Tags"
                          kind="lines"
                          [rows]="3"
                        />
                      </ng-template>
                    </app-list-editor>
                    <app-site-errors path="expertise.items" />
                  }
                  @case ('videos-band') {
                    <app-site-field path="videos.label" label="Section label" [hint]="H.label" />
                    <app-site-field path="videos.heading" label="Heading" />
                    <app-site-field path="videos.intro" label="Intro" kind="textarea" [rows]="2" />
                    <app-site-field
                      path="videos.channel_cta_label"
                      label="Channel button text"
                      [hint]="H.channelCta"
                    />
                    <h3>Featured video</h3>
                    <p class="a-hint">
                      The video played on the home page. Leave the link empty to feature the newest
                      article that has a YouTube video.
                    </p>
                    <ng-container
                      *ngTemplateOutlet="
                        videoFields;
                        context: { $implicit: c.videos.featured, path: 'videos.featured' }
                      "
                    />
                  }
                  @case ('blog-band') {
                    <p class="a-hint">
                      The band appears only once at least one article is published.
                    </p>
                    <app-site-field path="blog.label" label="Section label" [hint]="H.label" />
                    <app-site-field path="blog.heading" label="Heading" />
                    <app-site-field path="blog.intro" label="Intro" kind="textarea" [rows]="2" />
                    <app-site-field
                      path="blog.cta_label"
                      label="Button text"
                      hint="Links to the blog. Leave empty to hide the button."
                    />
                  }
                  @case ('newsletter') {
                    <p class="a-hint">
                      This is the block on the home page. The sign-up form on the blog and on
                      articles is always shown. The consent text is in Subscribers &amp; Email →
                      Settings.
                    </p>
                    <app-site-field
                      path="newsletter.label"
                      label="Section label"
                      hint="Small line above the heading. Optional."
                    />
                    <app-site-field path="newsletter.heading" label="Heading" />
                    <app-site-field
                      path="newsletter.intro"
                      label="Intro"
                      kind="textarea"
                      [rows]="2"
                    />
                  }
                  @case ('faq') {
                    <app-site-field
                      path="faq.label"
                      label="Section label"
                      hint="Small line above the heading. Optional."
                    />
                    <app-site-field path="faq.heading" label="Heading" />
                    <h3>Questions</h3>
                    <p class="a-hint">Also given to search engines as an FAQ.</p>
                    <app-list-editor
                      [items]="c.faq.items"
                      path="faq.items"
                      [max]="maxOf('faq.items')"
                      itemName="question"
                      [newItem]="blank.qa"
                      (changed)="markDirty()"
                    >
                      <ng-template let-i="index">
                        <app-site-field [path]="'faq.items[' + i + '].q'" label="Question" />
                        <app-site-field
                          [path]="'faq.items[' + i + '].a'"
                          label="Answer"
                          kind="textarea"
                          [rows]="3"
                        />
                      </ng-template>
                    </app-list-editor>
                    <app-site-errors path="faq.items" />
                  }
                  @case ('contact') {
                    <app-site-field
                      path="contact.label"
                      label="Section label"
                      [hint]="H.labelNav"
                    />
                    <app-site-field path="contact.heading" label="Heading" />
                    <app-site-field path="contact.intro" label="Intro" kind="textarea" [rows]="2" />
                    <label class="a-row"
                      ><input
                        type="checkbox"
                        name="contact.show_links"
                        [(ngModel)]="c.contact.show_links"
                      />
                      Show link buttons (LinkedIn, GitHub, YouTube and other links)</label
                    >
                    <p class="a-hint">
                      The links are edited in Site profile → Links &amp; YouTube handle.
                    </p>
                  }
                  @case ('home-seo') {
                    <app-site-field
                      path="seo.home_title"
                      label="Page title"
                      hint="The headline of search results and the browser tab. Aim for 60 characters or fewer."
                    />
                    <app-site-field
                      path="seo.home_description"
                      label="Description"
                      kind="textarea"
                      [rows]="3"
                      hint="The text under the title in search results. Aim for 160 characters or fewer."
                    />
                  }

                  @case ('identity') {
                    <div class="two">
                      <app-site-field
                        path="identity.name"
                        label="Full name"
                        hint="Used across the site, in page titles and as the default author."
                      />
                      <app-site-field
                        path="identity.initials"
                        label="Initials"
                        hint="The small badge in the header."
                      />
                    </div>
                    <app-site-field
                      path="identity.short_role"
                      label="Role"
                      hint="Shown under your photo and given to search engines as your job title."
                    />
                    <div class="a-field">
                      <span class="a-flabel">Photo</span>
                      <div class="a-row">
                        @if (c.identity.photo_url) {
                          <img
                            class="a-thumb tall"
                            [src]="c.identity.photo_url"
                            alt="Current photo"
                          />
                        }
                        <input
                          class="a-mono a-grow"
                          readonly
                          aria-label="Photo file"
                          [id]="fid('identity.photo_url')"
                          [value]="c.identity.photo_url"
                        />
                      </div>
                      <div class="a-row">
                        <app-media-picker
                          kind="image"
                          [alt]="'Portrait of ' + c.identity.name"
                          label="Upload photo…"
                          (picked)="c.identity.photo_url = $event.url; markDirty()"
                        />
                        @if (c.identity.photo_url !== d.defaults.identity.photo_url) {
                          <button
                            class="ab sm"
                            type="button"
                            (click)="
                              c.identity.photo_url = d.defaults.identity.photo_url; markDirty()
                            "
                          >
                            Use the original photo
                          </button>
                        }
                      </div>
                      <p class="a-hint">
                        A square or portrait JPEG, PNG or WebP works best. The browser-tab icon does
                        not change.
                      </p>
                      <app-site-errors path="identity.photo_url" [anchor]="false" />
                    </div>
                    <app-site-field
                      path="identity.summary"
                      label="Summary"
                      kind="textarea"
                      [rows]="4"
                      hint="The first paragraph of About, and how search engines describe you."
                    />
                    <div class="two">
                      <app-site-field
                        path="identity.location"
                        label="Location (footer)"
                        hint="Shown in the footer after your name, e.g. “Bangalore, India”."
                      />
                      <app-site-field
                        path="identity.city"
                        label="City"
                        hint="Given to search engines."
                      />
                    </div>
                    <app-site-field
                      path="identity.country_code"
                      label="Country code"
                      placeholder="IN"
                      hint="Two letters, e.g. IN. Given to search engines."
                    />
                  }
                  @case ('links') {
                    <app-site-field
                      path="identity.linkedin_url"
                      label="LinkedIn profile"
                      kind="url"
                      [hint]="H.link"
                    />
                    <app-site-field
                      path="identity.github_url"
                      label="GitHub profile"
                      kind="url"
                      [hint]="H.link"
                    />
                    <app-site-field
                      path="identity.youtube_url"
                      label="YouTube channel"
                      kind="url"
                      [hint]="H.link"
                    />
                    <app-site-field
                      path="identity.youtube_handle"
                      label="YouTube handle"
                      [placeholder]="H.handlePlaceholder"
                      [hint]="H.handle"
                    />
                    <h3>Other links</h3>
                    <p class="a-hint">
                      Shown as buttons in Contact and in the footer, and given to search engines.
                    </p>
                    <app-list-editor
                      [items]="c.identity.other_links"
                      path="identity.other_links"
                      [max]="maxOf('identity.other_links')"
                      itemName="link"
                      [newItem]="blank.link"
                      (changed)="markDirty()"
                    >
                      <ng-template let-i="index">
                        <div class="two">
                          <app-site-field
                            [path]="'identity.other_links[' + i + '].label'"
                            label="Button text"
                          />
                          <app-site-field
                            [path]="'identity.other_links[' + i + '].url'"
                            label="Link"
                            kind="url"
                          />
                        </div>
                      </ng-template>
                    </app-list-editor>
                    <app-site-errors path="identity.other_links" />
                  }
                  @case ('navigation') {
                    <div class="two">
                      <app-site-field
                        path="navigation.brand_subtitle"
                        label="Subtitle under your name"
                        hint="Small text under your name in the header."
                      />
                      <app-site-field
                        path="navigation.resume_label"
                        label="Resume button text"
                        hint="The header button. Hidden when empty, or when there is no resume."
                      />
                    </div>
                    <p class="a-hint">
                      Menu and footer links to home page sections use each section's label (Home
                      page → the section → Section label).
                    </p>
                    <h3>“Explore” menu cards</h3>
                    <p class="a-hint">
                      The Selected work and Career cards hide with their home page sections.
                    </p>
                    @for (m of megaCards; track m.key) {
                      <fieldset class="a-block">
                        <legend>{{ m.title }}</legend>
                        <div class="two">
                          <app-site-field [path]="'navigation.' + m.key + '.title'" label="Title" />
                          <app-site-field [path]="'navigation.' + m.key + '.text'" label="Text" />
                        </div>
                      </fieldset>
                    }
                  }
                  @case ('education') {
                    <h3>Education</h3>
                    <app-list-editor
                      [items]="c.identity.education"
                      path="identity.education"
                      [max]="maxOf('identity.education')"
                      itemName="qualification"
                      [newItem]="blank.education"
                      (changed)="markDirty()"
                    >
                      <ng-template let-i="index">
                        @let p = 'identity.education[' + i + ']';
                        <app-site-field [path]="p + '.degree'" label="Degree" />
                        <div class="two">
                          <app-site-field [path]="p + '.school'" label="School or university" />
                          <app-site-field
                            [path]="p + '.years'"
                            label="Years"
                            hint="e.g. 2006 – 2009."
                          />
                        </div>
                      </ng-template>
                    </app-list-editor>
                    <app-site-errors path="identity.education" />
                    <app-site-field
                      path="identity.languages"
                      label="Languages"
                      kind="lines"
                      [rows]="3"
                      hint="Given to search engines."
                    />
                  }
                  @case ('resume') {
                    <p class="a-hint">
                      Visitors get your resume by email after confirming their address; the file is
                      never public. This part saves straight away; it does not need Save changes.
                    </p>
                    @if (resume(); as r) {
                      <p class="a-resume-status" aria-live="polite">
                        <strong>{{ resumeLine(r) }}</strong>
                      </p>
                      <div class="a-row">
                        <input
                          #resumeFile
                          type="file"
                          class="sr-only"
                          tabindex="-1"
                          aria-hidden="true"
                          accept="application/pdf"
                          (change)="uploadResume($event, r)"
                        />
                        <button
                          class="ab primary"
                          type="button"
                          [disabled]="resumeProgress() !== null"
                          (click)="resumeFile.click()"
                        >
                          {{
                            resumeProgress() !== null
                              ? 'Uploading ' + resumeProgress() + '%'
                              : r.source === 'upload'
                                ? 'Replace with a new PDF…'
                                : 'Upload resume PDF…'
                          }}
                        </button>
                        @if (r.source !== 'none') {
                          <button class="ab" type="button" (click)="downloadResume(r)">
                            Download current
                          </button>
                        }
                        @if (r.source === 'upload') {
                          <button class="ab danger" type="button" (click)="removeResume()">
                            Remove uploaded resume
                          </button>
                        }
                      </div>
                      <p class="a-hint">
                        PDF only, up to {{ fmt(r.max_bytes) }}. Visitors receive it as
                        {{ r.download_name }}.
                      </p>
                    } @else if (!resumeErr()) {
                      <p class="a-muted">Loading…</p>
                    }
                    @if (resumeErr()) {
                      <p class="a-err" role="alert">{{ resumeErr() }}</p>
                    }
                    @if (resumeMsg()) {
                      <p class="a-ok" aria-live="polite">{{ resumeMsg() }}</p>
                    }
                  }
                  @case ('blog-page') {
                    <p class="a-hint">The /blog page, which lists your articles.</p>
                    <app-site-field path="blog.page_heading" label="Page heading" />
                    <app-site-field
                      path="blog.page_intro"
                      label="Intro"
                      kind="textarea"
                      [rows]="2"
                    />
                    <app-site-field
                      path="blog.empty_text"
                      label="Text when there are no articles"
                      kind="textarea"
                      [rows]="2"
                    />
                    <app-site-field
                      path="seo.blog_title"
                      label="Page title in search results"
                      hint="Also the browser tab title. Aim for 60 characters or fewer."
                    />
                    <app-site-field
                      path="seo.blog_description"
                      label="Description in search results"
                      kind="textarea"
                      [rows]="2"
                      hint="Aim for 160 characters or fewer."
                    />
                  }
                  @case ('llms') {
                    <app-site-field
                      path="seo.llms_summary"
                      label="Summary for AI assistants"
                      kind="textarea"
                      [rows]="4"
                      hint="The short description at the top of /llms.txt, which AI assistants read to learn about you and this site."
                    />
                  }

                  @case ('video-list') {
                    <p class="a-hint">
                      The videos on the /videos page, in this order. Each one plays on the page. The
                      home page plays its own featured video, set in
                      <a routerLink="/admin/site/home" fragment="sec-videos-band"
                        >Home page → Videos band &amp; featured video</a
                      >.
                    </p>
                    <label class="a-row"
                      ><input
                        type="checkbox"
                        name="videos.include_article_videos"
                        [(ngModel)]="c.videos.include_article_videos"
                      />
                      Also list articles that have a YouTube video (after these)</label
                    >
                    <app-list-editor
                      [items]="c.videos.items"
                      path="videos.items"
                      [max]="maxOf('videos.items')"
                      itemName="video"
                      [newItem]="blank.video"
                      (changed)="markDirty()"
                    >
                      <ng-template let-v let-i="index">
                        <ng-container
                          *ngTemplateOutlet="
                            videoFields;
                            context: { $implicit: v, path: 'videos.items[' + i + ']' }
                          "
                        />
                      </ng-template>
                    </app-list-editor>
                    <app-site-errors path="videos.items" />
                  }
                  @case ('videos-page') {
                    <app-site-field path="videos.page_heading" label="Page heading" />
                    <app-site-field
                      path="videos.page_intro"
                      label="Intro"
                      kind="textarea"
                      [rows]="2"
                    />
                    <app-site-field
                      path="videos.page_cta_label"
                      label="Channel link text"
                      hint="Links to your YouTube channel at the top of /videos and under the placeholder."
                    />
                    <app-site-field
                      path="videos.empty_text"
                      label="Text when there are no videos"
                      kind="textarea"
                      [rows]="2"
                    />
                    <div class="two">
                      <app-site-field
                        path="videos.placeholder_title"
                        label="Placeholder title"
                        hint="Shown where a video would be when there is none at all."
                      />
                      <app-site-field path="videos.placeholder_text" label="Placeholder text" />
                    </div>
                  }
                  @case ('videos-seo') {
                    <app-site-field
                      path="seo.videos_title"
                      label="Page title"
                      hint="The headline of search results and the browser tab. Aim for 60 characters or fewer."
                    />
                    <app-site-field
                      path="seo.videos_description"
                      label="Description"
                      kind="textarea"
                      [rows]="3"
                      hint="The text under the title in search results. Aim for 160 characters or fewer."
                    />
                  }
                }
                @if (g.fields.length) {
                  <div class="a-row a-sec-foot">
                    <button class="ab sm" type="button" (click)="resetGroup(g)">
                      Reset “{{ g.title }}” to defaults
                    </button>
                  </div>
                }
              </div>
            </details>
          }
        </form>

        <ng-template #videoFields let-v let-p="path">
          <div class="a-video">
            @if (ytId(v.youtube_id); as id) {
              <img
                class="a-thumb yt"
                [src]="'https://i.ytimg.com/vi/' + id + '/mqdefault.jpg'"
                [alt]="'Thumbnail: ' + (v.title || 'video')"
                loading="lazy"
              />
            }
            <div class="a-form a-grow">
              <app-site-field
                [path]="p + '.youtube_id'"
                label="YouTube link or video ID"
                placeholder="https://youtu.be/…"
                [hint]="H.videoLink"
              />
              <app-site-field [path]="p + '.title'" label="Title" />
              <app-site-field
                [path]="p + '.description'"
                label="Description"
                kind="textarea"
                [rows]="2"
              />
              <app-site-field
                [path]="p + '.published_on'"
                label="Upload date"
                kind="date"
                [hint]="H.uploadDate"
              />
            </div>
          </div>
        </ng-template>

        @if (area === 'profile') {
          <section class="a-card a-form a-danger">
            <h2>Danger zone</h2>
            <p style="margin:0">
              Put every section of the Home page, Site profile and Videos back to the original site
              text. Your media library and resume stay as they are, and you can undo this from
              History.
            </p>
            <div>
              <button
                class="ab danger"
                type="button"
                [disabled]="busy()"
                (click)="resetEverything()"
              >
                Reset everything to defaults
              </button>
            </div>
          </section>
        }

        @if (historyOpen()) {
          <app-history-dialog
            [dirty]="dirty()"
            [baseUpdatedAt]="d.updated_at"
            (discardRequested)="discardChanges()"
            (siteRestored)="restored($event)"
            (closed)="historyOpen.set(false)"
          />
        }
      } @else if (!error()) {
        <p class="a-muted">Loading…</p>
      }
    </div>
  `,
})
export class SiteContentEditorComponent implements UnsavedChanges {
  private api = inject(AdminApi);
  private route = inject(ActivatedRoute);
  private injector = inject(Injector);
  private draft = inject(SiteDraft);
  private locale = inject(LOCALE_ID);
  readonly area: SiteArea = this.route.snapshot.data['area'] ?? 'home';
  readonly title = AREA_TITLES[this.area];
  readonly intro = AREA_INTROS[this.area];
  readonly groups = GROUPS.filter((g) => g.area === this.area);
  readonly viewUrl = this.area === 'videos' ? '/videos' : '/';
  readonly doc = this.draft.doc;
  readonly dirty = this.draft.dirty;
  readonly errors = this.draft.errors;
  readonly warnings = computed(() => this.doc()?.warnings ?? []);
  readonly error = signal('');
  readonly notice = signal('');
  readonly conflict = signal('');
  readonly busy = signal(false);
  /** Reset everything is running: the form is inert, as the reply replaces every field. */
  readonly resetting = signal(false);
  readonly historyOpen = signal(false);
  readonly open = signal<ReadonlySet<string>>(new Set());
  readonly problem = computed(() => {
    const n = this.errors().length;
    if (n) return `Not saved: ${n === 1 ? '1 problem' : n + ' problems'} to fix, marked in red.`;
    if (this.conflict()) return 'Not saved: the site content was changed elsewhere (see above).';
    return this.error() && this.doc() ? 'Not saved (see above).' : '';
  });

  findText = '';
  replText = '';
  wholeWord = true;
  caseSensitive = false;
  readonly matches = signal<Match[] | null>(null);
  readonly findFor = signal('');

  readonly resume = signal<ResumeStatus | null>(null);
  readonly resumeErr = signal('');
  readonly resumeMsg = signal('');
  readonly resumeProgress = signal<number | null>(null);

  readonly H = H;
  readonly AREA_TITLES = AREA_TITLES;
  readonly uiLabel = uiLabel;
  readonly groupOf = groupOf;
  readonly fid = fieldId;
  readonly fmt = formatBytes;
  readonly blank = {
    link: blankLink,
    education: blankEducation,
    metric: blankMetric,
    role: blankRole,
    earlierRole: blankEarlierRole,
    workItem: blankWorkItem,
    organization: blankOrganization,
    expertiseItem: blankExpertiseItem,
    video: blankVideo,
    qa: blankQA,
  };
  readonly megaCards = [
    { key: 'mega_work', title: 'Selected work card' },
    { key: 'mega_videos', title: 'Videos card' },
    { key: 'mega_career', title: 'Career card' },
    { key: 'mega_blog', title: 'Blog card' },
  ];

  constructor() {
    // An unsaved draft from another site content screen is kept; otherwise load fresh.
    if (!this.doc() || !this.dirty()) this.load();
    this.route.fragment.pipe(takeUntilDestroyed()).subscribe((f) => {
      if (this.doc()) this.goToFragment(f);
    });
    if (this.area === 'profile') this.loadResume();
  }

  // ---- Unsaved changes ----

  keepsDraftFor(url: string) {
    return /^\/admin\/site\/(home|profile|videos)(?=$|[?#])/.test(url);
  }
  discard() {
    this.draft.discard();
  }
  beforeUnload(e: BeforeUnloadEvent) {
    if (!this.dirty()) return;
    e.preventDefault();
    e.returnValue = '';
  }
  markDirty() {
    this.dirty.set(true);
    this.notice.set('');
  }
  /** Only document fields carry a name; the media picker's search box and file inputs don't count. */
  onEdit(e: Event) {
    if ((e.target as HTMLElement | null)?.getAttribute?.('name')) this.markDirty();
  }

  // ---- Load and save ----

  private load(then?: () => void) {
    this.api.get<SiteContentAdminDoc>('site-content').subscribe({
      next: (d) => {
        this.setDoc(d);
        this.goToFragment(this.route.snapshot.fragment);
        then?.();
      },
      error: (e) => this.error.set(errMsg(e)),
    });
  }
  private setDoc(d: SiteContentAdminDoc) {
    this.draft.doc.set(d);
    this.draft.dirty.set(false);
    this.draft.errors.set([]);
    this.error.set('');
    this.conflict.set('');
  }
  reloadLatest() {
    if (
      this.dirty() &&
      !confirm('Load the latest saved site content? Your unsaved changes will be lost.')
    )
      return;
    this.notice.set('');
    this.load();
  }
  /** Back to the last saved version, without leaving the screen (or the History dialog). */
  discardChanges() {
    if (!confirm('Discard your unsaved changes and go back to the last saved version?')) return;
    this.notice.set('');
    this.matches.set(null);
    this.load(() => this.notice.set('Your unsaved changes were discarded.'));
  }
  save() {
    const d = this.doc();
    if (!d || this.busy()) return;
    this.notice.set('');
    this.error.set('');
    this.conflict.set('');
    // An empty number field would be sent as null, which the server ignores: say so instead.
    if (!Number.isInteger(d.content.identity.experience_years)) {
      const l = lim(d.limits, 'identity.experience_years');
      const range = l?.min != null && l.max ? ` between ${l.min} and ${l.max}` : '';
      this.failedWith([
        { path: 'identity.experience_years', message: `enter a whole number${range}` },
      ]);
      return;
    }
    this.busy.set(true);
    // The fields stay editable while the request runs: remember what was sent.
    const sent = JSON.stringify(d.content);
    const body: SiteContentPut = { content: d.content, base_updated_at: d.updated_at };
    this.api.put<SiteContentAdminDoc>('site-content', body).subscribe({
      next: (r) => {
        this.busy.set(false);
        const cur = this.doc();
        if (cur && JSON.stringify(cur.content) !== sent) {
          // Edited since: keep those edits (still unsaved) on top of the saved version.
          this.draft.doc.set({ ...r, content: cur.content });
          this.draft.dirty.set(true);
          this.draft.errors.set([]);
          this.error.set('');
          this.conflict.set('');
          this.notice.set('Saved. Your newer changes are not saved yet.');
          return;
        }
        this.setDoc(r);
        this.notice.set('Saved.');
      },
      error: (e) => this.failed(e),
    });
  }
  private failed(e: unknown) {
    this.busy.set(false);
    this.resetting.set(false);
    const x = e as { status?: number; error?: ApiError };
    if (x?.status === 400 && x.error?.errors?.length) return this.failedWith(x.error.errors);
    if (x?.status === 409) {
      this.conflict.set(
        x.error?.error ??
          'Site content was changed elsewhere. Reload to see it; your edits are still in the form.',
      );
    } else this.error.set(errMsg(e));
    window.scrollTo({ top: 0 });
  }
  private failedWith(errors: FieldError[]) {
    this.draft.errors.set(errors);
    const first = errors[0];
    if (this.isHere(first.path)) this.reveal(first.path);
    else window.scrollTo({ top: 0 });
  }

  // ---- Groups and field navigation ----

  isOpen(id: string) {
    return this.open().has(id);
  }
  onToggle(id: string, e: Event) {
    const isOpen = (e.target as HTMLDetailsElement).open;
    if (isOpen !== this.isOpen(id)) {
      this.open.update((s) => {
        const n = new Set(s);
        if (isOpen) n.add(id);
        else n.delete(id);
        return n;
      });
    }
  }
  groupErrors(g: Group) {
    return this.errors().filter((e) => groupOf(e.path)?.id === g.id).length;
  }
  /** True when the field for `path` is on this screen. */
  isHere(path: string) {
    if (path === 'identity.experience_years' && this.area === 'profile') return true;
    return groupOf(path)?.area === this.area;
  }
  /** Opens the group holding `path`, then scrolls to the field and focuses it. */
  reveal(path: string) {
    const id = fieldId(path);
    const sec =
      document.getElementById(id)?.closest('details.a-sec')?.id.slice(4) ?? groupOf(path)?.id;
    if (sec) this.open.update((s) => new Set(s).add(sec));
    afterNextRender(
      () => {
        const el = document.getElementById(id) ?? document.getElementById('sec-' + sec);
        el?.scrollIntoView({ block: 'center' });
        if (el?.id === id) el.focus({ preventScroll: true });
      },
      { injector: this.injector },
    );
  }
  private goToFragment(f: string | null) {
    const id = f?.startsWith('sec-') ? f.slice(4) : '';
    if (!this.groups.some((g) => g.id === id)) return;
    this.open.update((s) => new Set(s).add(id));
    afterNextRender(
      () => document.getElementById('sec-' + id)?.scrollIntoView({ block: 'start' }),
      {
        injector: this.injector,
      },
    );
  }
  maxOf(path: string) {
    return lim(this.doc()?.limits, path)?.max ?? Infinity;
  }
  isShown(c: SiteContent, k?: SectionKey) {
    return !!k && !!(c[k] as { visible?: boolean }).visible;
  }
  setShown(c: SiteContent, k: SectionKey, v: boolean) {
    (c[k] as { visible?: boolean }).visible = v;
  }
  namesOnly(c: SiteContent) {
    return (
      c.organizations.items.some((o) => o.logo_url) && c.organizations.intro.includes('Names only')
    );
  }
  /** "Remove logo" removes itself: focus the upload button next to it instead. */
  focusUpload(e: Event) {
    const row = (e.target as HTMLElement).closest('.a-row');
    afterNextRender(() => row?.querySelector<HTMLElement>('app-media-picker button')?.focus(), {
      injector: this.injector,
    });
  }
  ytId(v: string) {
    return (
      /(?:^|youtu\.be\/|[?&]v=|\/(?:shorts|embed|live)\/)([A-Za-z0-9_-]{11})(?=$|[?&#/])/.exec(
        (v ?? '').trim(),
      )?.[1] ?? ''
    );
  }

  // ---- Resets and history ----

  resetGroup(g: Group) {
    const d = this.doc();
    if (!d) return;
    // Years of experience is a fact, not site text: it stays as it is.
    const fields = g.fields.filter((f) => f !== 'identity.experience_years');
    const years = fields.length < g.fields.length ? ' Years of experience stays as it is.' : '';
    if (
      !confirm(
        `Put “${g.title}” back to the original site text?${years} Nothing is saved until you click Save changes.`,
      )
    )
      return;
    copyFields(d.content, d.defaults, fields);
    this.markDirty();
    this.notice.set(`“${g.title}” is back to the original text. Review and save.`);
  }
  resetEverything() {
    const d = this.doc();
    if (!d) return;
    const lost = this.dirty() ? ' Your unsaved changes will be lost too.' : '';
    if (
      !confirm(
        `Reset all site content to the original site text?${lost} You can undo this from History.`,
      )
    )
      return;
    this.busy.set(true);
    this.resetting.set(true);
    const body: SiteContentReset = { all: true, base_updated_at: d.updated_at };
    this.api.post<SiteContentAdminDoc>('site-content/reset', body).subscribe({
      next: (r) => {
        this.busy.set(false);
        this.resetting.set(false);
        this.setDoc(r);
        this.notice.set('Everything is back to the original site text.');
        window.scrollTo({ top: 0 });
      },
      error: (e) => this.failed(e),
    });
  }
  restored(r: { doc: SiteContentAdminDoc; label: string }) {
    this.historyOpen.set(false);
    this.setDoc(r.doc);
    this.notice.set(`Restored ${r.label}. The site shows it within a minute.`);
  }

  // ---- Find & replace ----

  find() {
    const d = this.doc();
    if (!d) return;
    if (!this.findText) {
      this.matches.set(null);
      return;
    }
    this.findFor.set(this.findText);
    this.matches.set(
      findMatches(d.content, this.findText, {
        wholeWord: this.wholeWord,
        caseSensitive: this.caseSensitive,
      }),
    );
    window.scrollTo({ top: 0 });
  }
  replaceAll() {
    const d = this.doc();
    const ms = this.matches();
    if (!d || !ms?.length) return;
    const r = replaceMatches(d.content, ms, this.replText);
    this.matches.set(null);
    if (r.occurrences) this.markDirty();
    const occ = r.occurrences === 1 ? '1 occurrence' : `${r.occurrences} occurrences`;
    const fields = r.fields === 1 ? '1 field' : `${r.fields} fields`;
    this.notice.set(`Replaced ${occ} in ${fields}. Review and save.`);
  }
  /** "11", "11+" or "11 years" while experience_years is 11: that number lives in one field. */
  yearsTip() {
    const m = /^\s*(\d+)\s*\+?\s*(?:years?)?\s*$/i.exec(this.findFor());
    return !!m && Number(m[1]) === this.doc()?.content.identity.experience_years;
  }

  // ---- Resume (its own API; not part of the draft) ----

  private loadResume() {
    this.api.get<ResumeStatus>('resume').subscribe({
      next: (r) => this.resume.set(r),
      error: (e) => this.resumeErr.set(errMsg(e)),
    });
  }
  resumeLine(r: ResumeStatus) {
    if (r.source === 'upload') {
      const on = r.uploaded_at ? ' on ' + formatDate(r.uploaded_at, 'd MMM y', this.locale) : '';
      return `Uploaded ${r.filename ?? 'resume.pdf'} (${formatBytes(r.size_bytes)})${on}`;
    }
    if (r.source === 'server_file') return 'Using the file configured on the server';
    return 'No resume: the resume buttons are hidden on the site';
  }
  uploadResume(ev: Event, r: ResumeStatus) {
    const el = ev.target as HTMLInputElement;
    const file = el.files?.[0];
    el.value = '';
    if (!file) return;
    this.resumeErr.set('');
    this.resumeMsg.set('');
    if (r.max_bytes && file.size > r.max_bytes) {
      this.resumeErr.set(
        `This file is ${formatBytes(file.size)}; the most allowed is ${formatBytes(r.max_bytes)}.`,
      );
      return;
    }
    this.resumeProgress.set(0);
    this.api
      .uploadWithProgress<ResumeStatus>('resume', file)
      .pipe(
        tap((e) => {
          if (e.type === HttpEventType.UploadProgress && e.total)
            this.resumeProgress.set(Math.round((100 * e.loaded) / e.total));
        }),
        filter((e) => e.type === HttpEventType.Response),
        map((e) => (e as HttpResponse<ResumeStatus>).body!),
      )
      .subscribe({
        next: (s) => {
          this.resumeProgress.set(null);
          this.resume.set(s);
          this.resumeMsg.set('Resume saved. The resume buttons are shown on the site.');
        },
        error: (e) => {
          this.resumeProgress.set(null);
          this.resumeErr.set(errMsg(e));
        },
      });
  }
  downloadResume(r: ResumeStatus) {
    this.api.download('resume/file', r.download_name);
  }
  removeResume() {
    if (
      !confirm(
        'Remove the resume you added? Visitors can no longer request it unless the server has its own file.',
      )
    )
      return;
    this.resumeErr.set('');
    this.resumeMsg.set('');
    this.api.delete<ResumeStatus>('resume').subscribe({
      next: (s) => {
        this.resume.set(s);
        this.resumeMsg.set(
          s.source === 'server_file'
            ? 'Removed. The site now uses the file configured on the server.'
            : 'Removed. The resume buttons are hidden on the site.',
        );
      },
      error: (e) => this.resumeErr.set(errMsg(e)),
    });
  }
}
