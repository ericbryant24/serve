// The live connection. A browser allows six connections to one server at a
// time and each tab's event stream holds one open, so tabs share a stream
// through a shared worker (live-worker.ts), which passes each tab the events
// for the document it shows. A browser without shared workers opens a stream
// per tab. Either way, after a reconnect the page fetches everything again.

import { eventsURL, token, Docs, Threads } from './api';
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
  const sub: { path?: string; home?: boolean } = {};
  if (s.page.view === 'doc' && s.page.path) sub.path = s.page.path;
  if (s.page.view === 'home') sub.home = true;
  let worker: SharedWorker;
  try {
    worker = new SharedWorker('/_serve/assets/live-worker.js?v=' + s.page.assets, { name: 'serve-live' });
  } catch {
    connectAlone(sub);
    return;
  }
  let heard = false;
  // A worker that cannot start (an older serve without one) leaves the tab
  // on a stream of its own.
  worker.onerror = () => {
    if (!heard) connectAlone(sub);
    heard = true;
  };
  const port = worker.port;
  const hello = (type: 'hello' | 'pong', again = false) =>
    port.postMessage({ type, client: clientId, token, url: eventsURL({ shared: '1' }), sub, again });
  port.onmessage = (m) => {
    const d = m.data;
    heard = true;
    switch (d.type) {
      case 'hello':
        checkAssets(d.hello);
        break;
      case 'ready':
        setState({ connected: true });
        if (d.resync) resync();
        break;
      case 'down':
        setState({ connected: false });
        break;
      case 'ping':
        hello('pong');
        break;
      case 'event':
        dispatch(d.event, d.data);
        break;
    }
  };
  port.start();
  hello('hello');
  addEventListener('pagehide', () => port.postMessage({ type: 'bye', client: clientId }));
  addEventListener('pageshow', (e) => {
    if (e.persisted) hello('hello', true);
  });
}

// connectAlone opens a stream for this tab only.
function connectAlone(sub: { path?: string; home?: boolean }) {
  const params: Record<string, string> = { client: clientId };
  if (sub.path) params.path = sub.path;
  if (sub.home) params.home = '1';
  const es = new EventSource(eventsURL(params));
  let first = true;
  es.addEventListener('hello', (ev) => {
    if (!checkAssets(JSON.parse((ev as MessageEvent).data))) return;
    if (!first) resync();
    first = false;
    setState({ connected: true });
  });
  for (const name of ['doc', 'asset', 'threads', 'gone', 'tree', 'counts', 'resync']) {
    es.addEventListener(name, (ev) => dispatch(name, JSON.parse((ev as MessageEvent).data)));
  }
  es.onerror = () => setState({ connected: false });
}

// checkAssets reloads the page when serve was upgraded under it.
function checkAssets(hello: { assets?: string }): boolean {
  if (hello.assets && hello.assets !== getState().page.assets) {
    location.reload();
    return false;
  }
  return true;
}

function dispatch(event: string, d: any) {
  switch (event) {
    case 'doc':
      // Views without a handler of their own (images, PDFs) fetch the document again.
      if (handlers.onDoc) handlers.onDoc(d);
      else resync();
      break;
    case 'asset':
      // A file the document loads (an image, a stylesheet) changed.
      handlers.onAsset?.(d.url);
      break;
    case 'threads':
      // When the file changed too, the doc event (or a resync) brings both.
      if (!d.rev || d.rev === getState().rev) setState({ threads: d.threads });
      break;
    case 'gone':
      setState({ gone: true });
      break;
    case 'tree':
      handlers.onTree?.(d.dir);
      break;
    case 'counts':
      handlers.onCounts?.();
      break;
    case 'resync':
      resync();
      break;
  }
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
