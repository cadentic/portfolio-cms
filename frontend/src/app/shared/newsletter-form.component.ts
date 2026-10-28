import { Component, inject, input, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { RouterLink } from '@angular/router';
import { PublicApi, apiError } from '../core/public-api.service';

@Component({
  selector: 'app-newsletter-form',
  imports: [FormsModule, RouterLink],
  template: `
    <section class="newsletter" aria-labelledby="nl-title">
      <div>
        <div class="eyebrow" style="color:var(--dark-muted)">Newsletter</div>
        <h2 id="nl-title" style="font-size:clamp(1.8rem,3vw,2.6rem)">New articles and videos, straight to your inbox.</h2>
        <p>Practical Oracle DBA and database reliability notes, at most a few times a month. Confirm by email; unsubscribe in one click.</p>
      </div>
      @if (state() === 'done') {
        <div class="alert alert-ok" role="status">
          <strong>Check your inbox.</strong> {{ message() }}
          <div style="margin-top:10px">
            <button class="btn btn-sm btn-outline" type="button" (click)="resend()" [disabled]="busy()">Resend confirmation email</button>
          </div>
        </div>
      } @else {
        <form class="form" (ngSubmit)="submit()" #f="ngForm" novalidate [attr.aria-busy]="busy()">
          <div class="row">
            <div class="field">
              <label for="nl-email-{{ uid }}">Email address</label>
              <input id="nl-email-{{ uid }}" name="email" type="email" autocomplete="email" required [(ngModel)]="email"
                [attr.aria-invalid]="touched() && !validEmail()" [attr.aria-describedby]="'nl-msg-' + uid" />
            </div>
            <div class="field">
              <label for="nl-name-{{ uid }}">Name <span style="font-weight:400">(optional)</span></label>
              <input id="nl-name-{{ uid }}" name="name" autocomplete="given-name" maxlength="100" [(ngModel)]="name" />
            </div>
          </div>
          <div class="hp" aria-hidden="true"><label>Website <input name="website" tabindex="-1" autocomplete="off" [(ngModel)]="website" /></label></div>
          <label class="check">
            <input type="checkbox" name="consent" [(ngModel)]="consent" required />
            <span>{{ consentText() }} Read the <a routerLink="/privacy">privacy notice</a>.</span>
          </label>
          <button class="btn btn-primary" type="submit" [disabled]="busy()">
            {{ busy() ? 'Subscribing…' : 'Subscribe' }}
          </button>
          <p id="nl-msg-{{ uid }}" aria-live="polite" class="alert alert-err" [hidden]="!error()">{{ error() }}</p>
        </form>
      }
    </section>
  `,
})
export class NewsletterFormComponent {
  private api = inject(PublicApi);
  readonly source = input('website');
  readonly consentText = input('I agree to receive the newsletter by email and can unsubscribe at any time.');
  readonly uid = Math.random().toString(36).slice(2, 8);
  email = '';
  name = '';
  consent = false;
  website = '';
  readonly busy = signal(false);
  readonly error = signal('');
  readonly message = signal('');
  readonly state = signal<'form' | 'done'>('form');
  readonly touched = signal(false);

  validEmail() {
    return /^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/.test(this.email.trim());
  }

  submit() {
    this.touched.set(true);
    this.error.set('');
    if (!this.validEmail()) return this.error.set('Please enter a valid email address.');
    if (!this.consent) return this.error.set('Please tick the consent box to subscribe.');
    this.busy.set(true);
    this.api.subscribe({ email: this.email.trim(), name: this.name.trim(), consent: true, topics: [], source: this.source(), website: this.website })
      .subscribe({
        next: (r) => { this.message.set(r.message); this.state.set('done'); this.busy.set(false); },
        error: (e) => { this.error.set(apiError(e)); this.busy.set(false); },
      });
  }

  resend() {
    this.busy.set(true);
    this.api.resend(this.email.trim()).subscribe({
      next: (r) => { this.message.set(r.message); this.busy.set(false); },
      error: (e) => { this.message.set(apiError(e)); this.busy.set(false); },
    });
  }
}
