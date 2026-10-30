import { Component, inject } from '@angular/core';
import { Seo } from '../core/seo.service';
import { ResumeAccessComponent } from '../shared/resume-access.component';

/** Shareable resume download page (linked from emails); same email-code check as the modal. */
@Component({
  selector: 'app-resume-page',
  imports: [ResumeAccessComponent],
  template: `
    <section class="center-card" aria-labelledby="resume-page-title">
      <div class="eyebrow">Resume</div>
      <h1 id="resume-page-title" style="font-size:2.2rem">Download my resume</h1>
      <app-resume-access idPrefix="resume-page" />
    </section>
  `,
})
export class ResumePageComponent {
  constructor() {
    inject(Seo).set({ title: 'Resume', description: 'Download the resume after a quick email verification.', path: '/resume' });
  }
}
