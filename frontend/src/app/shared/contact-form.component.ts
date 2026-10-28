import { Component, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { PublicApi, apiError } from '../core/public-api.service';

@Component({
  selector: 'app-contact-form',
  imports: [FormsModule],
  template: `
    @if (sent()) {
      <p class="alert alert-ok" role="status">Thanks, your message is on its way. You'll get an acknowledgement by email.</p>
    } @else {
      <form class="form" (ngSubmit)="submit()" [attr.aria-busy]="busy()">
        <div class="field"><label for="c-name">Your name</label><input id="c-name" name="name" autocomplete="name" required maxlength="120" [(ngModel)]="name" /></div>
        <div class="field"><label for="c-email">Email address</label><input id="c-email" name="email" type="email" autocomplete="email" required [(ngModel)]="email" /></div>
        <div class="field"><label for="c-msg">How can I help?</label><textarea id="c-msg" name="message" required maxlength="5000" [(ngModel)]="message" placeholder="A little context about your enquiry…"></textarea></div>
        <div class="hp" aria-hidden="true"><label>Website <input name="website" tabindex="-1" autocomplete="off" [(ngModel)]="website" /></label></div>
        <button class="btn btn-primary" type="submit" [disabled]="busy()">{{ busy() ? 'Sending…' : 'Send enquiry ↗' }}</button>
        <small>Your details are used only to reply to you. See the privacy notice.</small>
        <p class="alert alert-err" role="alert" [hidden]="!error()">{{ error() }}</p>
      </form>
    }
  `,
})
export class ContactFormComponent {
  private api = inject(PublicApi);
  name = '';
  email = '';
  message = '';
  website = '';
  readonly busy = signal(false);
  readonly sent = signal(false);
  readonly error = signal('');

  submit() {
    this.error.set('');
    if (!this.name.trim() || !this.email.includes('@') || this.message.trim().length < 5) {
      return this.error.set('Please fill in your name, a valid email and a short message.');
    }
    this.busy.set(true);
    this.api.contact({ name: this.name, email: this.email, message: this.message, website: this.website }).subscribe({
      next: () => { this.busy.set(false); this.sent.set(true); },
      error: (e) => { this.busy.set(false); this.error.set(apiError(e)); },
    });
  }
}
