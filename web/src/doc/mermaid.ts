// Mermaid is large, so it is loaded only on pages that have a diagram, from
// serve itself (no CDN, so diagrams work offline).

let loading: Promise<any> | null = null;

function load(): Promise<any> {
  if ((window as any).mermaid) return Promise.resolve((window as any).mermaid);
  if (!loading) {
    loading = new Promise((resolve, reject) => {
      const s = document.createElement('script');
      const v = (document.querySelector('link[href*="app.css"]') as HTMLLinkElement | null)?.href.split('v=')[1] || '';
      s.src = '/_serve/assets/mermaid.min.js' + (v ? '?v=' + v : '');
      s.onload = () => resolve((window as any).mermaid);
      s.onerror = () => reject(new Error('could not load the diagram renderer'));
      document.head.appendChild(s);
    });
  }
  return loading;
}

let initialised = false;

export async function renderDiagrams(root: ParentNode) {
  const nodes = Array.from(root.querySelectorAll('pre.mermaid:not([data-processed])')) as HTMLElement[];
  if (!nodes.length) return;
  try {
    const m = await load();
    if (!initialised) {
      m.initialize({ startOnLoad: false, theme: 'neutral', securityLevel: 'strict' });
      initialised = true;
    }
    for (const n of nodes) {
      if (!n.dataset.source) n.dataset.source = n.textContent || '';
    }
    await m.run({ nodes, suppressErrors: true });
  } catch (e) {
    console.warn(e);
  }
}
