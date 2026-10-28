import { Component, inject } from '@angular/core';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { PROFILE } from '../core/profile';
import { Seo } from '../core/seo.service';

const UPDATED = '28 October 2026';

/**
 * Privacy notice (Digital Personal Data Protection Act, 2023 and DPDP Rules,
 * 2025), terms of use and AI policy. Plain-language; review with a lawyer
 * before relying on them commercially.
 */
@Component({
  selector: 'app-legal',
  imports: [RouterLink],
  template: `
    <section class="page-hero">
      <div class="wrap" style="max-width:860px">
        <ol class="breadcrumbs"><li><a routerLink="/">Home</a></li><li aria-current="page">{{ heading() }}</li></ol>
        <h1>{{ heading() }}</h1>
        <p>Last updated {{ updated }}</p>
      </div>
    </section>
    <div class="wrap">
      <div class="legal">
        @switch (doc()) {
          @case ('privacy') {
            <p>This notice explains how {{ p.name }} ("I", "me"), as the Data Fiduciary for this website (cadentic.dev), collects and uses your personal data under India's Digital Personal Data Protection Act, 2023 (DPDP Act) and the DPDP Rules, 2025. I only process personal data for the specific purposes below, with your consent or for a legitimate use permitted by the Act.</p>
            <h2>What I collect and why</h2>
            <table>
              <thead><tr><th>Purpose</th><th>Data</th><th>Basis</th><th>Kept for</th></tr></thead>
              <tbody>
                <tr><td>Newsletter</td><td>Email, optional name, chosen topics, consent record (time, wording version, IP address, browser)</td><td>Your consent, confirmed by email (double opt-in)</td><td>Until you unsubscribe; unconfirmed sign-ups are deleted after 30 days</td></tr>
                <tr><td>Resume download</td><td>Email address and a one-time code</td><td>Consent, to send the code</td><td>30 days</td></tr>
                <tr><td>Contact form</td><td>Name, email, message</td><td>Consent, to reply to you</td><td>24 months, then deleted</td></tr>
                <tr><td>Email delivery records</td><td>Delivery status, bounces, complaints, unsubscribe requests</td><td>Legitimate use: honouring opt-outs and keeping mail deliverable</td><td>Message content 90 days; suppression records as long as needed to respect your opt-out</td></tr>
                <tr><td>Analytics (optional)</td><td>Pseudonymous usage data via Google Analytics</td><td>Consent through the cookie banner; off by default</td><td>14 months (Google Analytics setting)</td></tr>
                <tr><td>Security</td><td>IP address and request logs, rate-limit counters</td><td>Legitimate use: preventing abuse</td><td>Up to 30 days</td></tr>
              </tbody>
            </table>
            <p>I do not sell personal data, use it for advertising, or knowingly collect data from children. If you are under 18, please do not submit the forms.</p>
            <h2>Who processes data for me</h2>
            <p>I use Data Processors bound by contract: Brevo (Sendinblue SAS, EU) and Mailjet (Sinch, EU) to send email; Neon (PostgreSQL hosting) for the database; Google Cloud / Firebase or Microsoft Azure for hosting and owner sign-in; Cloudflare for DNS and network security; Google Analytics only if you allow it. Some processing happens outside India, which the DPDP Act permits except for countries the Government restricts.</p>
            <h2>Your rights</h2>
            <ul>
              <li><strong>Withdraw consent</strong> at any time: every newsletter has a one-click unsubscribe link, and you can change analytics consent from "Cookie settings" in the footer. Withdrawal is as easy as giving consent.</li>
              <li><strong>Access</strong> a summary of your personal data and how it was processed.</li>
              <li><strong>Correction, completion, updating and erasure</strong> of your data.</li>
              <li><strong>Grievance redressal</strong>: I will respond within 30 days at the latest.</li>
              <li><strong>Nominate</strong> another person to exercise your rights in case of death or incapacity.</li>
            </ul>
            <p>To exercise any right, use the <a routerLink="/" fragment="contact">contact form</a> with the subject "Data request". I may need to verify your identity. If you are not satisfied with my response, you can complain to the Data Protection Board of India.</p>
            <h2>Security and breaches</h2>
            <p>Data is encrypted in transit, access to the admin area requires authenticated sign-in, API keys are kept in a secret manager, and tokens are stored only as hashes. If a personal data breach affects you, I will inform you and the Data Protection Board as the Rules require.</p>
            <h2>Contact</h2>
            <p>Data Fiduciary and grievance contact: {{ p.name }}, Bangalore, India, via the <a routerLink="/" fragment="contact">contact form</a>.</p>
          }
          @case ('terms') {
            <p>By using cadentic.dev you agree to these terms. If you don't agree, please don't use the site.</p>
            <h2>Content</h2>
            <p>Articles, videos and code samples are shared for general information about database administration. They are provided "as is", without warranty. Test any command, script or configuration in a non-production environment and follow your organisation's change-management process before applying it. I am not liable for loss arising from use of the content.</p>
            <h2>Intellectual property</h2>
            <p>Unless stated otherwise, the text, images and videos on this site are © {{ p.name }}. You may quote short extracts with attribution and a link. Code snippets may be reused in your own work. Oracle, Oracle Database, RAC, Data Guard, GoldenGate and other product names are trademarks of their respective owners; no endorsement is implied. Organisation names shown in the career section describe past employment or engagements only.</p>
            <h2>Acceptable use</h2>
            <p>Don't attempt to disrupt the site, probe it for vulnerabilities without permission, scrape it at a rate that degrades service, or submit unlawful, abusive or misleading content through the forms. Automated requests are rate limited.</p>
            <h2>Newsletter and resume</h2>
            <p>The newsletter is free and you can leave at any time. The resume is shared for recruitment and professional enquiries only; please don't republish it.</p>
            <h2>Links</h2>
            <p>Links to third-party sites (YouTube, LinkedIn, GitHub and others) are provided for convenience; I'm not responsible for their content or privacy practices.</p>
            <h2>Governing law</h2>
            <p>These terms are governed by the laws of India, and the courts of Bengaluru, Karnataka have exclusive jurisdiction.</p>
            <h2>Changes</h2>
            <p>I may update these terms; the date above shows the latest version. See also the <a routerLink="/privacy">privacy notice</a> and <a routerLink="/ai-policy">AI policy</a>.</p>
          }
          @case ('ai-policy') {
            <p>This page explains how artificial intelligence is used on and around this site, and what I ask of AI systems that read it.</p>
            <h2>How I use AI</h2>
            <ul>
              <li><strong>Writing assistance.</strong> I sometimes use AI tools to draft, outline or proofread articles. Every published article is reviewed, tested and edited by me, and I am responsible for its accuracy.</li>
              <li><strong>Content management.</strong> I can create and update draft pages through an AI assistant connected to this site's CMS (Model Context Protocol). The connection requires my sign-in, can't send email to subscribers, and nothing is published or mailed without my approval.</li>
              <li><strong>No automated decisions about you.</strong> AI is not used to profile subscribers, make decisions about visitors, or process your personal data beyond the purposes in the <a routerLink="/privacy">privacy notice</a>.</li>
              <li><strong>Commands and code.</strong> Database commands in articles are tested by me; AI-generated snippets are labelled when they haven't been run against a real system.</li>
            </ul>
            <h2>AI crawlers and answer engines</h2>
            <p>Search and answer engines (including AI assistants) may read and cite public articles. A machine-readable summary is at <a href="/llms.txt">/llms.txt</a>, and permissions are in <a href="/robots.txt">/robots.txt</a>. When you quote or summarise content, please attribute "{{ p.name }}" and link to the original article. The admin area, APIs and personal data are excluded and must not be crawled.</p>
            <h2>Training</h2>
            <p>You may index and cite public articles. Please do not use the resume, contact details or any personal data on this site to train models or build profiles.</p>
            <h2>Questions</h2>
            <p>Questions or concerns about AI use: please get in touch through the <a routerLink="/" fragment="contact">contact form</a>.</p>
          }
        }
      </div>
    </div>
  `,
})
export class LegalComponent {
  readonly p = PROFILE;
  readonly updated = UPDATED;
  private seo = inject(Seo);
  private docValue = inject(ActivatedRoute).snapshot.data['doc'] as 'privacy' | 'terms' | 'ai-policy';
  readonly doc = () => this.docValue;
  readonly heading = () => ({ privacy: 'Privacy notice', terms: 'Terms of use', 'ai-policy': 'AI policy' })[this.docValue];

  constructor() {
    const desc = {
      privacy: 'How personal data is collected, used and protected on cadentic.dev under the DPDP Act 2023 and DPDP Rules 2025.',
      terms: 'Terms of use for cadentic.dev: content, intellectual property, acceptable use and governing law.',
      'ai-policy': 'How AI is used for writing and content management on cadentic.dev, and guidance for AI crawlers.',
    }[this.docValue];
    this.seo.set({ title: this.heading(), description: desc, path: '/' + this.docValue,
      jsonLd: [this.seo.breadcrumbs([{ name: 'Home', path: '/' }, { name: this.heading(), path: '/' + this.docValue }])] });
  }
}
