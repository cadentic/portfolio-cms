import {
  EarlierRole,
  Education,
  ExpertiseItem,
  Link,
  Metric,
  Organization,
  QA,
  Role,
  SECTION_KEYS,
  SectionKey,
  SiteContent,
  Video,
  WorkItem,
} from '../../core/site-content';

// ---- Admin envelopes (§2.1) ----

export interface FieldError {
  path: string; // e.g. "organizations.items[2].logo_url"; "" = whole document
  message: string;
}
export interface Limit {
  max: number;
  min?: number;
  item?: number;
  required?: boolean;
}
// Limit keys use "[]" for list elements: "hero.metrics" {max:6}, "hero.metrics[].value" {max:24, required:true},
// "about.skills" {max:40, item:40}, "identity.experience_years" {min:1, max:60, required:true}.
// For a number field, min/max bound the value; for a list, max counts items; for text, max counts characters.
// required means "required when it applies" (see R above). It is a hint: the server decides.
export interface SiteContentAdminDoc {
  content: SiteContent; // raw stored document: {years} kept, missing media kept (reported in warnings)
  defaults: SiteContent; // raw Default()
  limits: Record<string, Limit>;
  updated_at: string | null; // opaque token: send back verbatim; null = never saved
  updated_by: string | null;
  warnings: FieldError[]; // e.g. an image no longer in the media library; never null
}
export interface SiteContentPut {
  content: Partial<Record<SectionKey, object>>;
  base_updated_at?: string | null;
}
export interface SiteContentReset {
  sections?: SectionKey[];
  all?: boolean;
  base_updated_at?: string | null;
}
export interface VersionRestore {
  base_updated_at?: string | null;
}
export interface ContentVersion {
  id: number;
  action: 'seed' | 'update' | 'reset' | 'restore';
  actor: string;
  changed: string[]; // section keys (site content) or column names (pages)
  sha256: string;
  created_at: string;
}
export interface PageVersionDetail extends ContentVersion {
  snapshot: { title: string; excerpt: string; hero: object; blocks: object[]; seo: object };
}
export interface ResumeStatus {
  source: 'upload' | 'server_file' | 'none';
  filename: string | null;
  size_bytes: number | null;
  uploaded_at: string | null;
  uploaded_by: string | null;
  download_name: string; // e.g. "Sayantan-Chakraborty-Resume.pdf"
  max_bytes: number;
}
export interface ApiError {
  error: string; // always present, human readable
  errors?: FieldError[]; // 400 from site content
  updated_at?: string | null; // 409 from site content
  updated_by?: string | null;
  pages?: string[]; // 409 from media delete
  settings?: string[];
  email?: string[];
}

// ---- Editor groups ----

export type SiteArea = 'home' | 'profile' | 'videos';

export const AREA_TITLES: Record<SiteArea, string> = {
  home: 'Home page',
  profile: 'Site profile',
  videos: 'Videos',
};

export interface Group {
  area: SiteArea;
  id: string; // DOM id "sec-<id>"; used by e2e and warnings
  title: string;
  fields: string[]; // a whole section ("hero") or one field ("identity.experience_years")
  toggle?: SectionKey; // the section whose `visible` checkbox this group shows
}

export const GROUPS: Group[] = [
  {
    area: 'home',
    id: 'hero',
    title: 'Hero & key numbers',
    fields: ['identity.experience_years', 'hero'],
  },
  { area: 'home', id: 'about', title: 'About & skills', fields: ['about'], toggle: 'about' },
  {
    area: 'home',
    id: 'experience',
    title: 'Experience',
    fields: ['experience'],
    toggle: 'experience',
  },
  { area: 'home', id: 'work', title: 'Selected work', fields: ['work'], toggle: 'work' },
  {
    area: 'home',
    id: 'organizations',
    title: 'Organisations (employers & clients)',
    fields: ['organizations'],
    toggle: 'organizations',
  },
  { area: 'home', id: 'expertise', title: 'Expertise', fields: ['expertise'], toggle: 'expertise' },
  {
    area: 'home',
    id: 'videos-band',
    title: 'Videos band & featured video',
    fields: [
      'videos.visible',
      'videos.label',
      'videos.heading',
      'videos.intro',
      'videos.channel_cta_label',
      'videos.featured',
    ],
    toggle: 'videos',
  },
  {
    area: 'home',
    id: 'blog-band',
    title: 'Notes & insights band',
    fields: ['blog.visible', 'blog.label', 'blog.heading', 'blog.intro', 'blog.cta_label'],
    toggle: 'blog',
  },
  {
    area: 'home',
    id: 'newsletter',
    title: 'Newsletter block',
    fields: ['newsletter'],
    toggle: 'newsletter',
  },
  { area: 'home', id: 'faq', title: 'FAQ', fields: ['faq'], toggle: 'faq' },
  { area: 'home', id: 'contact', title: 'Contact', fields: ['contact'] },
  {
    area: 'home',
    id: 'home-seo',
    title: 'Search engines',
    fields: ['seo.home_title', 'seo.home_description'],
  },
  {
    area: 'profile',
    id: 'identity',
    title: 'Name, role & photo',
    fields: [
      'identity.name',
      'identity.initials',
      'identity.short_role',
      'identity.location',
      'identity.city',
      'identity.country_code',
      'identity.photo_url',
      'identity.summary',
    ],
  },
  {
    area: 'profile',
    id: 'links',
    title: 'Links & YouTube handle',
    fields: [
      'identity.linkedin_url',
      'identity.github_url',
      'identity.youtube_url',
      'identity.youtube_handle',
      'identity.other_links',
    ],
  },
  { area: 'profile', id: 'navigation', title: 'Header, menu & footer', fields: ['navigation'] },
  {
    area: 'profile',
    id: 'education',
    title: 'Education & languages',
    fields: ['identity.education', 'identity.languages'],
  },
  // The resume has its own API; it is not part of this document.
  { area: 'profile', id: 'resume', title: 'Resume (PDF)', fields: [] },
  {
    area: 'profile',
    id: 'blog-page',
    title: 'Blog page',
    fields: [
      'blog.page_heading',
      'blog.page_intro',
      'blog.empty_text',
      'seo.blog_title',
      'seo.blog_description',
    ],
  },
  {
    area: 'profile',
    id: 'llms',
    title: 'AI answer engines (llms.txt)',
    fields: ['seo.llms_summary'],
  },
  {
    area: 'videos',
    id: 'video-list',
    title: 'Video list',
    fields: ['videos.items', 'videos.include_article_videos'],
  },
  {
    area: 'videos',
    id: 'videos-page',
    title: 'Videos page text',
    fields: [
      'videos.page_heading',
      'videos.page_intro',
      'videos.page_cta_label',
      'videos.empty_text',
      'videos.placeholder_title',
      'videos.placeholder_text',
    ],
  },
  {
    area: 'videos',
    id: 'videos-seo',
    title: 'Search engines',
    fields: ['seo.videos_title', 'seo.videos_description'],
  },
];

/** The group that owns a path such as "organizations.items[2].logo_url" (indices ignored). */
export function groupOf(path: string): Group | undefined {
  const bare = path.replace(/\[\d+\]/g, '');
  return GROUPS.find((g) => g.fields.some((f) => bare === f || bare.startsWith(f + '.')));
}

const NAMES: Record<string, string> = {
  q: 'Question',
  a: 'Answer',
  url: 'Link',
  experience_years: 'Years of experience',
  photo_url: 'Photo',
  logo_url: 'Logo',
  linkedin_url: 'LinkedIn',
  github_url: 'GitHub',
  youtube_url: 'YouTube channel',
  youtube_handle: 'YouTube handle',
  youtube_id: 'YouTube video',
  llms_summary: 'llms.txt summary',
  featured: 'Featured video',
};
// List keys, named per row: "Metric 1", "Item 3".
const ROW_NAMES: Record<string, string> = {
  other_links: 'Link',
  education: 'Education',
  languages: 'Language',
  metrics: 'Metric',
  stamp_lines: 'Stamp line',
  paragraphs: 'Paragraph',
  skills: 'Skill',
  roles: 'Role',
  points: 'Point',
  earlier_roles: 'Earlier role',
  items: 'Item',
  chips: 'Chip',
};
const humanize = (k: string) =>
  NAMES[k] ?? k.charAt(0).toUpperCase() + k.slice(1).replace(/_/g, ' ');

/** "hero.metrics[0].value" → "Home page › Hero & key numbers › Metric 1 › Value". */
export function fieldLabel(path: string): string {
  const keys = keysOf(path);
  const parts: string[] = [];
  for (let i = 1; i < keys.length; i++) {
    const k = keys[i];
    if (typeof k === 'number') continue;
    const n = keys[i + 1];
    parts.push(typeof n === 'number' ? `${ROW_NAMES[k] ?? humanize(k)} ${n + 1}` : humanize(k));
  }
  const g = groupOf(path);
  const head = g ? [AREA_TITLES[g.area], g.title] : keys.length ? [humanize(String(keys[0]))] : [];
  return [...head, ...parts].join(' › ');
}

// ---- Paths ----

type Key = string | number;
type Node = Record<Key, unknown>;

function keysOf(path: string): Key[] {
  return (path.match(/[^.[\]]+/g) ?? []).map((k) => (/^\d+$/.test(k) ? Number(k) : k));
}

function isNode(v: unknown): v is Node {
  return typeof v === 'object' && v !== null;
}

function parentOf(root: object, keys: Key[]): Node | undefined {
  let n: unknown = root;
  for (const k of keys.slice(0, -1)) {
    if (!isNode(n)) return undefined;
    n = n[k];
  }
  return isNode(n) ? n : undefined;
}

/** Copies group fields from `source` (usually defaults) into `target`, deep-cloned. */
export function copyFields(target: SiteContent, source: SiteContent, fields: string[]): void {
  for (const f of fields) {
    const keys = keysOf(f);
    const to = parentOf(target, keys);
    const from = parentOf(source, keys);
    const last = keys[keys.length - 1];
    if (to && from && last !== undefined) to[last] = structuredClone(from[last]);
  }
}

/** The value at a path such as "hero.metrics[0].value", or undefined. */
export function valueAt(root: object, path: string): unknown {
  const keys = keysOf(path);
  return parentOf(root, keys)?.[keys[keys.length - 1]];
}

/** Sets the value at a path whose parent exists; returns false when it doesn't. */
export function setValueAt(root: object, path: string, value: unknown): boolean {
  const keys = keysOf(path);
  const parent = parentOf(root, keys);
  if (!parent || !keys.length) return false;
  parent[keys[keys.length - 1]] = value;
  return true;
}

// ---- Limits and {years} ----

/** Limit for a path, "[n]" read as "[]": "hero.metrics[0].value" → "hero.metrics[].value". */
export function lim(
  limits: Record<string, Limit> | null | undefined,
  path: string,
): Limit | undefined {
  return limits?.[path.replace(/\[\d+\]/g, '[]')];
}

/** What a text field shows on the public site. */
export function expandPreview(text: string, years: number | null | undefined): string {
  if (!text || years === null || years === undefined || Number.isNaN(years)) return text ?? '';
  return text.split('{years}').join(String(years));
}

// ---- Find & replace ----

export interface FindOptions {
  wholeWord: boolean;
  caseSensitive: boolean;
}

export interface Match {
  path: string; // e.g. "about.skills[3]"
  label: string; // fieldLabel(path)
  start: number; // offset in the field's text
  text: string; // the matched text, as found
  before: string; // "…" + up to CONTEXT characters before the match
  after: string; // up to CONTEXT characters after the match + "…"
  context: string; // before + text + after
}

const CONTEXT = 30;
// Links, IDs and dates are never text to replace.
const skipKey = (k: Key) =>
  typeof k === 'string' &&
  (k === 'url' ||
    k.endsWith('_url') ||
    k === 'youtube_id' ||
    k === 'youtube_handle' ||
    k === 'published_on');
const wordChar = /[\p{L}\p{N}_]/u;
const isWord = (ch: string | undefined) => ch !== undefined && wordChar.test(ch);

/** Every occurrence of `find` in the document's text fields, in document order. */
export function findMatches(content: SiteContent, find: string, opts: FindOptions): Match[] {
  if (!find) return [];
  const re = new RegExp(
    find.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'),
    opts.caseSensitive ? 'gu' : 'giu',
  );
  const out: Match[] = [];
  const scan = (s: string, path: string) => {
    re.lastIndex = 0;
    for (let m = re.exec(s); m; m = re.exec(s)) {
      const start = m.index;
      const end = start + m[0].length;
      // Whole word: the match must not continue a word on either side ("11" never matches "11g").
      if (
        opts.wholeWord &&
        ((isWord(s[start - 1]) && isWord(m[0][0])) ||
          (isWord(s[end]) && isWord(m[0][m[0].length - 1])))
      ) {
        re.lastIndex = start + 1;
        continue;
      }
      const before = (start > CONTEXT ? '…' : '') + s.slice(Math.max(0, start - CONTEXT), start);
      const after = s.slice(end, end + CONTEXT) + (end + CONTEXT < s.length ? '…' : '');
      out.push({
        path,
        label: fieldLabel(path),
        start,
        text: m[0],
        before,
        after,
        context: before + m[0] + after,
      });
    }
  };
  const walk = (v: unknown, path: string) => {
    if (typeof v === 'string') scan(v, path);
    else if (Array.isArray(v)) v.forEach((x, i) => walk(x, `${path}[${i}]`));
    else if (isNode(v)) {
      for (const [k, x] of Object.entries(v)) if (!skipKey(k)) walk(x, `${path}.${k}`);
    }
  };
  for (const k of SECTION_KEYS) walk(content[k], k);
  return out;
}

/** Replaces matches in place, literally. A match whose text has changed since is skipped. */
export function replaceMatches(
  content: SiteContent,
  matches: Match[],
  repl: string,
): { occurrences: number; fields: number } {
  const byPath = new Map<string, Match[]>();
  for (const m of matches) byPath.set(m.path, [...(byPath.get(m.path) ?? []), m]);
  let occurrences = 0;
  let fields = 0;
  for (const [path, ms] of byPath) {
    const keys = keysOf(path);
    const parent = parentOf(content, keys);
    const last = keys[keys.length - 1];
    if (!parent || last === undefined || typeof parent[last] !== 'string') continue;
    let s = parent[last] as string;
    let n = 0;
    // Last first, so earlier offsets stay valid.
    for (const m of [...ms].sort((x, y) => y.start - x.start)) {
      if (s.slice(m.start, m.start + m.text.length) !== m.text) continue;
      s = s.slice(0, m.start) + repl + s.slice(m.start + m.text.length);
      n++;
    }
    if (n) {
      parent[last] = s;
      occurrences += n;
      fields++;
    }
  }
  return { occurrences, fields };
}

// ---- Blank rows for <app-list-editor [newItem]> ----

export const blankLink = (): Link => ({ label: '', url: '' });
export const blankEducation = (): Education => ({ degree: '', school: '', years: '' });
export const blankMetric = (): Metric => ({ value: '', label: '' });
export const blankRole = (): Role => ({ period: '', title: '', org: '', points: [] });
export const blankEarlierRole = (): EarlierRole => ({ period: '', title: '', org: '' });
export const blankWorkItem = (): WorkItem => ({ tag: '', title: '', text: '' });
export const blankOrganization = (): Organization => ({
  name: '',
  note: '',
  logo_url: '',
  url: '',
});
export const blankExpertiseItem = (): ExpertiseItem => ({ title: '', text: '', chips: [] });
export const blankVideo = (): Video => ({
  youtube_id: '',
  title: '',
  description: '',
  published_on: '',
});
export const blankQA = (): QA => ({ q: '', a: '' });
