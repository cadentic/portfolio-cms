import { Component, inject, input } from '@angular/core';
import { Brand } from '../core/brand';
import { Block } from '../core/models';
import { inlineMarkdown } from './inline';

@Component({
  selector: 'app-blocks',
  template: `
    @for (b of blocks(); track $index) {
      @switch (b.type) {
        @case ('heading') {
          @if (b.level === 3) { <h3 [innerHTML]="md(b.text)"></h3> }
          @else if (b.level === 4) { <h4 [innerHTML]="md(b.text)"></h4> }
          @else { <h2 [innerHTML]="md(b.text)"></h2> }
        }
        @case ('paragraph') { <p [innerHTML]="md(b.text)"></p> }
        @case ('list') {
          @if (b.style === 'numbered') {
            <ol>@for (it of b.items ?? []; track $index) { <li [innerHTML]="md(it)"></li> }</ol>
          } @else {
            <ul>@for (it of b.items ?? []; track $index) { <li [innerHTML]="md(it)"></li> }</ul>
          }
        }
        @case ('quote') {
          <blockquote><p [innerHTML]="md(b.text)"></p>@if (b.cite) { <cite>{{ b.cite }}</cite> }</blockquote>
        }
        @case ('code') { <pre><code [attr.data-lang]="b.language || null">{{ b.text }}</code></pre> }
        @case ('callout') { <div class="callout" [class]="'callout ' + (b.tone || 'info')" role="note"><p [innerHTML]="md(b.text)"></p></div> }
        @case ('html') { <div [innerHTML]="brand.html(b.html)"></div> }
        @case ('divider') { <hr /> }
      }
    }
  `,
})
export class BlocksComponent {
  readonly blocks = input<Block[]>([]);
  protected brand = inject(Brand);
  md(t?: string) {
    return inlineMarkdown(t ?? '');
  }
}
