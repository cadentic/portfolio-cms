import { HttpClient } from '@angular/common/http';
import { Injectable, inject, signal } from '@angular/core';
import { FirebaseApp, initializeApp } from 'firebase/app';
import {
  Auth, GoogleAuthProvider, User, connectAuthEmulator, getAuth, onAuthStateChanged, signInWithEmailAndPassword,
  signInWithPopup, signOut,
} from 'firebase/auth';
import { firstValueFrom } from 'rxjs';
import { AdminToken } from '../core/admin-token';

interface RuntimeConfig {
  firebase: { apiKey: string; authDomain: string; projectId: string; appId?: string };
  authEmulatorUrl?: string;
}

export interface Me { uid: string; email: string; name?: string }

/** Firebase sign-in for the site owner. The API re-verifies every token and checks the allow-list. */
@Injectable({ providedIn: 'root' })
export class AuthService {
  private http = inject(HttpClient);
  private tokens = inject(AdminToken);
  private auth?: Auth;
  private ready?: Promise<void>;
  readonly user = signal<User | null>(null);
  readonly me = signal<Me | null>(null);
  readonly emulator = signal(false);
  /** False when the server has no Firebase web config (FIREBASE_API_KEY etc. unset). */
  readonly configured = signal(true);

  init(): Promise<void> {
    this.ready ??= (async () => {
      const cfg = await firstValueFrom(this.http.get<RuntimeConfig>('/app-config.json'));
      if (cfg.authEmulatorUrl && !cfg.firebase.apiKey) {
        // The Auth emulator accepts any key; the project id must match the API's FIREBASE_PROJECT_ID.
        cfg.firebase = { ...cfg.firebase, apiKey: 'emulator-key', projectId: cfg.firebase.projectId || 'demo-cadentic' };
      }
      if (!cfg.firebase.apiKey) {
        this.configured.set(false);
        return;
      }
      const app: FirebaseApp = initializeApp(cfg.firebase);
      this.auth = getAuth(app);
      if (cfg.authEmulatorUrl) {
        connectAuthEmulator(this.auth, cfg.authEmulatorUrl, { disableWarnings: true });
        this.emulator.set(true);
      }
      this.tokens.getter = async () => (this.auth?.currentUser ? this.auth.currentUser.getIdToken() : null);
      await new Promise<void>((resolve) => {
        onAuthStateChanged(this.auth!, (u) => {
          this.user.set(u);
          if (!u) this.me.set(null);
          resolve();
        });
      });
    })();
    return this.ready;
  }

  /** Confirms with the API that the signed-in account is the site owner. */
  async checkOwner(): Promise<'ok' | 'forbidden' | 'signed-out'> {
    await this.init();
    if (!this.user()) return 'signed-out';
    try {
      this.me.set(await firstValueFrom(this.http.get<Me>('/api/admin/me')));
      return 'ok';
    } catch (e: unknown) {
      return (e as { status?: number }).status === 403 ? 'forbidden' : 'signed-out';
    }
  }

  async google() {
    await this.init();
    await signInWithPopup(this.auth!, new GoogleAuthProvider());
  }

  async password(email: string, password: string) {
    await this.init();
    await signInWithEmailAndPassword(this.auth!, email, password);
  }

  async logout() {
    await this.init();
    await signOut(this.auth!);
    this.me.set(null);
  }
}
