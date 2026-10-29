/** Editable site content, as GET /api/public/site-content returns it ({years} already expanded). */

export type SectionKey =
  | 'identity'
  | 'navigation'
  | 'hero'
  | 'about'
  | 'experience'
  | 'work'
  | 'organizations'
  | 'expertise'
  | 'videos'
  | 'blog'
  | 'newsletter'
  | 'faq'
  | 'contact'
  | 'seo';

export interface SiteContent {
  identity: Identity;
  navigation: Navigation;
  hero: Hero;
  about: About;
  experience: Experience;
  work: Work;
  organizations: Organizations;
  expertise: Expertise;
  videos: Videos;
  blog: Blog;
  newsletter: Newsletter;
  faq: Faq;
  contact: Contact;
  seo: SiteSeo;
}

export interface Identity {
  name: string; // 80 R. Brand, titles, JSON-LD, llms.txt heading, resume file name, default author
  initials: string; // 4 R. Header brand mark
  short_role: string; // 80 R. Hero card, Person.jobTitle, llms.txt heading
  location: string; // 80. Footer "© {year} {name} · {location}" (separator omitted when "")
  city: string; // 60. Person.address.addressLocality
  country_code: string; // "" or ^[A-Z]{2}$ (upper-cased on save). Person.address.addressCountry
  experience_years: number; // integer 1–60 R. Replaces {years} everywhere in the public view
  photo_url: string; // 300 R, local image
  summary: string; // 600 R. About first paragraph, Person.description
  linkedin_url: string; // 300, "" or https
  github_url: string; // 300, "" or https
  youtube_url: string; // 300, "" or https (channel)
  youtube_handle: string; // "" or ^@[A-Za-z0-9._-]{3,30}$ ("@" added if missing)
  other_links: Link[]; // [≤5] contact buttons, footer "Elsewhere", Person.sameAs
  education: Education[]; // [≤10] Person.alumniOf
  languages: string[]; // [≤12] ×40. Person.knowsLanguage
}
export interface Link {
  label: string; // 40 R
  url: string; // 300 R, https
}
export interface Education {
  degree: string; // 120 R
  school: string; // 120 R
  years: string; // 40
}

export interface NavCard {
  title: string; // 40 R
  text: string; // 120
}
export interface Navigation {
  brand_subtitle: string; // 40. Header <small> under the name
  resume_label: string; // 40. Header button; hidden when "" or the resume is unavailable
  mega_work: NavCard; // "Explore" menu cards; work/career cards hide with their sections
  mega_videos: NavCard;
  mega_career: NavCard;
  mega_blog: NavCard;
}

export interface Metric {
  value: string; // 24 R, e.g. "{years}+ years"
  label: string; // 120 R
}
export interface Hero {
  // always visible
  eyebrow: string; // 120
  title: string; // 120 R. h1 text
  title_highlight: string; // 120. Rendered in <em> after title
  lead: string; // 600 R
  primary_cta_label: string; // 40. Links to #work; hidden when "" or work hidden
  resume_cta_label: string; // 40. Opens the resume modal; hidden when "" or the resume is unavailable
  availability_visible: boolean;
  availability_text: string; // 160. R when availability_visible
  metrics: Metric[]; // [≤6] hero panel key numbers
}

export interface About {
  visible: boolean;
  label: string; // 40 R. Eyebrow after the number; also nav and sitemap label
  stamp_kicker: string; // 40
  stamp_lines: string[]; // [≤4] ×24
  stamp_text: string; // 200
  kicker: string; // 60
  heading: string; // 160 R
  show_summary: boolean; // first paragraph = identity.summary
  paragraphs: string[]; // [≤4] ×1200
  skills: string[]; // [≤40] ×40. Chips and Person.knowsAbout
}

export interface Role {
  period: string; // 40 R
  title: string; // 120 R
  org: string; // 120
  points: string[]; // [≤8] ×300
}
export interface EarlierRole {
  period: string; // 40 R
  title: string; // 120 R
  org: string; // 120
}
export interface Experience {
  visible: boolean;
  label: string; // 40 R. Also nav and sitemap label
  heading: string; // 160 R
  intro: string; // 500
  cta_label: string; // 40. Links to #contact; hidden when ""
  roles: Role[]; // [≤12]
  earlier_label: string; // 80. <summary> of the earlier-roles <details>
  earlier_roles: EarlierRole[]; // [≤30]
}

export interface WorkItem {
  tag: string; // 40
  title: string; // 120 R
  text: string; // 400 R
}
export interface Work {
  visible: boolean;
  label: string; // 40 R
  heading: string; // 160 R
  intro: string; // 500
  items: WorkItem[]; // [≤12]
}

export interface Organization {
  name: string; // 80 R. Also the logo's alt text
  note: string; // 40. e.g. "Employer", "Client engagement", "Project engagement"
  logo_url: string; // 300. "" or local image
  url: string; // 300. "" or https
}
export interface Organizations {
  visible: boolean;
  label: string; // 40 R
  heading: string; // 160 R
  intro: string; // 500
  items: Organization[]; // [≤24]
}

export interface ExpertiseItem {
  title: string; // 80 R
  text: string; // 400
  chips: string[]; // [≤8] ×40
}
export interface Expertise {
  visible: boolean;
  label: string; // 40 R
  heading: string; // 160 R
  intro: string; // 500
  items: ExpertiseItem[]; // [≤9]
}

export interface Video {
  youtube_id: string; // input: URL or ID; stored: ^[A-Za-z0-9_-]{11}$. R in items; may be "" in featured
  title: string; // 120. R when youtube_id is set
  description: string; // 400
  published_on: string; // "" or YYYY-MM-DD. VideoObject.uploadDate (no VideoObject without it)
}
export interface Videos {
  visible: boolean; // home videos band
  label: string; // 40 R
  heading: string; // 160 R
  intro: string; // 500
  channel_cta_label: string; // 60. Rendered "{label} {youtube_handle} ↗"
  featured: Video;
  items: Video[]; // [≤50] curated list for /videos; IDs unique
  include_article_videos: boolean;
  page_heading: string; // 80 R always. /videos h1
  page_intro: string; // 500
  page_cta_label: string; // 60. Channel link on /videos and in the placeholder
  empty_text: string; // 300
  placeholder_title: string; // 80. Shown when there is no video at all
  placeholder_text: string; // 200
}

export interface Blog {
  visible: boolean; // home "Notes & insights" band (also needs a published article)
  label: string; // 40 R
  heading: string; // 160 R
  intro: string; // 500
  cta_label: string; // 40
  page_heading: string; // 80 R always. /blog h1 when there is no tag filter
  page_intro: string; // 500
  empty_text: string; // 300
}
export interface Newsletter {
  visible: boolean; // home block only; /blog and articles always show the form
  label: string; // 40
  heading: string; // 160 R always
  intro: string; // 400
}
export interface QA {
  q: string; // 200 R
  a: string; // 1200 R
}
export interface Faq {
  visible: boolean;
  label: string; // 40
  heading: string; // 160 R
  items: QA[]; // [≤20] also FAQPage JSON-LD
}
export interface Contact {
  // always visible
  label: string; // 40 R
  heading: string; // 160 R
  intro: string; // 500
  show_links: boolean; // LinkedIn / GitHub / YouTube / other link buttons
}
export interface SiteSeo {
  home_title: string; // 70 R
  home_description: string; // 300 R (aim for ≤160)
  videos_title: string; // 70 R
  videos_description: string; // 300 R
  blog_title: string; // 70 R
  blog_description: string; // 300 R
  llms_summary: string; // 600 R. The "> " paragraph of /llms.txt
}

/** Document order, as the API stores and returns it. */
export const SECTION_KEYS: readonly SectionKey[] = [
  'identity',
  'navigation',
  'hero',
  'about',
  'experience',
  'work',
  'organizations',
  'expertise',
  'videos',
  'blog',
  'newsletter',
  'faq',
  'contact',
  'seo',
];

const FALLBACK_NAME = 'Sayantan Chakraborty';
const blankCard = (): NavCard => ({ title: '', text: '' });

/** Used when the API is unreachable and there is no last good copy. Not a copy of the defaults. */
export const SITE_FALLBACK: SiteContent = {
  identity: {
    name: FALLBACK_NAME,
    initials: 'SC',
    short_role: 'Oracle DBA & Database Reliability Engineer',
    location: '',
    city: '',
    country_code: '',
    experience_years: 1,
    photo_url: '/images/sayantan.jpg',
    summary: '',
    linkedin_url: '',
    github_url: '',
    youtube_url: '',
    youtube_handle: '',
    other_links: [],
    education: [],
    languages: [],
  },
  navigation: {
    brand_subtitle: '',
    resume_label: '',
    mega_work: blankCard(),
    mega_videos: blankCard(),
    mega_career: blankCard(),
    mega_blog: blankCard(),
  },
  hero: {
    eyebrow: '',
    title: FALLBACK_NAME,
    title_highlight: '',
    lead: 'This page is temporarily unavailable. Please try again in a minute.',
    primary_cta_label: '',
    resume_cta_label: '',
    availability_visible: false,
    availability_text: '',
    metrics: [],
  },
  about: {
    visible: false,
    label: '',
    stamp_kicker: '',
    stamp_lines: [],
    stamp_text: '',
    kicker: '',
    heading: '',
    show_summary: false,
    paragraphs: [],
    skills: [],
  },
  experience: {
    visible: false,
    label: '',
    heading: '',
    intro: '',
    cta_label: '',
    roles: [],
    earlier_label: '',
    earlier_roles: [],
  },
  work: { visible: false, label: '', heading: '', intro: '', items: [] },
  organizations: { visible: false, label: '', heading: '', intro: '', items: [] },
  expertise: { visible: false, label: '', heading: '', intro: '', items: [] },
  videos: {
    visible: false,
    label: '',
    heading: '',
    intro: '',
    channel_cta_label: '',
    featured: { youtube_id: '', title: '', description: '', published_on: '' },
    items: [],
    include_article_videos: false,
    page_heading: '',
    page_intro: '',
    page_cta_label: '',
    empty_text: '',
    placeholder_title: '',
    placeholder_text: '',
  },
  blog: {
    visible: false,
    label: '',
    heading: '',
    intro: '',
    cta_label: '',
    page_heading: '',
    page_intro: '',
    empty_text: '',
  },
  newsletter: { visible: false, label: '', heading: '', intro: '' },
  faq: { visible: false, label: '', heading: '', items: [] },
  contact: { label: 'Contact', heading: 'Get in touch', intro: '', show_links: false },
  seo: {
    home_title: FALLBACK_NAME,
    home_description: '',
    videos_title: FALLBACK_NAME,
    videos_description: '',
    blog_title: FALLBACK_NAME,
    blog_description: '',
    llms_summary: '',
  },
};

/** Home sections that carry a number ("01 / About"), in page order. */
export type NumberedSection =
  'about' | 'experience' | 'work' | 'organizations' | 'expertise' | 'videos' | 'blog' | 'contact';

/** "01", "02", … over the sections actually shown (D17); a hidden section gets "". */
export function sectionNumbers(
  c: SiteContent,
  hasArticle: boolean,
): Record<NumberedSection, string> {
  const shown: Record<NumberedSection, boolean> = {
    about: c.about.visible,
    experience: c.experience.visible,
    work: c.work.visible,
    organizations: c.organizations.visible,
    expertise: c.expertise.visible,
    videos: c.videos.visible,
    blog: c.blog.visible && hasArticle,
    contact: true,
  };
  let n = 0;
  const out = {} as Record<NumberedSection, string>;
  for (const k of Object.keys(shown) as NumberedSection[]) {
    out[k] = shown[k] ? String(++n).padStart(2, '0') : '';
  }
  return out;
}

export interface ProfileLink extends Link {
  kind: 'linkedin' | 'github' | 'youtube' | 'other';
}

/** The links that are set, then other_links: contact buttons, footer "Elsewhere", Person.sameAs. */
export function profileLinks(c: SiteContent): ProfileLink[] {
  const id = c.identity;
  const out: ProfileLink[] = [];
  if (id.linkedin_url) out.push({ kind: 'linkedin', label: 'LinkedIn', url: id.linkedin_url });
  if (id.github_url) out.push({ kind: 'github', label: 'GitHub', url: id.github_url });
  if (id.youtube_url) out.push({ kind: 'youtube', label: 'YouTube', url: id.youtube_url });
  for (const l of id.other_links ?? []) {
    if (l.url) out.push({ kind: 'other', label: l.label || l.url, url: l.url });
  }
  return out;
}
