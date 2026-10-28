const esc = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');

/**
 * Renders the small inline Markdown subset used in text blocks (**bold**,
 * *italic*, `code`, [links](https://…)). Input is escaped first, and links
 * are limited to http(s), mailto and site-relative URLs.
 */
export function inlineMarkdown(text: string): string {
  let s = esc(text ?? '');
  s = s.replace(/`([^`]+)`/g, '<code>$1</code>');
  s = s.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
  s = s.replace(/(^|[^*])\*([^*\s][^*]*)\*/g, '$1<em>$2</em>');
  s = s.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (_m, label: string, url: string) => {
    const ok = /^(https?:\/\/|mailto:|\/(?!\/))/i.test(url);
    if (!ok) return label;
    const ext = /^https?:\/\//i.test(url);
    return `<a href="${url}"${ext ? ' target="_blank" rel="noopener nofollow"' : ''}>${label}</a>`;
  });
  return s.replace(/\n/g, '<br>');
}
