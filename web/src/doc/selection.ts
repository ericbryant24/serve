// Turning a browser selection into what the server needs: the leaf it starts
// in and the offset there, the leaf it ends in and the offset there, plus the
// selected text and a little context, in case the page is out of date.

import type { SelectionPayload } from '../types';

const LEAF = '[data-t]';

function textLenBefore(leaf: Element, node: Node, offset: number): number {
  // Offset of (node, offset) in the leaf's text.
  let n = 0;
  const w = document.createTreeWalker(leaf, NodeFilter.SHOW_TEXT);
  if (node.nodeType === Node.TEXT_NODE) {
    for (let t = w.nextNode(); t; t = w.nextNode()) {
      if (t === node) return n + offset;
      n += (t as Text).data.length;
    }
    return n;
  }
  // An element boundary: count text before its offset-th child.
  const child = node.childNodes[offset] || null;
  for (let t = w.nextNode(); t; t = w.nextNode()) {
    if (child && (child === t || child.contains(t) || child.compareDocumentPosition(t) & Node.DOCUMENT_POSITION_FOLLOWING)) return n;
    n += (t as Text).data.length;
  }
  return n;
}

function leafText(leaf: Element): string {
  let s = '';
  const w = document.createTreeWalker(leaf, NodeFilter.SHOW_TEXT);
  for (let t = w.nextNode(); t; t = w.nextNode()) s += (t as Text).data;
  return s;
}

// The leaf at or after (forward) / at or before (backward) a point.
function leafNear(root: Element, node: Node, offset: number, forward: boolean): { leaf: Element; offset: number } | null {
  const el = (node.nodeType === Node.ELEMENT_NODE ? (node as Element) : node.parentElement) as Element | null;
  const own = el?.closest(LEAF);
  if (own && root.contains(own)) return { leaf: own, offset: textLenBefore(own, node, offset) };
  const leaves = Array.from(root.querySelectorAll(LEAF));
  const probe = document.createRange();
  probe.setStart(node, offset);
  if (forward) {
    for (const l of leaves) {
      const r = document.createRange();
      r.selectNodeContents(l);
      if (r.compareBoundaryPoints(Range.START_TO_START, probe) >= 0) return { leaf: l, offset: 0 };
    }
  } else {
    for (let i = leaves.length - 1; i >= 0; i--) {
      const r = document.createRange();
      r.selectNodeContents(leaves[i]);
      if (r.compareBoundaryPoints(Range.END_TO_END, probe) <= 0) return { leaf: leaves[i], offset: leafText(leaves[i]).length };
    }
  }
  return null;
}

export function selectionIn(root: HTMLElement): Range | null {
  const sel = window.getSelection();
  if (!sel || sel.isCollapsed || sel.rangeCount === 0) return null;
  const r = sel.getRangeAt(0);
  if (!root.contains(r.commonAncestorContainer)) return null;
  if (!r.toString().trim()) return null;
  return r;
}

export function selectionPayload(root: HTMLElement, r: Range): SelectionPayload | null {
  const s = leafNear(root, r.startContainer, r.startOffset, true);
  const e = leafNear(root, r.endContainer, r.endOffset, false);
  if (!s || !e) return null;
  const quote = r.toString();
  const st = leafText(s.leaf);
  const et = leafText(e.leaf);
  return {
    start_key: (s.leaf as HTMLElement).dataset.b!,
    start_offset: s.offset,
    end_key: (e.leaf as HTMLElement).dataset.b!,
    end_offset: e.offset,
    quote,
    prefix: st.slice(Math.max(0, s.offset - 32), s.offset),
    suffix: et.slice(e.offset, e.offset + 32),
  };
}
