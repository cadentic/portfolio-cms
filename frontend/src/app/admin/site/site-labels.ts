import { AREA_TITLES, groupOf } from './site-content.model';

// Field labels, keyed by path with "[]" for list rows. The editor's fields, its
// error and warning lists and the find results all read them, so the owner sees
// one name per field.
const FIELDS: Record<string, string> = {
  'identity.name': 'Full name',
  'identity.initials': 'Initials',
  'identity.short_role': 'Role',
  'identity.location': 'Location (footer)',
  'identity.city': 'City',
  'identity.country_code': 'Country code',
  'identity.experience_years': 'Years of experience',
  'identity.photo_url': 'Photo',
  'identity.summary': 'Summary',
  'identity.linkedin_url': 'LinkedIn profile',
  'identity.github_url': 'GitHub profile',
  'identity.youtube_url': 'YouTube channel',
  'identity.youtube_handle': 'YouTube handle',
  'identity.other_links': 'Other links',
  'identity.other_links[].label': 'Button text',
  'identity.education': 'Qualifications',
  'identity.education[].school': 'School or university',
  'identity.languages': 'Languages',
  'navigation.brand_subtitle': 'Subtitle under your name',
  'navigation.resume_label': 'Resume button text',
  'navigation.mega_work': 'Selected work card',
  'navigation.mega_videos': 'Videos card',
  'navigation.mega_career': 'Career card',
  'navigation.mega_blog': 'Blog card',
  'hero.eyebrow': 'Line above the headline',
  'hero.title': 'Headline',
  'hero.title_highlight': 'Headline ending',
  'hero.lead': 'Intro paragraph',
  'hero.primary_cta_label': 'Main button text',
  'hero.resume_cta_label': 'Resume button text',
  'hero.availability_visible': 'Show the availability line',
  'hero.availability_text': 'Availability line',
  'hero.metrics': 'Key numbers',
  'hero.metrics[].label': 'What it means',
  'about.kicker': 'Line above the heading',
  'about.show_summary': 'Start with your summary',
  'about.paragraphs': 'More paragraphs',
  'about.skills': 'Skills',
  'about.stamp_kicker': 'Card title',
  'about.stamp_lines': 'Card lines',
  'about.stamp_text': 'Card text',
  'experience.roles': 'Roles',
  'experience.roles[].title': 'Job title',
  'experience.roles[].points': 'Highlights',
  'experience.earlier_label': '“Show earlier roles” link text',
  'experience.earlier_roles': 'Earlier roles',
  'experience.earlier_roles[].title': 'Job title',
  'work.items': 'Projects',
  'work.items[].tag': 'Tag',
  'work.items[].text': 'Description',
  'organizations.items': 'Organisations',
  'organizations.items[].note': 'Note',
  'organizations.items[].url': 'Website',
  'organizations.items[].logo_url': 'Logo',
  'expertise.items': 'Areas',
  'expertise.items[].text': 'Description',
  'expertise.items[].chips': 'Tags',
  'videos.channel_cta_label': 'Channel button text',
  'videos.featured': 'Featured video',
  'videos.items': 'Videos',
  'videos.include_article_videos': 'Also list articles that have a YouTube video',
  'videos.page_cta_label': 'Channel link text',
  'videos.empty_text': 'Text when there are no videos',
  'videos.placeholder_title': 'Placeholder title',
  'videos.placeholder_text': 'Placeholder text',
  'blog.empty_text': 'Text when there are no articles',
  'faq.items': 'Questions',
  'faq.items[].q': 'Question',
  'faq.items[].a': 'Answer',
  'contact.show_links': 'Show link buttons',
  'seo.home_title': 'Page title',
  'seo.home_description': 'Description',
  'seo.videos_title': 'Page title',
  'seo.videos_description': 'Description',
  'seo.blog_title': 'Page title in search results',
  'seo.blog_description': 'Description in search results',
  'seo.llms_summary': 'Summary for AI assistants',
};

// The same name wherever the key appears, unless FIELDS says otherwise.
const KEYS: Record<string, string> = {
  visible: 'Show on the home page',
  label: 'Section label',
  heading: 'Heading',
  intro: 'Intro',
  cta_label: 'Button text',
  page_heading: 'Page heading',
  page_intro: 'Intro',
  title: 'Title',
  text: 'Text',
  description: 'Description',
  youtube_id: 'YouTube link or video ID',
  published_on: 'Upload date',
  period: 'Period',
  org: 'Organisation',
  name: 'Name',
  url: 'Link',
  value: 'Value',
  degree: 'Degree',
  years: 'Years',
};

// List rows: "hero.metrics[0]" → "Key number 1".
const ROWS: Record<string, string> = {
  'identity.other_links': 'Link',
  'identity.education': 'Qualification',
  'hero.metrics': 'Key number',
  'experience.roles': 'Role',
  'experience.earlier_roles': 'Earlier role',
  'work.items': 'Project',
  'organizations.items': 'Organisation',
  'expertise.items': 'Area',
  'videos.items': 'Video',
  'faq.items': 'Question',
};

const bare = (path: string) => path.replace(/\[\d+\]/g, '[]');

/** The label of the field at `path`, e.g. "hero.metrics[0].value" → "Value". */
export function labelFor(path: string): string {
  const b = bare(path).replace(/\[\]$/, '');
  const key = b.slice(b.lastIndexOf('.') + 1);
  return FIELDS[b] ?? KEYS[key] ?? key.charAt(0).toUpperCase() + key.slice(1).replace(/_/g, ' ');
}

/** "organizations.items[2].logo_url" → "Home page › Organisations (employers & clients) › Organisation 3 › Logo". */
export function uiLabel(path: string): string {
  const g = groupOf(path);
  const parts = g ? [AREA_TITLES[g.area], g.title] : [];
  const tokens = path.match(/[^.[\]]+|\[\d+\]/g) ?? [];
  let prefix = '';
  tokens.forEach((t, i) => {
    const last = i === tokens.length - 1;
    if (t.startsWith('[')) {
      const n = Number(t.slice(1, -1)) + 1;
      const row = ROWS[bare(prefix)];
      // A row of a list of objects, or a line of a one-per-line list.
      parts.push(row ? `${row} ${n}` : `${labelFor(prefix)}, line ${n}`);
      prefix += t;
      return;
    }
    prefix += (prefix ? '.' : '') + t;
    if (i === 0) return;
    if (last) {
      if (!tokens[i + 1]?.startsWith('[')) parts.push(labelFor(prefix));
    } else if (!tokens[i + 1].startsWith('[') && FIELDS[bare(prefix)]) {
      parts.push(FIELDS[bare(prefix)]); // a nested object, e.g. "Featured video"
    }
  });
  return parts.join(' › ');
}
