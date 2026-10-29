export interface Hero {
  type: 'none' | 'image' | 'video' | 'youtube';
  url?: string;
  youtube_id?: string;
  alt?: string;
  caption?: string;
  poster?: string;
  heading?: string;
  subheading?: string;
}

export interface Block {
  type: 'heading' | 'paragraph' | 'list' | 'quote' | 'code' | 'callout' | 'html' | 'divider';
  text?: string;
  level?: number;
  style?: string;
  items?: string[];
  cite?: string;
  language?: string;
  tone?: string;
  html?: string;
}

export interface SEO {
  title?: string;
  description?: string;
  og_image?: string;
  canonical?: string;
  noindex?: boolean;
}

export interface Page {
  id: string;
  kind: 'page' | 'article';
  parent_id: string | null;
  menu_order: number;
  slug: string;
  path: string;
  title: string;
  excerpt: string;
  hero: Hero;
  blocks: Block[];
  featured_image_url: string;
  author_name: string;
  categories: string[];
  tags: string[];
  seo: SEO;
  status: 'draft' | 'published';
  published_at: string | null;
  first_published_at: string | null;
  source?: string;
  created_at: string;
  updated_at: string;
  reading_minutes: number;
}

export interface SiteConfig {
  site_url: string;
  blog_enabled: boolean;
  gtm_id: string;
  ga4_id: string;
  newsletter_topics: string[];
  consent_text: string;
  privacy_policy_url: string;
  privacy_policy_version: string;
  resume_available: boolean;
}

export interface Paged<T> {
  items: T[];
  total: number;
}

export interface MediaItem {
  id: string;
  url: string;
  kind: 'image' | 'video' | 'file';
  alt: string;
  caption: string;
  width: number | null;
  height: number | null;
  content_type: string | null;
  size_bytes: number | null;
  filename: string | null;
  source: string | null;
  created_at: string;
}
