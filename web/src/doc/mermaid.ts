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

function dark(): boolean {
  const t = document.documentElement.getAttribute('data-theme');
  if (t === 'dark') return true;
  if (t === 'light') return false;
  return window.matchMedia('(prefers-color-scheme: dark)').matches;
}

let initialisedDark: boolean | null = null;

export async function renderDiagrams(root: ParentNode) {
  const nodes = Array.from(root.querySelectorAll('pre.mermaid:not([data-processed])')) as HTMLElement[];
  if (!nodes.length) return;
  try {
    const m = await load();
    const d = dark();
    if (initialisedDark !== d) {
      m.initialize({ startOnLoad: false, theme: d ? 'dark' : 'default', securityLevel: 'strict' });
      initialisedDark = d;
    }
    for (const n of nodes) {
      if (!n.dataset.source) n.dataset.source = n.textContent || '';
    }
    await m.run({ nodes, suppressErrors: true });
  } catch (e) {
    console.warn(e);
  }
}

// rerenderDiagrams redraws every diagram, for a theme change.
export async function rerenderDiagrams(root: ParentNode) {
  root.querySelectorAll('pre.mermaid[data-processed]').forEach((n) => {
    const el = n as HTMLElement;
    if (el.dataset.source !== undefined) {
      el.removeAttribute('data-processed');
      el.textContent = el.dataset.source;
    }
  });
  initialisedDark = null;
  await renderDiagrams(root);
}
