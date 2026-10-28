import { Component, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { Meta, Title } from '@angular/platform-browser';
import { ActivatedRoute, Router } from '@angular/router';
import { AuthService } from './auth.service';

@Component({
  selector: 'app-admin-login',
  imports: [FormsModule],
  template: `
    <div class="admin a-login" style="display:grid;grid-template-columns:1fr">
      <main class="a-card" aria-labelledby="login-title">
        <h1 id="login-title">Sign in</h1>
        <p>Portfolio CMS owner access. Only allow-listed accounts can sign in.</p>
        @if (denied()) { <p class="a-err" role="alert">This account isn't authorised to manage the site.</p> }
        @if (error()) { <p class="a-err" role="alert">{{ error() }}</p> }
        @if (!auth.configured()) { <p class="a-warn" role="status">Sign-in isn't configured yet. Set FIREBASE_API_KEY, FIREBASE_AUTH_DOMAIN and FIREBASE_PROJECT_ID for the web service (or FIREBASE_AUTH_EMULATOR_URL locally).</p> }
        <button class="ab primary" style="width:100%;justify-content:center;margin:12px 0" type="button" (click)="google()" [disabled]="busy() || !auth.configured()">Continue with Google</button>
        <details [open]="auth.emulator()">
          <summary class="a-muted" style="cursor:pointer">Sign in with email and password</summary>
          <form class="a-form" style="margin-top:10px" (ngSubmit)="password()">
            <label class="f">Email<input name="email" type="email" autocomplete="username" [(ngModel)]="email" required /></label>
            <label class="f">Password<input name="password" type="password" autocomplete="current-password" [(ngModel)]="pass" required /></label>
            <button class="ab" type="submit" [disabled]="busy() || !auth.configured()">Sign in</button>
          </form>
        </details>
        @if (auth.emulator()) { <p class="a-warn" style="margin-top:12px">Local mode: signing in through the Firebase Auth emulator.</p> }
      </main>
    </div>
  `,
})
export class LoginComponent {
  readonly auth = inject(AuthService);
  private router = inject(Router);
  private route = inject(ActivatedRoute);
  readonly busy = signal(false);
  readonly error = signal('');
  readonly denied = signal(this.route.snapshot.queryParamMap.get('denied') === '1');
  email = '';
  pass = '';

  constructor() {
    inject(Title).setTitle('Sign in · Portfolio CMS');
    inject(Meta).updateTag({ name: 'robots', content: 'noindex, nofollow' });
    this.auth.init();
  }

  async google() {
    await this.run(() => this.auth.google());
  }
  async password() {
    await this.run(() => this.auth.password(this.email, this.pass));
  }

  private async run(fn: () => Promise<void>) {
    this.busy.set(true);
    this.error.set('');
    this.denied.set(false);
    try {
      await fn();
      const r = await this.auth.checkOwner();
      if (r === 'ok') {
        const next = this.route.snapshot.queryParamMap.get('next') ?? '/admin';
        this.router.navigateByUrl(next.startsWith('/admin') ? next : '/admin');
      } else {
        this.denied.set(r === 'forbidden');
        if (r === 'forbidden') await this.auth.logout();
      }
    } catch (e) {
      this.error.set((e as Error).message?.replace('Firebase: ', '') ?? 'Sign-in failed.');
    } finally {
      this.busy.set(false);
    }
  }
}
