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
        <div class="field"><label for="c-name">Your name</label><input id="c-name" name="name" autocomplete="name" required maxlength="120" [(ngModel)]="name" [readonly]="step() === 'code'" /></div>
        <div class="field"><label for="c-email">Email address</label><input id="c-email" name="email" type="email" autocomplete="email" required [(ngModel)]="email" [readonly]="step() === 'code'" /></div>
        <div class="field"><label for="c-msg">How can I help?</label><textarea id="c-msg" name="message" required maxlength="5000" [(ngModel)]="message" [readonly]="step() === 'code'" placeholder="A little context about your enquiry…"></textarea></div>
        <div class="hp" aria-hidden="true"><label>Website <input name="website" tabindex="-1" autocomplete="off" [(ngModel)]="website" /></label></div>
        @if (step() === 'code') {
          <p role="status">We sent a 6-digit code to <strong>{{ email }}</strong>. Enter it to verify your email and send the message. It expires in 10 minutes.</p>
          <div class="field"><label for="c-code">Verification code</label><input id="c-code" name="code" inputmode="numeric" autocomplete="one-time-code" maxlength="6" pattern="[0-9]{6}" required [(ngModel)]="code" /></div>
          <button class="btn btn-primary" type="submit" [disabled]="busy()">{{ busy() ? 'Sending…' : 'Verify and send ↗' }}</button>
          <button class="btn btn-outline" type="button" (click)="edit()" [disabled]="busy()">Edit message or email</button>
          <button class="btn btn-outline" type="button" (click)="requestCode()" [disabled]="busy()">Send a new code</button>
        } @else {
          <button class="btn btn-primary" type="submit" [disabled]="busy()">{{ busy() ? 'Sending code…' : 'Verify email to send ↗' }}</button>
        }
        <small>We email you a one-time code to confirm the address is yours. Your details are used only to reply to you. See the privacy notice.</small>
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
  code = '';
  website = '';
  readonly step = signal<'details' | 'code'>('details');
  readonly busy = signal(false);
  readonly sent = signal(false);
  readonly error = signal('');

  submit() {
    if (this.step() === 'code') return this.send();
    this.error.set('');
    if (!this.name.trim() || !this.email.includes('@') || this.message.trim().length < 10) {
      return this.error.set('Please fill in your name, a valid email and a message of at least 10 characters.');
    }
    if (this.website) return this.sent.set(true);
    this.requestCode();
  }

  requestCode() {
    this.error.set('');
    this.busy.set(true);
    this.api.contactCode(this.email.trim()).subscribe({
      next: () => { this.busy.set(false); this.code = ''; this.step.set('code'); },
      error: (e) => { this.busy.set(false); this.error.set(apiError(e)); },
    });
  }

  edit() {
    this.error.set('');
    this.step.set('details');
  }

  private send() {
    this.error.set('');
    if (!/^[0-9]{6}$/.test(this.code.trim())) return this.error.set('Enter the 6-digit code from the email.');
    this.busy.set(true);
    this.api.contact({ name: this.name, email: this.email, message: this.message, code: this.code.trim(), website: this.website }).subscribe({
      next: () => { this.busy.set(false); this.sent.set(true); },
      error: (e) => { this.busy.set(false); this.error.set(apiError(e)); },
    });
  }
}
