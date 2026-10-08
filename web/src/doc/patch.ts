// Live updates replace only the top-level blocks whose HTML changed. Kept
// blocks keep their DOM (so a rendered Mermaid diagram stays rendered), and
// the block at the top of the window stays where it was on screen.

export type TopUpdate = { key: string; html?: string; changed?: boolean };

function topKey(el: Element): string | undefined {
  return (el as HTMLElement).dataset.top;
}

// mount fills a container with the initial HTML, tagging each top-level
// block with its key.
export function mountTops(root: HTMLElement, html: string, tops: { key: string }[] | undefined) {
  root.innerHTML = html;
  if (!tops) return;
  const kids = Array.from(root.children);
  if (kids.length !== tops.length) return; // blocks that render to several elements; keys are optional
  kids.forEach((k, i) => ((k as HTMLElement).dataset.top = tops[i].key));
}

function fromHTML(html: string): Element[] {
  const t = document.createElement('template');
  t.innerHTML = html;
  return Array.from(t.content.children);
}

// The first block on screen, and how far its top is from the viewport top.
function screenAnchor(root: HTMLElement, scroller: HTMLElement): { key: string; offset: number } | null {
  const top = scroller.getBoundingClientRect().top;
  for (const el of Array.from(root.children)) {
    const r = el.getBoundingClientRect();
    if (r.bottom > top + 1) {
      const k = topKey(el);
      return k ? { key: k, offset: r.top - top } : null;
    }
  }
  return null;
}

// patch applies an update and returns the elements that are new. When the
// container's blocks are not keyed it returns null and the caller re-renders.
export function patchTops(root: HTMLElement, scroller: HTMLElement, tops: TopUpdate[]): Element[] | null {
  const existing = new Map<string, Element>();
  for (const el of Array.from(root.children)) {
    const k = topKey(el);
    if (!k) return null;
    existing.set(k, el);
  }
  for (const t of tops) if (!existing.has(t.key) && t.html === undefined) return null;
  const anchor = screenAnchor(root, scroller);
  const added: Element[] = [];
  const next: Element[] = [];
  for (const t of tops) {
    let el = existing.get(t.key);
    if (el) existing.delete(t.key);
    else {
      const made = fromHTML(t.html || '');
      if (made.length !== 1) {
        // A block that is not a single element: wrap it so it can be keyed.
        const wrap = document.createElement('div');
        made.forEach((m) => wrap.appendChild(m));
        el = wrap;
      } else el = made[0];
      (el as HTMLElement).dataset.top = t.key;
      added.push(el);
    }
    if (t.changed) added.includes(el) || added.push(el);
    next.push(el);
  }
  existing.forEach((el) => el.remove());
  let prev: Element | null = null;
  for (const el of next) {
    const want: ChildNode | null = prev ? prev.nextSibling : root.firstChild;
    if (want !== el) root.insertBefore(el, want);
    prev = el;
  }
  if (anchor) {
    const el = Array.from(root.children).find((c) => topKey(c) === anchor.key);
    if (el) {
      const r = el.getBoundingClientRect();
      const top = scroller.getBoundingClientRect().top;
      scroller.scrollTop += r.top - top - anchor.offset;
    }
  }
  return added;
}

export function flash(els: Element[]) {
  for (const el of els) {
    el.classList.remove('just-changed');
    void (el as HTMLElement).offsetWidth;
    el.classList.add('just-changed');
    window.setTimeout(() => el.classList.remove('just-changed'), 2600);
  }
}
