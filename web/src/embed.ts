// Runs inside an HTML page shown in serve's iframe, on the content origin.
// It has no access to serve's API. It draws comment highlights, offers comment
// mode, and tells the app (its parent window) about selections and clicks.
//
// Offsets are into the page's "body text": every text node under <body>,
// outside script, style, template, noscript and textarea, skipping nodes that
// are only whitespace. The server computes the same text from the file's
// source, so an offset means the same thing on both sides.

(function () {
  const me = document.currentScript as HTMLScriptElement | null;
  const app = me?.dataset.serveApp || '';
  if (!app || window.parent === window) return;
  const SKIP = new Set(['SCRIPT', 'STYLE', 'TEMPLATE', 'NOSCRIPT', 'TEXTAREA', 'HEAD', 'TITLE']);
  const UI_ID = 'serve-embed-ui';
  let threads: any[] = [];
  let active: string | null = null;
  let commenting = false;

  const send = (msg: any) => window.parent.postMessage(msg, app);

  // --- body text -----------------------------------------------------------------
  type TN = { node: Text; start: number };
  function bodyNodes(): TN[] {
    const out: TN[] = [];
    let pos = 0;
    const w = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT, {
      acceptNode(n) {
        for (let p = n.parentElement; p; p = p.parentElement) {
          if (SKIP.has(p.tagName) || p.id === UI_ID) return NodeFilter.FILTER_REJECT;
        }
        return (n as Text).data.trim() ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_REJECT;
      },
    });
    for (let n = w.nextNode(); n; n = w.nextNode()) {
      out.push({ node: n as Text, start: pos });
      pos += (n as Text).data.length;
    }
    return out;
  }

  function bodyText(nodes: TN[]): string {
    return nodes.map((n) => n.node.data).join('');
  }

  function offsetOf(nodes: TN[], node: Node, offset: number, forward: boolean): number | null {
    if (node.nodeType === Node.TEXT_NODE) {
      const hit = nodes.find((n) => n.node === node);
      if (hit) return hit.start + offset;
    }
    // A boundary between nodes: the next (or previous) counted text node.
    const probe = document.createRange();
    probe.setStart(node, offset);
    if (forward) {
      for (const n of nodes) {
        const r = document.createRange();
        r.selectNodeContents(n.node);
        if (r.compareBoundaryPoints(Range.START_TO_START, probe) >= 0) return n.start;
      }
      return null;
    }
    for (let i = nodes.length - 1; i >= 0; i--) {
      const r = document.createRange();
      r.selectNodeContents(nodes[i].node);
      if (r.compareBoundaryPoints(Range.END_TO_END, probe) <= 0) return nodes[i].start + nodes[i].node.data.length;
    }
    return null;
  }

  function payload(nodes: TN[], r: Range) {
    const s = offsetOf(nodes, r.startContainer, r.startOffset, true);
    const e = offsetOf(nodes, r.endContainer, r.endOffset, false);
    if (s === null || e === null || e <= s) return null;
    const text = bodyText(nodes);
    return { start_key: 'body', start_offset: s, end_key: 'body', end_offset: e, quote: text.slice(s, e), prefix: text.slice(Math.max(0, s - 32), s), suffix: text.slice(e, e + 32) };
  }

  // --- drawing -------------------------------------------------------------------
  // Desk's comment colors (--mark-strong, --mark-line, --fg-4, --attn, --accent),
  // written out because this page can't read Desk's tokens. The yellow is
  // translucent so text stays readable on a page's own background.
  const style = document.createElement('style');
  style.textContent = `
mark.serve-cm{background:rgba(251,220,110,.45);color:inherit;border-radius:2px;cursor:pointer;box-shadow:0 2px 0 #e2b41f}
mark.serve-cm.serve-active{background:rgba(251,220,110,.85)}
mark.serve-cm.serve-resolved{background:none;box-shadow:none;text-decoration:underline dotted #a8a8a2;text-underline-offset:3px}
mark.serve-cm.serve-resolved:hover,mark.serve-cm.serve-resolved.serve-active{background:rgba(128,128,120,.15)}
mark.serve-cm.serve-changed{text-decoration:underline dotted #e2b41f;text-underline-offset:3px}
.serve-el{outline:2px solid #e2b41f!important;outline-offset:3px}
.serve-el.serve-active{outline-color:#a35300!important}
#${UI_ID}{position:fixed;inset:0;pointer-events:none;z-index:2147483646}
#${UI_ID} .box{position:fixed;border:2px solid #1f7a57;background:rgba(31,122,87,.05);border-radius:4px}
#${UI_ID} .box span{position:absolute;top:-22px;left:-2px;background:#1f7a57;color:#fff;font:500 11.5px/18px -apple-system,BlinkMacSystemFont,system-ui,sans-serif;padding:0 8px;border-radius:4px;white-space:nowrap}
body.serve-commenting, body.serve-commenting *{cursor:crosshair!important}
`;
  document.head.appendChild(style);
  const ui = document.createElement('div');
  ui.id = UI_ID;
  const box = document.createElement('div');
  box.className = 'box';
  box.style.display = 'none';
  box.innerHTML = '<span></span>';
  ui.appendChild(box);
  document.body.appendChild(ui);

  function clear() {
    document.querySelectorAll('mark.serve-cm').forEach((m) => {
      const p = m.parentNode!;
      while (m.firstChild) p.insertBefore(m.firstChild, m);
      p.removeChild(m);
      p.normalize();
    });
    document.querySelectorAll('.serve-el').forEach((e) => {
      e.classList.remove('serve-el', 'serve-active');
      (e as HTMLElement).removeAttribute('data-serve-thread');
    });
  }

  function wrapRange(nodes: TN[], start: number, end: number, cls: string, id: string) {
    for (const n of nodes) {
      const len = n.node.data.length;
      const ns = n.start, ne = ns + len;
      if (ne <= start || ns >= end) continue;
      let node = n.node;
      const a = Math.max(start - ns, 0), b = Math.min(end - ns, len);
      if (b <= a) continue;
      if (a > 0) node = node.splitText(a);
      if (b - a < node.data.length) node.splitText(b - a);
      const m = document.createElement('mark');
      m.className = cls;
      m.dataset.serveThread = id;
      node.parentNode!.insertBefore(m, node);
      m.appendChild(node);
    }
  }

  const squash = (s: string) => s.replace(/\s+/g, '');

  // Where a thread's text is now: the server's offsets when the page agrees
  // with them, else the quote searched with its context.
  function locate(nodes: TN[], text: string, t: any): [number, number] | null {
    const seg = t.placement?.segments?.[0];
    if (seg && squash(text.slice(seg.start, seg.end)) === squash(seg.text || '')) return [seg.start, seg.end];
    const q: string = t.anchor?.display || '';
    if (!q) return null;
    let best = -1, bestScore = -1;
    for (let i = text.indexOf(q); i >= 0; i = text.indexOf(q, i + 1)) {
      const score = (seg ? -Math.abs(i - seg.start) / 1e6 : 0);
      if (score > bestScore) (best = i), (bestScore = score);
    }
    return best >= 0 ? [best, best + q.length] : null;
  }

  function elementFor(nodes: TN[], text: string, t: any): Element | null {
    const el = t.anchor?.element;
    const range = locate(nodes, text, t);
    if (range) {
      // The deepest element whose text is exactly that range.
      const first = nodes.find((n) => n.start + n.node.data.length > range[0]);
      for (let p = first?.node.parentElement; p && p !== document.body; p = p.parentElement) {
        if (!el || p.localName === el.tag) {
          const r = document.createRange();
          r.selectNodeContents(p);
          if (squash(r.toString()) === squash(text.slice(range[0], range[1]))) return p;
        }
      }
    }
    if (el?.selector) {
      try {
        const found = document.querySelector(el.selector);
        if (found && found.localName === el.tag) return found;
      } catch {}
    }
    return null;
  }

  function draw() {
    clear();
    const nodes = bodyNodes();
    const text = bodyText(nodes);
    for (const t of threads) {
      const cls = 'serve-cm' + (t.status === 'resolved' ? ' serve-resolved' : '') + (t.location?.state === 'changed' ? ' serve-changed' : '') + (t.id === active ? ' serve-active' : '');
      if (t.scope === 'element') {
        const e = elementFor(nodes, text, t);
        if (e) {
          e.classList.add('serve-el');
          if (t.id === active) e.classList.add('serve-active');
          (e as HTMLElement).dataset.serveThread = t.id;
        }
        continue;
      }
      if (t.scope !== 'text') continue;
      const r = locate(nodes, text, t);
      if (r) wrapRange(bodyNodes(), r[0], r[1], cls, t.id);
    }
  }

  // --- comment mode ----------------------------------------------------------------
  function selectorFor(el: Element): string {
    const parts: string[] = [];
    for (let n: Element | null = el; n && n !== document.body; n = n.parentElement) {
      if (n.id && document.querySelectorAll('#' + CSS.escape(n.id)).length === 1) {
        parts.unshift('#' + CSS.escape(n.id));
        return parts.join(' > ');
      }
      const p: Element | null = n.parentElement;
      if (!p) break;
      const same = Array.from(p.children).filter((c) => c.localName === n!.localName);
      parts.unshift(same.length > 1 ? `${n.localName}:nth-of-type(${same.indexOf(n) + 1})` : n.localName);
    }
    return ['body', ...parts].join(' > ');
  }

  function label(el: Element): string {
    if (el.localName === 'img') return el.getAttribute('alt') || (el.getAttribute('src') || '').split('/').pop() || '';
    const t = (el.getAttribute('aria-label') || el.textContent || '').replace(/\s+/g, ' ').trim();
    return t.length > 80 ? t.slice(0, 79) + '…' : t;
  }

  function sibling(el: Element, dir: -1 | 1): string {
    for (let n = dir < 0 ? el.previousElementSibling : el.nextElementSibling; n; n = dir < 0 ? n.previousElementSibling : n.nextElementSibling) {
      if (n.id === UI_ID || n.localName === 'script' || n.localName === 'style') continue;
      const t = (n.textContent || '').replace(/\s+/g, ' ').trim();
      if (t) return t.slice(0, 200);
    }
    return '';
  }

  function target(e: Event): Element | null {
    let el = e.target instanceof Element ? e.target : null;
    if (!el || el.closest('#' + UI_ID)) return null;
    for (let s = el.closest('svg'); s; s = s.parentElement && s.parentElement.closest('svg')) el = s;
    if (el === document.body || el === document.documentElement) return null;
    return el;
  }

  let justSelected = false;
  function onMove(e: PointerEvent) {
    if (!commenting) return;
    const el = target(e);
    if (!el) return void (box.style.display = 'none');
    const r = el.getBoundingClientRect();
    Object.assign(box.style, { display: 'block', left: r.left - 3 + 'px', top: r.top - 3 + 'px', width: r.width + 6 + 'px', height: r.height + 6 + 'px' });
    (box.firstChild as HTMLElement).textContent = el.localName;
  }

  function currentSelection(): any {
    const sel = getSelection();
    if (!sel || sel.isCollapsed || !sel.toString().trim()) return null;
    return payload(bodyNodes(), sel.getRangeAt(0));
  }

  function onUp() {
    if (!commenting) return;
    const p = currentSelection();
    if (p) {
      justSelected = true;
      send({ type: 'serve:select', selection: p });
      getSelection()?.removeAllRanges();
    }
  }

  // The page's own controls. Outside comment mode they work as they always do,
  // with a comment on them or not; that comment opens from the panel instead.
  const CONTROL = 'a[href], button, input, select, textarea, label, summary, [role="button"]';

  function onClick(e: MouseEvent) {
    const m = (e.target as Element | null)?.closest?.('mark.serve-cm, [data-serve-thread]') as HTMLElement | null;
    if (!commenting) {
      if (m?.dataset.serveThread && !(e.metaKey || e.ctrlKey) && !(e.target as Element).closest(CONTROL)) {
        e.preventDefault();
        send({ type: 'serve:click', id: m.dataset.serveThread });
      }
      return;
    }
    e.preventDefault();
    e.stopPropagation();
    if (justSelected) return void (justSelected = false);
    if (m?.matches('mark') && m.dataset.serveThread) return void send({ type: 'serve:click', id: m.dataset.serveThread });
    const el = target(e);
    if (!el) return;
    const nodes = bodyNodes();
    const r = document.createRange();
    r.selectNodeContents(el);
    const range = payload(nodes, r);
    send({
      type: 'serve:element',
      label: el.localName + (label(el) ? ' · ' + label(el) : ''),
      element: { key: 'body', element: { tag: el.localName, label: label(el), selector: selectorFor(el), before: sibling(el, -1), after: sibling(el, 1) }, range: range || undefined },
    });
  }

  function swallow(e: Event) {
    if (commenting && !(e.target as Element)?.closest?.('#' + UI_ID)) e.stopPropagation();
  }

  window.addEventListener('pointermove', onMove, true);
  window.addEventListener('mouseup', onUp, true);
  window.addEventListener('click', onClick, true);
  for (const t of ['pointerdown', 'mousedown', 'dblclick', 'auxclick', 'submit']) window.addEventListener(t, swallow, true);
  window.addEventListener('keydown', (e) => {
    const t = e.target as HTMLElement;
    if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return;
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    if (['c', 'Escape', '[', ']', '?', 'r', 'e', 'p'].includes(e.key)) {
      if (e.key === 'c') {
        const p = currentSelection();
        if (p) {
          e.preventDefault();
          send({ type: 'serve:select', selection: p });
          return;
        }
      }
      e.preventDefault();
      send({ type: 'serve:key', key: e.key });
    }
  });

  // --- messages from the app --------------------------------------------------------
  window.addEventListener('message', (e) => {
    if (e.origin !== app || e.source !== window.parent) return;
    const m = e.data || {};
    if (m.type === 'serve:threads') {
      threads = m.threads || [];
      active = m.active || null;
      draw();
    } else if (m.type === 'serve:mode') {
      commenting = !!m.on;
      document.body.classList.toggle('serve-commenting', commenting);
      if (!commenting) box.style.display = 'none';
    } else if (m.type === 'serve:comment') {
      const p = currentSelection();
      if (p) send({ type: 'serve:select', selection: p });
      else send({ type: 'serve:no-selection' });
    } else if (m.type === 'serve:focus') {
      const el = document.querySelector(`[data-serve-thread="${CSS.escape(m.id)}"]`);
      el?.scrollIntoView({ block: 'center', behavior: 'smooth' });
    }
  });

  // Keep the scroll position across the reloads a file change causes.
  const key = 'serve-scroll:' + location.pathname;
  try {
    const y = sessionStorage.getItem(key);
    if (y) window.scrollTo(0, parseInt(y, 10));
  } catch {}
  window.addEventListener('scroll', () => {
    try {
      sessionStorage.setItem(key, String(window.scrollY));
    } catch {}
  }, { passive: true });

  send({ type: 'serve:ready' });
})();
