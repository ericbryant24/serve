export function timeAgo(iso: string, now = Date.now()): string {
  const t = Date.parse(iso);
  if (isNaN(t)) return '';
  const s = Math.max(0, (now - t) / 1000);
  if (s < 45) return 'just now';
  if (s < 3600) return Math.round(s / 60) + 'm';
  if (s < 86400) return Math.round(s / 3600) + 'h';
  if (s < 86400 * 7) return Math.round(s / 86400) + 'd';
  return new Date(t).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: new Date(t).getFullYear() === new Date().getFullYear() ? undefined : 'numeric' });
}

export function fullDate(iso: string): string {
  const t = Date.parse(iso);
  return isNaN(t) ? '' : new Date(t).toLocaleString();
}

export function clip(s: string, n: number): string {
  s = s.replace(/\s+/g, ' ').trim();
  return s.length > n ? s.slice(0, n - 1) + '…' : s;
}

export function debounce<T extends (...a: any[]) => void>(f: T, ms: number): T {
  let t: number | undefined;
  return ((...a: any[]) => {
    clearTimeout(t);
    t = window.setTimeout(() => f(...a), ms);
  }) as T;
}

export function typing(el: Element | null): boolean {
  if (!el) return false;
  const tag = el.tagName;
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || (el as HTMLElement).isContentEditable;
}

export function plural(n: number, word: string, many = word + 's'): string {
  return n + ' ' + (n === 1 ? word : many);
}

export const isMac = /Mac|iPhone|iPad/.test(navigator.platform);
export const modKey = isMac ? '⌘' : 'Ctrl';

const KIND_NAMES: Record<string, string> = {
  p: 'Paragraph', tb: 'List item', li: 'List item', ul: 'List', ol: 'List', h1: 'Heading', h2: 'Heading', h3: 'Heading',
  h4: 'Heading', h5: 'Heading', h6: 'Heading', pre: 'Code block', table: 'Table', tr: 'Table row', td: 'Table cell',
  th: 'Table cell', blockquote: 'Quote', img: 'Image', svg: 'Graphic', figure: 'Figure', a: 'Link', button: 'Button',
  input: 'Input', select: 'Menu', textarea: 'Text box', video: 'Video', canvas: 'Canvas', hr: 'Rule', details: 'Details',
  thead: 'Table header', span: 'Text', div: 'Section', section: 'Section', nav: 'Navigation', header: 'Header', footer: 'Footer',
};

export function kindName(el: Element | null, tag?: string): string {
  if (el && el.matches('pre.mermaid')) return 'Diagram';
  if (el && el.matches('.markdown-alert')) return 'Callout';
  if (el && el.matches('.lc')) return 'Line';
  if (el && el.matches('details.frontmatter')) return 'Frontmatter';
  if (el && el.matches('.html-block')) return 'HTML';
  const t = tag || (el ? el.localName : '');
  return KIND_NAMES[t] || '<' + t + '>';
}

export function elementLabel(el: Element): string {
  if (el.localName === 'img') {
    const src = el.getAttribute('src') || '';
    return el.getAttribute('alt') || decodeURIComponent(src.split('?')[0].split('/').pop() || '');
  }
  if (el.matches('pre.mermaid')) return 'diagram';
  const own = el.getAttribute('aria-label') || el.getAttribute('title');
  if (own) return clip(own, 80);
  const t = (el.textContent || '').replace(/\s+/g, ' ').trim();
  if (t) return clip(t, 80);
  const img = el.querySelector('img');
  return img ? elementLabel(img) : '';
}

export function copyText(text: string): Promise<void> {
  if (navigator.clipboard) return navigator.clipboard.writeText(text);
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.style.position = 'fixed';
  ta.style.opacity = '0';
  document.body.appendChild(ta);
  ta.select();
  document.execCommand('copy');
  ta.remove();
  return Promise.resolve();
}
