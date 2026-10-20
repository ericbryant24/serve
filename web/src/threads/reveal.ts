// Bringing a thread's card into view in the comments panel. On its own, with
// no imports, so the views that pick a thread (the document, the frame, the
// cards themselves) can all use it.

// afterLayout runs f once the cards have rendered and moved.
export function afterLayout(f: () => void) {
  requestAnimationFrame(() => requestAnimationFrame(f));
}

// scrollPanelTo scrolls the panel to a thread's card, if the panel shows it,
// and says whether it did.
export function scrollPanelTo(id: string): boolean {
  const card = document.querySelector(`.panel .thread[data-thread="${CSS.escape(id)}"]`) as HTMLElement | null;
  if (!card) return false;
  const body = card.closest('.col-body') as HTMLElement;
  const r = card.getBoundingClientRect(), b = body.getBoundingClientRect();
  if (r.top < b.top || r.bottom > b.bottom) body.scrollTo({ top: body.scrollTop + r.top - b.top - 16, behavior: 'smooth' });
  return true;
}
