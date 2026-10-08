// The document itself: server-rendered HTML that this component mounts and
// then manages directly (Preact never re-renders inside it). It draws the
// threads' highlights, applies live updates block by block, and turns clicks
// and selections into new comments.

import { useEffect, useLayoutEffect, useRef, useState } from 'preact/hooks';
import { getState, setState, useStore, visibleThreads } from '../state';
import { applyMarks, anchorElement, leaf } from './highlight';
import { mountTops, patchTops, flash } from './patch';
import { renderDiagrams } from './mermaid';
import { selectionIn, selectionPayload } from './selection';
import { onLive, resync } from '../live';
import { kindName, elementLabel } from '../util';
import type { Draft, SelectionPayload, Segment } from '../types';

export const refs: { root: HTMLElement | null; page: HTMLElement | null; scroller: HTMLElement | null } = { root: null, page: null, scroller: null };

export function relayout() {
  window.dispatchEvent(new Event('serve:layout'));
}

// Files the document loads that changed while it was open, by URL path, with
// the stamp that makes the browser fetch them again. Blocks that arrive later
// still say the plain URL, which the browser would serve from its cache.
const stamps = new Map<string, string>();

function pathKey(u: URL): string {
  try {
    return decodeURIComponent(u.pathname);
  } catch {
    return u.pathname;
  }
}

function stamped(value: string): string | null {
  const u = new URL(value, location.href);
  const v = u.origin === location.origin ? stamps.get(pathKey(u)) : undefined;
  if (!v || u.searchParams.get('v') === v) return null;
  u.searchParams.set('v', v);
  return u.toString();
}

// restamp points every load of a changed file under el at its new stamp.
function restamp(el: Element) {
  if (!stamps.size) return;
  el.querySelectorAll('[src], [poster], [srcset]').forEach((n) => {
    for (const attr of ['src', 'poster']) {
      const v = n.getAttribute(attr);
      const next = v && stamped(v);
      if (next) {
        if (n instanceof HTMLImageElement) n.addEventListener('load', relayout, { once: true });
        n.setAttribute(attr, next);
      }
    }
    const set = n.getAttribute('srcset');
    if (set) {
      const next = set
        .split(',')
        .map((c) => {
          const [u, ...rest] = c.trim().split(/\s+/);
          return [stamped(u) || u, ...rest].join(' ');
        })
        .join(', ');
      if (next !== set) n.setAttribute('srcset', next);
    }
  });
}

// The last selection made outside comment mode, for the "c" key and the
// Comment button.
let lastSelection: Range | null = null;

export function currentSelection(): Range | null {
  const root = refs.root;
  if (!root) return null;
  const live = selectionIn(root);
  if (live) return live;
  if (lastSelection && !lastSelection.collapsed && root.contains(lastSelection.commonAncestorContainer)) return lastSelection;
  return null;
}

function topOf(el: Element | Range): number {
  const page = refs.page!;
  const r = el.getBoundingClientRect();
  return r.top - page.getBoundingClientRect().top;
}

// draftFromSelection starts a new text comment on a range.
export function draftFromSelection(r: Range): boolean {
  const root = refs.root;
  if (!root) return false;
  const sel = selectionPayload(root, r);
  if (!sel || !sel.quote.trim()) return false;
  setState({ draft: { kind: 'text', selection: sel, top: topOf(r) }, active: null });
  window.getSelection()?.removeAllRanges();
  lastSelection = null;
  return true;
}

// pickTarget is what comment mode points at: the innermost block, or an
// image inside one.
function pickTarget(root: HTMLElement, t: EventTarget | null): HTMLElement | null {
  let el = t instanceof Element ? t : null;
  if (!el || !root.contains(el)) return null;
  const img = el.closest('img');
  if (img && root.contains(img)) return img as HTMLElement;
  const svg = el.closest('pre.mermaid');
  if (svg && root.contains(svg)) return svg as HTMLElement;
  const b = el.closest('[data-b]') as HTMLElement | null;
  return b && root.contains(b) ? b : null;
}

export function draftFromElement(el: HTMLElement): boolean {
  const root = refs.root;
  if (!root) return false;
  const block = (el.matches('[data-b]') ? el : el.closest('[data-b]')) as HTMLElement | null;
  if (!block) return false;
  const tag = el.localName;
  const label = elementLabel(el);
  const d: Draft = {
    kind: 'element',
    element: { key: block.dataset.b!, element: { tag, label } },
    label: kindName(el) + (label ? ' · ' + label : ''),
    top: topOf(el),
  };
  setState({ draft: d, active: null });
  return true;
}

// Segments covering a selection, for drawing a draft before it is saved.
function draftSegments(root: HTMLElement, s: SelectionPayload): Segment[] {
  const leaves = Array.from(root.querySelectorAll('[data-t]')) as HTMLElement[];
  const a = leaves.findIndex((l) => l.dataset.b === s.start_key);
  const b = leaves.findIndex((l) => l.dataset.b === s.end_key);
  if (a < 0 || b < 0) return [];
  const out: Segment[] = [];
  for (let i = a; i <= b; i++) {
    const len = (leaves[i].textContent || '').length;
    out.push({ key: leaves[i].dataset.b!, start: i === a ? s.start_offset : 0, end: i === b ? s.end_offset : len, text: '' });
  }
  return out;
}

export function DocView() {
  const doc = useStore((s) => s.page.doc)!;
  const threads = useStore((s) => s.threads);
  const active = useStore((s) => s.active);
  const prefs = useStore((s) => s.prefs);
  const draft = useStore((s) => s.draft);
  const commenting = useStore((s) => s.commenting);
  const rootRef = useRef<HTMLElement>(null);
  const mounted = useRef('');
  const [hover, setHover] = useState<{ box: DOMRect; label: string } | null>(null);

  // First paint, and full refreshes.
  useLayoutEffect(() => {
    const root = rootRef.current!;
    refs.root = root;
    if (mounted.current !== doc.rev && doc.html !== undefined) {
      mountTops(root, doc.html, doc.tops);
      restamp(root);
      mounted.current = doc.rev;
      if (doc.has_mermaid) renderDiagrams(root).then(relayout);
      root.querySelectorAll('img').forEach((img) => img.addEventListener('load', relayout, { once: true }));
      if (location.hash) {
        const t = document.getElementById(decodeURIComponent(location.hash.slice(1)));
        if (t && root.contains(t)) t.scrollIntoView();
      }
    }
  }, [doc.rev, doc.html]);

  // Live updates.
  useEffect(() => {
    onLive({
      onDoc: (e) => {
        const root = refs.root;
        const s = getState();
        if (!root || s.editing) {
          // The editor is open: it compares this revision with the one it
          // loaded, and the page is fetched afresh when it closes.
          if (s.editing) setState((st) => ({ page: { ...st.page, doc: { ...st.page.doc!, html: undefined, rev: e.rev } }, rev: e.rev, threads: e.threads }));
          return;
        }
        if (e.from !== s.rev || e.kind !== s.page.doc?.kind) return void resync();
        const added = patchTops(root, refs.scroller!, e.tops);
        if (added === null) return void resync();
        mounted.current = e.rev;
        flash(added.filter((el) => e.tops.find((t) => t.key === (el as HTMLElement).dataset.top)?.changed));
        added.forEach((el) => el.querySelectorAll('img').forEach((img) => img.addEventListener('load', relayout, { once: true })));
        added.forEach(restamp);
        if (e.has_mermaid) renderDiagrams(root).then(relayout);
        setState((st) => ({
          rev: e.rev,
          threads: e.threads,
          page: { ...st.page, doc: { ...st.page.doc!, rev: e.rev, tops: e.tops.map((t) => ({ key: t.key, src: '' })), has_mermaid: e.has_mermaid } },
        }));
      },
      onAsset: (url) => {
        stamps.set(pathKey(new URL(url, location.href)), Date.now().toString(36));
        if (refs.root) restamp(refs.root);
      },
    });
  }, []);

  // Highlights.
  useLayoutEffect(() => {
    const root = rootRef.current!;
    const s = getState();
    const shown = prefs.showHighlights ? visibleThreads(s) : [];
    applyMarks(root, shown, active);
    if (draft && draft.kind === 'text') {
      for (const seg of draftSegments(root, draft.selection)) {
        const el = leaf(root, seg.key);
        if (!el) continue;
        applyDraft(el, seg);
      }
    }
    if (draft && draft.kind === 'element') leaf(root, draft.element.key)?.classList.add('cm-el-draft');
    relayout();
  }, [threads, active, prefs.showHighlights, prefs.showResolved, draft, doc.rev]);

  // Layout changes that move anchors.
  useEffect(() => {
    const ro = new ResizeObserver(() => relayout());
    ro.observe(rootRef.current!);
    return () => ro.disconnect();
  }, []);

  // Selections outside comment mode are remembered for "c".
  useEffect(() => {
    const remember = () => {
      const r = refs.root && selectionIn(refs.root);
      if (r) lastSelection = r.cloneRange();
    };
    document.addEventListener('selectionchange', remember);
    return () => document.removeEventListener('selectionchange', remember);
  }, []);

  // Comment mode.
  useEffect(() => {
    const root = rootRef.current!;
    document.body.classList.toggle('commenting', commenting);
    if (!commenting) {
      setHover(null);
      return;
    }
    let justSelected = false;
    const move = (e: PointerEvent) => {
      const t = pickTarget(root, e.target);
      if (!t) return setHover(null);
      const pr = refs.page!.getBoundingClientRect();
      const r = t.getBoundingClientRect();
      setHover({ box: new DOMRect(r.left - pr.left, r.top - pr.top, r.width, r.height), label: kindName(t) });
    };
    const leave = () => setHover(null);
    const up = () => {
      const r = selectionIn(root);
      if (r && r.toString().trim()) {
        justSelected = draftFromSelection(r);
      }
    };
    const click = (e: MouseEvent) => {
      const t = e.target as Element;
      if (!root.contains(t)) return;
      // Links and the page's own controls do not fire in comment mode.
      e.preventDefault();
      e.stopPropagation();
      if (justSelected) {
        justSelected = false;
        return;
      }
      // A click on an existing highlight opens that thread rather than
      // starting a comment on the block around it.
      const mark = t.closest('mark.cm') as HTMLElement | null;
      if (mark?.dataset.thread) return void setState({ active: mark.dataset.thread, draft: null });
      const target = pickTarget(root, t);
      if (target) draftFromElement(target);
    };
    root.addEventListener('pointermove', move);
    root.addEventListener('pointerleave', leave);
    window.addEventListener('mouseup', up, true);
    root.addEventListener('click', click, true);
    return () => {
      root.removeEventListener('pointermove', move);
      root.removeEventListener('pointerleave', leave);
      window.removeEventListener('mouseup', up, true);
      root.removeEventListener('click', click, true);
    };
  }, [commenting]);

  // Clicking a line number in a code view comments on that line; with
  // Shift, on every line from the last one clicked.
  const lastLine = useRef<HTMLElement | null>(null);
  const onLineNumber = (e: MouseEvent): boolean => {
    const row = e.target as HTMLElement;
    if (!row.classList?.contains('cl') || e.offsetX > row.clientWidth - (row.querySelector('.lc') as HTMLElement).clientWidth) return false;
    const lc = row.querySelector('.lc') as HTMLElement;
    const first = e.shiftKey && lastLine.current && rootRef.current!.contains(lastLine.current) ? lastLine.current : lc;
    const [a, b] = first.compareDocumentPosition(lc) & Node.DOCUMENT_POSITION_PRECEDING ? [lc, first] : [first, lc];
    lastLine.current = lc;
    const r = document.createRange();
    r.setStart(a, 0);
    r.setEnd(b, b.childNodes.length);
    if (!r.toString().trim()) return true;
    draftFromSelection(r);
    return true;
  };

  // Clicking a highlight opens its thread.
  const onClick = (e: MouseEvent) => {
    if (getState().commenting) return;
    if (doc.kind !== 'markdown' && onLineNumber(e)) return;
    const t = e.target as Element;
    // Inside another app's frame, links to other documents stay embedded.
    const a = t.closest('a[href]') as HTMLAnchorElement | null;
    if (a && getState().page.embedded && a.origin === location.origin && !t.closest('mark.cm') && !a.hash) {
      const u = new URL(a.href);
      u.searchParams.set('embed', '1');
      a.href = u.toString();
    }
    const m = t.closest('mark.cm, .cm-point') as HTMLElement | null;
    const el = t.closest('[data-cm-el]') as HTMLElement | null;
    const id = m?.dataset.thread || (el && !selectionIn(rootRef.current!) ? el.dataset.cmEl?.split(' ')[0] : undefined);
    if (id) {
      if (m && t.closest('a') && (e.metaKey || e.ctrlKey)) return; // let a modified click follow the link
      e.preventDefault();
      setState({ active: id });
      return;
    }
    if (!selectionIn(rootRef.current!) && getState().active) setState({ active: null });
  };

  const onOver = (e: MouseEvent) => {
    const m = (e.target as Element).closest('mark.cm, [data-cm-el]') as HTMLElement | null;
    const id = m ? m.dataset.thread || m.dataset.cmEl?.split(' ')[0] || null : null;
    if (id !== getState().hover) setState({ hover: id });
  };

  const cls = doc.kind === 'markdown' ? 'markdown-body' : 'code-view chroma';
  return (
    <>
      <article class={cls} ref={rootRef} onClick={onClick} onMouseOver={onOver} data-kind={doc.kind} />
      {hover && (
        <div class="pick-box" style={{ left: hover.box.x - 4 + 'px', top: hover.box.y - 3 + 'px', width: hover.box.width + 8 + 'px', height: hover.box.height + 6 + 'px' }}>
          <span>{hover.label}</span>
        </div>
      )}
    </>
  );
}

function applyDraft(el: HTMLElement, seg: Segment) {
  // Same walk as a highlight, with its own class and no thread id.
  let pos = 0;
  const w = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  const nodes: Text[] = [];
  for (let n = w.nextNode(); n; n = w.nextNode()) nodes.push(n as Text);
  for (const n of nodes) {
    const len = n.data.length;
    const ns = pos, ne = pos + len;
    pos = ne;
    if (ne <= seg.start || ns >= seg.end) continue;
    let node: Text = n;
    const a = Math.max(seg.start - ns, 0);
    const b = Math.min(seg.end - ns, len);
    if (b <= a || !n.data.trim()) continue;
    if (a > 0) node = node.splitText(a);
    if (b - a < node.data.length) node.splitText(b - a);
    const m = document.createElement('mark');
    m.className = 'cm cm-draft';
    node.parentNode!.insertBefore(m, node);
    m.appendChild(node);
  }
}

export function scrollToThread(id: string) {
  const root = refs.root;
  if (!root) return;
  const el = anchorElement(root, id);
  el?.scrollIntoView({ block: 'center', behavior: 'smooth' });
}
