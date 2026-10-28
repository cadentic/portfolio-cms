import { Component, inject, signal } from '@angular/core';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { PublicApi, apiError } from '../core/public-api.service';
import { Seo } from '../core/seo.service';

/**
 * Double opt-in confirmation. The token is only consumed when the visitor
 * presses the button, so mail scanners that prefetch links can't confirm.
 */
@Component({
  selector: 'app-confirm',
  imports: [RouterLink],
  template: `
    <section class="center-card" aria-labelledby="c-title">
      <div class="eyebrow">Newsletter</div>
      @switch (state()) {
        @case ('ready') {
          <h1 id="c-title" style="font-size:2.2rem">Confirm your subscription</h1>
          <p>Press the button to confirm you want to receive the newsletter. You can unsubscribe at any time.</p>
          <button class="btn btn-primary" type="button" (click)="confirm()" [disabled]="busy()">{{ busy() ? 'Confirming…' : 'Yes, subscribe me' }}</button>
        }
        @case ('done') {
          <h1 id="c-title" style="font-size:2.2rem">You're subscribed 🎉</h1>
          <p>{{ already() ? 'This address was already confirmed.' : 'Thanks for confirming ' + email() + '. A welcome email is on its way.' }}</p>
          <a class="btn btn-primary" routerLink="/blog">Read the latest articles</a>
        }
        @case ('expired') {
          <h1 id="c-title" style="font-size:2.2rem">This link has expired</h1>
          <p>Confirmation links are valid for 48 hours. Subscribe again and we'll send a fresh link.</p>
          <a class="btn btn-primary" routerLink="/" fragment="contact">Subscribe again</a>
        }
        @default {
          <h1 id="c-title" style="font-size:2.2rem">This link isn't valid</h1>
          <p>{{ error() || 'It may have been used already or replaced by a newer email.' }}</p>
          <a class="btn btn-outline" routerLink="/">Back to the homepage</a>
        }
      }
    </section>
  `,
})
export class ConfirmComponent {
  private api = inject(PublicApi);
  private token = inject(ActivatedRoute).snapshot.queryParamMap.get('token') ?? '';
  readonly state = signal<'ready' | 'done' | 'expired' | 'invalid'>(this.token ? 'ready' : 'invalid');
  readonly busy = signal(false);
  readonly email = signal('');
  readonly already = signal(false);
  readonly error = signal('');

  constructor() {
    inject(Seo).set({ title: 'Confirm subscription', description: 'Confirm your newsletter subscription.', path: '/subscribe/confirm', noindex: true });
  }

  confirm() {
    this.busy.set(true);
    this.api.confirm(this.token).subscribe({
      next: (r) => { this.email.set(r.email); this.already.set(r.already_confirmed); this.state.set('done'); },
      error: (e) => { this.busy.set(false); this.error.set(apiError(e)); this.state.set(e?.status === 410 ? 'expired' : 'invalid'); },
    });
  }
}
