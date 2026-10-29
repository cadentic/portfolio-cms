import { isPlatformBrowser } from '@angular/common';
import { ApplicationRef, DestroyRef, Directive, ElementRef, PLATFORM_ID, inject } from '@angular/core';

// Elements that fade/slide in as they scroll into view. Matched by class so page
// templates need no changes: anything rendered inside the host picks this up,
// including content that arrives later from the API or after navigation.
const REVEAL = [
  '.section .eyebrow', '.section h2', '.section-head p', '.about-stamp', '.about-copy > p',
  '.chips', '.timeline-item', '.project-card', '.client-head', '.client-logo', '.blog-feature',
  '.contact-card', '.video-grid > *', '.newsletter', '.article-card',
].join(',');
// Groups whose children reveal one after another rather than all at once.
const STAGGER = '.project-grid,.client-logos,.timeline,.chips,.article-grid';
const COUNT = '.metric strong';

/**
 * Scroll animations for the public site. Server-rendered HTML is never hidden:
 * elements are only prepared for animation in the browser, and anything already
 * on screen at that moment is left alone so nothing flashes. Honours
 * prefers-reduced-motion by doing nothing at all.
 */
@Directive({ selector: '[appScrollReveal]' })
export class ScrollRevealDirective {
  constructor() {
    if (!isPlatformBrowser(inject(PLATFORM_ID))) return;
    const host = inject<ElementRef<HTMLElement>>(ElementRef).nativeElement;
    const win = host.ownerDocument.defaultView;
    if (!win || !('IntersectionObserver' in win) || win.matchMedia('(prefers-reduced-motion: reduce)').matches) return;

    const io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (!e.isIntersecting) continue;
          io.unobserve(e.target);
          const el = e.target as HTMLElement;
          if (el.matches(COUNT)) countUp(el);
          else show(el);
        }
      },
      { rootMargin: '0px 0px -8% 0px', threshold: 0.12 },
    );

    const seen = new WeakSet<Element>();
    // Once revealed, drop the classes so hover transitions are not delayed by the stagger.
    const show = (el: HTMLElement) => {
      el.classList.add('is-visible');
      win.setTimeout(() => {
        el.classList.remove('reveal', 'is-visible');
        el.style.removeProperty('--reveal-delay');
      }, 1400);
    };
    const inView = (el: Element) => {
      const r = el.getBoundingClientRect();
      return r.top < win.innerHeight && r.bottom > 0;
    };
    const scan = () => {
      host.querySelectorAll(REVEAL).forEach((el) => {
        if (seen.has(el)) return;
        seen.add(el);
        if (inView(el)) return;
        const group = el.parentElement?.closest(STAGGER);
        if (group && el.parentElement === group) {
          const i = Array.prototype.indexOf.call(group.children, el);
          (el as HTMLElement).style.setProperty('--reveal-delay', `${Math.min(i, 8) * 70}ms`);
        }
        el.classList.add('reveal');
        io.observe(el);
      });
      host.querySelectorAll(COUNT).forEach((el) => {
        if (seen.has(el)) return;
        seen.add(el);
        io.observe(el);
      });
    };

    let queued = false;
    const mo = new MutationObserver(() => {
      if (queued) return;
      queued = true;
      win.requestAnimationFrame(() => {
        queued = false;
        scan();
      });
    });
    // Start once hydration has settled: Angular rewrites class attributes while it
    // renders, which would wipe the classes added here.
    const appRef = inject(ApplicationRef);
    let started = false;
    const start = () => {
      if (started) return;
      started = true;
      mo.observe(host, { childList: true, subtree: true });
      win.requestAnimationFrame(scan);
    };
    appRef.whenStable().then(start);
    win.setTimeout(start, 2000);

    inject(DestroyRef).onDestroy(() => {
      started = true;
      mo.disconnect();
      io.disconnect();
    });
  }
}

/**
 * Counts the leading number of a metric up from zero ("11+ years" → 0…11).
 * Writes to the existing text node so Angular's binding keeps working, and
 * stops if Angular changes the text mid-animation.
 */
function countUp(el: HTMLElement) {
  const node = el.firstChild;
  if (!node || node.nodeType !== Node.TEXT_NODE || node !== el.lastChild) return;
  const text = node.nodeValue ?? '';
  const m = /^(\D*)(\d+)(.*)$/s.exec(text);
  if (!m) return;
  const [, pre, num, post] = m;
  const target = Number(num);
  if (!target) return;
  const win = el.ownerDocument.defaultView!;
  const duration = 1100;
  let start = 0;
  let last = text;
  const step = (t: number) => {
    if (node.nodeValue !== last) return; // content was updated; leave it be
    if (!start) start = t;
    const p = Math.min((t - start) / duration, 1);
    const eased = 1 - Math.pow(1 - p, 3);
    last = p < 1 ? `${pre}${Math.round(target * eased)}${post}` : text;
    node.nodeValue = last;
    if (p < 1) win.requestAnimationFrame(step);
  };
  last = `${pre}0${post}`;
  node.nodeValue = last;
  win.requestAnimationFrame(step);
}
