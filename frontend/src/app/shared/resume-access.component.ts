import { DOCUMENT } from '@angular/common';
import { AfterViewInit, Component, ElementRef, inject, input, signal, viewChild } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { PublicApi, apiError } from '../core/public-api.service';

/** Email one-time-code check that unlocks the resume download (used by the modal and the /resume page). */
@Component({
  selector: 'app-resume-access',
  imports: [FormsModule],
  template: `
    @if (step() === 'email') {
      <p>Enter your email address and I'll send a one-time code. The resume downloads once the code is verified.</p>
      <form (ngSubmit)="request()">
        <div class="field">
          <label [for]="idPrefix() + '-email'">Email address</label>
          <input #first [id]="idPrefix() + '-email'" name="email" type="email" autocomplete="email" required [(ngModel)]="email" />
        </div>
        <button class="btn btn-primary" style="width:100%" type="submit" [disabled]="busy()">{{ busy() ? 'Sending…' : 'Send verification code' }}</button>
      </form>
    } @else if (step() === 'code') {
      <p>We sent a 6-digit code to <strong>{{ email }}</strong>. It expires in 10 minutes.</p>
      <form (ngSubmit)="verify()">
        <div class="field">
          <label [for]="idPrefix() + '-code'">One-time code</label>
          <input [id]="idPrefix() + '-code'" name="code" inputmode="numeric" autocomplete="one-time-code" maxlength="6" pattern="[0-9]{6}" required [(ngModel)]="code" />
        </div>
        <button class="btn btn-primary" style="width:100%" type="submit" [disabled]="busy()">{{ busy() ? 'Verifying…' : 'Verify and download' }}</button>
        <button class="btn btn-outline" style="width:100%;margin-top:8px" type="button" (click)="step.set('email')">Use a different email</button>
      </form>
    } @else {
      <p class="alert alert-ok" role="status">Verified. Your download should start now. If it doesn't, <a [href]="downloadUrl()" style="text-decoration:underline">download the resume</a>.</p>
    }
    <p class="alert alert-err" role="alert" [hidden]="!error()" style="margin-top:12px">{{ error() }}</p>
    <p style="font-size:.78rem;margin-top:14px">Your email is used only to send this code and is deleted after 30 days. See the privacy notice.</p>
  `,
})
export class ResumeAccessComponent implements AfterViewInit {
  private api = inject(PublicApi);
  private doc = inject(DOCUMENT);
  /** Keeps element ids unique when the modal and the page are both on screen. */
  readonly idPrefix = input('otp');
  readonly autofocus = input(false);
  readonly first = viewChild<ElementRef<HTMLInputElement>>('first');
  readonly step = signal<'email' | 'code' | 'done'>('email');
  readonly busy = signal(false);
  readonly error = signal('');
  readonly downloadUrl = signal('');
  email = '';
  code = '';

  ngAfterViewInit() {
    if (this.autofocus()) this.first()?.nativeElement.focus();
  }

  request() {
    this.error.set('');
    this.busy.set(true);
    this.api.resumeRequest(this.email.trim()).subscribe({
      next: () => { this.busy.set(false); this.step.set('code'); },
      error: (e) => { this.busy.set(false); this.error.set(apiError(e)); },
    });
  }

  verify() {
    this.error.set('');
    if (!/^\d{6}$/.test(this.code)) return this.error.set('Enter the 6-digit code from the email.');
    this.busy.set(true);
    this.api.resumeVerify(this.email.trim(), this.code).subscribe({
      next: (r) => {
        this.busy.set(false);
        this.downloadUrl.set(r.download_url);
        this.step.set('done');
        this.doc.location.assign(r.download_url);
      },
      error: (e) => { this.busy.set(false); this.error.set(apiError(e)); },
    });
  }
}
