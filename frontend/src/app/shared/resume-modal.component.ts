import { DOCUMENT } from '@angular/common';
import { Component, inject, output, AfterViewInit, OnDestroy } from '@angular/core';
import { ResumeAccessComponent } from './resume-access.component';

@Component({
  selector: 'app-resume-modal',
  imports: [ResumeAccessComponent],
  host: { '(document:keydown.escape)': 'close.emit()' },
  template: `
    <div class="modal-backdrop" (click)="backdrop($event)">
      <div class="modal" role="dialog" aria-modal="true" aria-labelledby="resume-title">
        <button class="modal-close" type="button" (click)="close.emit()" aria-label="Close dialog">×</button>
        <div class="eyebrow">Resume access</div>
        <h2 id="resume-title" style="font-size:2rem;margin-top:14px">Verify your email</h2>
        <app-resume-access idPrefix="otp" [autofocus]="true" />
      </div>
    </div>
  `,
})
export class ResumeModalComponent implements AfterViewInit, OnDestroy {
  private doc = inject(DOCUMENT);
  readonly close = output<void>();
  private previous: Element | null = null;

  ngAfterViewInit() {
    this.previous = this.doc.activeElement;
  }
  ngOnDestroy() {
    (this.previous as HTMLElement | null)?.focus?.();
  }

  backdrop(e: MouseEvent) {
    if (e.target === e.currentTarget) this.close.emit();
  }
}
