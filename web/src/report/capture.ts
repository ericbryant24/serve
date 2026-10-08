// Capturing the page for a bug report without capturing its words. The page
// is cloned and every run of text is wrapped in a span with transparent text
// over a solid bar: the glyphs still take up their space, so layout, wrapping
// and overflow are exactly what the reporter saw, but the text is unreadable.
// Images, canvases and frames become hatched placeholders of the same size.

const SKIP = new Set(['SCRIPT', 'STYLE', 'TITLE', 'NOSCRIPT', 'TEMPLATE']);
const HATCH = 'repeating-linear-gradient(45deg,#c9ced6 0,#c9ced6 6px,#e4e7ec 6px,#e4e7ec 12px)';

function redactText(orig: Element, clone: Element) {
  const wo = document.createTreeWalker(orig, NodeFilter.SHOW_TEXT);
  const wc = document.createTreeWalker(clone, NodeFilter.SHOW_TEXT);
  const jobs: [Text, string][] = [];
  for (let o = wo.nextNode(), c = wc.nextNode(); o && c; o = wo.nextNode(), c = wc.nextNode()) {
    const p = o.parentElement;
    if (!p || SKIP.has(p.tagName) || !(o as Text).data.trim()) continue;
    let color = '#3a3a3a';
    try {
      const cs = getComputedStyle(p);
      if (cs.color && cs.color !== 'rgba(0, 0, 0, 0)') color = cs.color;
    } catch {}
    jobs.push([c as Text, color]);
  }
  for (const [node, color] of jobs) {
    if (!node.parentNode) continue;
    const span = document.createElement('span');
    span.setAttribute('style', `color:transparent;background:${color};border-radius:2px;-webkit-box-decoration-break:clone;box-decoration-break:clone;`);
    node.parentNode.replaceChild(span, node);
    span.appendChild(node);
  }
}

function blank(orig: Element, clone: Element, selector: string) {
  const os = orig.querySelectorAll(selector);
  const cs = clone.querySelectorAll(selector);
  for (let i = 0; i < Math.min(os.length, cs.length); i++) {
    const node = cs[i];
    if (!node.parentNode) continue;
    const r = os[i].getBoundingClientRect();
    const ph = document.createElement('div');
    ph.setAttribute('style', `display:inline-block;width:${Math.max(Math.round(r.width), 8)}px;height:${Math.max(Math.round(r.height), 8)}px;border:1px solid #b6bcc6;border-radius:3px;background:${HATCH};`);
    node.parentNode.replaceChild(ph, node);
  }
}

function background(): string {
  try {
    const bg = getComputedStyle(document.body).backgroundColor;
    if (bg && bg !== 'rgba(0, 0, 0, 0)' && bg !== 'transparent') return bg;
  } catch {}
  return '#ffffff';
}

// Inline every stylesheet so the SVG the clone is drawn through can apply
// them (an SVG loaded as an image fetches nothing).
function inlineStyles(clone: Element) {
  const css: string[] = [];
  for (const sheet of Array.from(document.styleSheets)) {
    try {
      for (const r of Array.from(sheet.cssRules)) css.push(r.cssText);
    } catch {}
  }
  clone.querySelectorAll('link[rel="stylesheet"], style, script').forEach((n) => n.remove());
  const st = document.createElement('style');
  st.textContent = css.join('\n');
  clone.querySelector('head')?.appendChild(st);
}

export async function captureStructural(): Promise<Blob> {
  const docEl = document.documentElement;
  const vw = docEl.clientWidth || innerWidth;
  const vh = innerHeight;
  const clone = docEl.cloneNode(true) as HTMLElement;
  redactText(docEl, clone);
  blank(docEl, clone, 'img,svg,canvas,video,iframe,embed,object');
  clone.querySelectorAll('.modal-backdrop, .toasts').forEach((n) => n.remove());
  inlineStyles(clone);
  // Every scrolled container keeps its scroll position.
  const main = document.querySelector('.main') as HTMLElement | null;
  const cmain = clone.querySelector('.main') as HTMLElement | null;
  if (main && cmain) {
    cmain.style.overflow = 'hidden';
    const inner = cmain.firstElementChild as HTMLElement | null;
    if (inner) inner.style.transform = `translateY(${-main.scrollTop}px)`;
  }
  clone.style.width = vw + 'px';
  clone.style.height = vh + 'px';
  const xml = new XMLSerializer().serializeToString(clone);
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${vw}" height="${vh}"><foreignObject x="0" y="0" width="100%" height="100%">${xml}</foreignObject></svg>`;
  const img = new Image();
  const scale = Math.min(devicePixelRatio || 1, 2);
  await new Promise<void>((resolve, reject) => {
    const t = setTimeout(() => reject(new Error('the capture timed out')), 8000);
    img.onload = () => (clearTimeout(t), resolve());
    img.onerror = () => (clearTimeout(t), reject(new Error('the page could not be captured')));
    img.src = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(svg);
  });
  const canvas = document.createElement('canvas');
  canvas.width = Math.max(Math.round(vw * scale), 1);
  canvas.height = Math.max(Math.round(vh * scale), 1);
  const ctx = canvas.getContext('2d')!;
  ctx.fillStyle = background();
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  ctx.scale(scale, scale);
  ctx.drawImage(img, 0, 0);
  return new Promise((resolve, reject) => canvas.toBlob((b) => (b ? resolve(b) : reject(new Error('the capture produced no image'))), 'image/png'));
}
