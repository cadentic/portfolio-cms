import { Component, inject, signal } from '@angular/core';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { PublicApi, apiError } from '../core/public-api.service';
import { Seo } from '../core/seo.service';

@Component({
  selector: 'app-unsubscribe',
  imports: [RouterLink],
  template: `
    <section class="center-card" aria-labelledby="u-title">
      <div class="eyebrow">Newsletter</div>
      @switch (state()) {
        @case ('loading') { <p aria-live="polite">Checking your link…</p> }
        @case ('ready') {
          <h1 id="u-title" style="font-size:2.2rem">Unsubscribe</h1>
          <p>Stop newsletter emails to <strong>{{ email() }}</strong>? You'll still receive replies to messages you send me.</p>
          <button class="btn btn-primary" type="button" (click)="go()" [disabled]="busy()">{{ busy() ? 'Unsubscribing…' : 'Unsubscribe' }}</button>
        }
        @case ('done') {
          <h1 id="u-title" style="font-size:2.2rem">You're unsubscribed</h1>
          <p>{{ message() }} If this was a mistake, you can subscribe again from the homepage.</p>
          <a class="btn btn-outline" routerLink="/">Back to the homepage</a>
        }
        @default {
          <h1 id="u-title" style="font-size:2.2rem">This link isn't valid</h1>
          <p>{{ message() || 'Please use the unsubscribe link from a recent newsletter, or contact me and I will remove you.' }}</p>
          <a class="btn btn-outline" routerLink="/" fragment="contact">Contact</a>
        }
      }
    </section>
  `,
})
export class UnsubscribeComponent {
  private api = inject(PublicApi);
  private token = inject(ActivatedRoute).snapshot.queryParamMap.get('token') ?? '';
  readonly state = signal<'loading' | 'ready' | 'done' | 'invalid'>('loading');
  readonly email = signal('');
  readonly message = signal('');
  readonly busy = signal(false);

  constructor() {
    inject(Seo).set({ title: 'Unsubscribe', description: 'Unsubscribe from the newsletter.', path: '/unsubscribe', noindex: true });
    if (!this.token) {
      this.state.set('invalid');
      return;
    }
    this.api.unsubscribeInfo(this.token).subscribe({
      next: (r) => {
        this.email.set(r.email);
        if (r.status === 'unsubscribed') { this.message.set('This address is already unsubscribed.'); this.state.set('done'); }
        else this.state.set('ready');
      },
      error: (e) => { this.message.set(apiError(e)); this.state.set('invalid'); },
    });
  }

  go() {
    this.busy.set(true);
    this.api.unsubscribe(this.token).subscribe({
      next: (r) => { this.message.set(r.message); this.state.set('done'); },
      error: (e) => { this.busy.set(false); this.message.set(apiError(e)); this.state.set('invalid'); },
    });
  }
}
