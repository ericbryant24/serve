// The live connection: one event stream per tab, subscribed to the document
// it shows. The browser reconnects by itself; the server then says "resync"
// and the page fetches everything again.

import { eventsURL, Docs, Threads } from './api';
import { getState, setState } from './state';
import type { Thread } from './types';

export type DocEvent = {
  rev: string;
  from: string;
  kind: string;
  tops: { key: string; html?: string; changed?: boolean }[];
  has_mermaid: boolean;
  threads: Thread[];
};

type Handlers = {
  onDoc?: (e: DocEvent) => void;
  onAsset?: (url: string) => void;
  onResync?: () => void;
  onTree?: (dir: string) => void;
  onCounts?: () => void;
};

const handlers: Handlers = {};
export const clientId = Math.random().toString(36).slice(2, 12);

export function onLive(h: Handlers) {
  Object.assign(handlers, h);
}

export function connect() {
  const s = getState();
  const params: Record<string, string> = { client: clientId };
  if (s.page.view === 'doc' && s.page.path) params.path = s.page.path;
  if (s.page.view === 'home') params.home = '1';
  const es = new EventSource(eventsURL(params));
  let first = true;
  es.addEventListener('hello', (ev) => {
    const d = JSON.parse((ev as MessageEvent).data);
    if (d.assets && d.assets !== getState().page.assets) {
      // serve was upgraded; this page's script is out of date.
      location.reload();
      return;
    }
    if (!first) resync();
    first = false;
    setState({ connected: true });
  });
  es.addEventListener('doc', (ev) => {
    // Views without a handler of their own (images, PDFs) fetch the document again.
    if (handlers.onDoc) handlers.onDoc(JSON.parse((ev as MessageEvent).data));
    else resync();
  });
  // A file the document loads (an image, a stylesheet) changed.
  es.addEventListener('asset', (ev) => handlers.onAsset?.(JSON.parse((ev as MessageEvent).data).url));
  es.addEventListener('threads', (ev) => {
    const d = JSON.parse((ev as MessageEvent).data);
    if (d.rev && d.rev !== getState().rev) {
      // The file changed too; the doc event (or a resync) brings both.
      return;
    }
    setState({ threads: d.threads });
  });
  es.addEventListener('gone', () => setState({ gone: true }));
  es.addEventListener('tree', (ev) => handlers.onTree?.(JSON.parse((ev as MessageEvent).data).dir));
  es.addEventListener('counts', () => handlers.onCounts?.());
  es.addEventListener('resync', () => resync());
  es.onerror = () => setState({ connected: false });
}

export async function resync() {
  const s = getState();
  if (s.page.view !== 'doc' || !s.page.path) {
    handlers.onResync?.();
    return;
  }
  try {
    const d = await Docs.get(s.page.path);
    setState((st) => ({ page: { ...st.page, doc: d.doc }, threads: d.threads, rev: d.doc.rev, gone: false }));
    handlers.onResync?.();
  } catch {
    setState({ gone: true });
  }
}

export async function refreshThreads() {
  const s = getState();
  if (!s.page.path) return;
  const r = await Threads.list(s.page.path);
  setState({ threads: r.threads });
}
