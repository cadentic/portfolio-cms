import { Component, inject, signal } from '@angular/core';
import { RouterLink, RouterLinkActive, RouterOutlet } from '@angular/router';
import { AdminApi } from '../admin-api.service';

@Component({
  selector: 'app-email-shell',
  imports: [RouterOutlet, RouterLink, RouterLinkActive],
  template: `
    <h1>Subscribers &amp; Email</h1>
    <p>Manage subscribers, create campaigns and configure email providers.</p>
    <nav class="a-tabs" aria-label="Email sections">
      <a routerLink="subscribers" routerLinkActive="active">Subscribers</a>
      @if (googleContacts()) { <a routerLink="google-contacts" routerLinkActive="active">Google Contacts</a> }
      <a routerLink="campaigns" routerLinkActive="active">Campaigns</a>
      <a routerLink="templates" routerLinkActive="active">Templates</a>
      <a routerLink="providers" routerLinkActive="active">Providers</a>
      <a routerLink="monitor" routerLinkActive="active">Delivery monitor</a>
      <a routerLink="suppressions" routerLinkActive="active">Suppressions</a>
      <a routerLink="settings" routerLinkActive="active">Settings</a>
    </nav>
    <router-outlet />
  `,
})
export class EmailShellComponent {
  /** The Google Contacts tab shows only when the server has the feature on (GOOGLE_CONTACTS_ENABLED). */
  readonly googleContacts = signal(false);

  constructor() {
    inject(AdminApi).get<{ enabled: boolean }>('google-contacts').subscribe({ next: (s) => this.googleContacts.set(s.enabled), error: () => {} });
  }
}
