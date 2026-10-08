// Drawing threads onto the rendered document. The server says where each
// thread is as (leaf key, start, end) in the leaf's text, counted in UTF-16
// units, which is how JavaScript counts string length. So a highlight is a
// walk over the leaf's text nodes, never a search for the quoted words.

import type { Thread, Segment } from '../types';

// Text nodes directly inside these cannot hold a <mark> (it would become an
// anonymous table cell or list item); they are always formatting whitespace.
const NO_MARK_PARENT = new Set(['TABLE', 'THEAD', 'TBODY', 'TFOOT', 'TR', 'UL', 'OL', 'DL', 'COLGROUP', 'SELECT', 'PICTURE']);

export function leaf(root: HTMLElement, key: string): HTMLElement | null {
  return root.querySelector(`[data-b="${CSS.escape(key)}"]`);
}

function textNodes(el: Node): Text[] {
  const out: Text[] = [];
  const w = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  for (let n = w.nextNode(); n; n = w.nextNode()) out.push(n as Text);
  return out;
}

export function leafText(el: HTMLElement): string {
  return textNodes(el).map((t) => t.data).join('');
}

// Remove every highlight, pin and point marker.
export function clearMarks(root: HTMLElement) {
  root.querySelectorAll('mark.cm').forEach((m) => {
    const parent = m.parentNode as Node;
    while (m.firstChild) parent.insertBefore(m.firstChild, m);
    parent.removeChild(m);
    parent.normalize();
  });
  root.querySelectorAll('.cm-point').forEach((p) => {
    const parent = p.parentNode as Node;
    p.remove();
    parent.normalize();
  });
  root.querySelectorAll('.cm-el-draft').forEach((e) => e.classList.remove('cm-el-draft'));
  root.querySelectorAll('[data-cm-el]').forEach((e) => {
    e.removeAttribute('data-cm-el');
    e.classList.remove('cm-el', 'cm-el-active', 'cm-el-resolved');
  });
}

// Wrap [start, end) of a leaf's text in marks. Returns false when the leaf's
// text there is not what the server expected (the page is stale).
function wrap(el: HTMLElement, seg: Segment, cls: string, id: string): boolean {
  let { start, end } = seg;
  const text = leafText(el);
  if (seg.text && text.slice(start, end).replace(/\s+/g, '') !== seg.text.replace(/\s+/g, '')) {
    // The page and the server disagree; fall back to the expected text inside
    // this one leaf only.
    const i = text.indexOf(seg.text);
    if (i < 0) return false;
    start = i;
    end = i + seg.text.length;
  }
  let pos = 0;
  for (const n of textNodes(el)) {
    const len = n.data.length;
    const ns = pos, ne = pos + len;
    pos = ne;
    if (ne <= start || ns >= end) continue;
    if (n.parentElement && NO_MARK_PARENT.has(n.parentElement.tagName)) continue;
    let node: Text = n;
    const a = Math.max(start - ns, 0);
    const b = Math.min(end - ns, len);
    if (b <= a) continue;
    if (a > 0) node = node.splitText(a);
    if (b - a < node.data.length) node.splitText(b - a);
    const m = document.createElement('mark');
    m.className = cls;
    m.dataset.thread = id;
    node.parentNode!.insertBefore(m, node);
    m.appendChild(node);
  }
  return true;
}

function point(el: HTMLElement, offset: number, cls: string, id: string) {
  const span = document.createElement('span');
  span.className = cls;
  span.dataset.thread = id;
  span.setAttribute('aria-label', 'deleted text that was commented on');
  let pos = 0;
  for (const n of textNodes(el)) {
    const len = n.data.length;
    if (offset <= pos + len) {
      const at = offset - pos;
      if (at > 0 && at < len) n.splitText(at);
      if (at >= len) n.parentNode!.insertBefore(span, n.nextSibling);
      else n.parentNode!.insertBefore(span, at === 0 ? n : n.nextSibling);
      return;
    }
    pos += len;
  }
  el.appendChild(span);
}

// apply draws the visible threads. It returns the ids of threads it could
// not place (the page has drifted from the server's idea of it).
export function applyMarks(root: HTMLElement, threads: Thread[], active: string | null): Set<string> {
  clearMarks(root);
  const missed = new Set<string>();
  for (const t of threads) {
    const p = t.placement || {};
    const resolved = t.status === 'resolved';
    const cls = 'cm' + (resolved ? ' cm-resolved' : '') + (t.location.state === 'changed' ? ' cm-changed' : '') + (t.id === active ? ' cm-active' : '');
    if (p.segments && p.segments.length) {
      let ok = false;
      for (const s of p.segments) {
        const el = leaf(root, s.key);
        if (el && wrap(el, s, cls, t.id)) ok = true;
      }
      if (!ok) missed.add(t.id);
    } else if (p.element) {
      const el = leaf(root, p.element);
      if (el) {
        // Several threads can be on one block: the attribute holds them all.
        el.dataset.cmEl = el.dataset.cmEl ? el.dataset.cmEl + ' ' + t.id : t.id;
        el.classList.add('cm-el');
        if (resolved) el.classList.add('cm-el-resolved');
        if (t.id === active) el.classList.add('cm-el-active');
      } else missed.add(t.id);
    } else if (p.point) {
      const el = leaf(root, p.point.key);
      if (el) point(el, p.point.start, 'cm-point' + (t.id === active ? ' cm-active' : ''), t.id);
      else missed.add(t.id);
    }
  }
  return missed;
}

// anchorTop is where a thread's anchor sits, relative to container's top.
export function anchorTop(root: HTMLElement, container: HTMLElement, id: string): number | null {
  const el =
    root.querySelector(`mark.cm[data-thread="${CSS.escape(id)}"]`) ||
    root.querySelector(`[data-cm-el~="${CSS.escape(id)}"]`) ||
    root.querySelector(`.cm-point[data-thread="${CSS.escape(id)}"]`);
  if (!el) return null;
  const r = el.getBoundingClientRect();
  const c = container.getBoundingClientRect();
  return r.top - c.top + container.scrollTop;
}

export function anchorElement(root: HTMLElement, id: string): Element | null {
  return (
    root.querySelector(`mark.cm[data-thread="${CSS.escape(id)}"]`) ||
    root.querySelector(`[data-cm-el~="${CSS.escape(id)}"]`) ||
    root.querySelector(`.cm-point[data-thread="${CSS.escape(id)}"]`)
  );
}
