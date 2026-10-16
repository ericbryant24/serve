// The shared worker that holds the app's one connection to serve. A browser
// allows six connections to a server at a time, and every tab needs its
// event stream open for as long as it is open, so the seventh tab would wait
// for ever. Instead this worker opens one shared stream, subscribes each tab
// to it (POST /_serve/api/live) and hands each tab the events tagged with its
// id. live.ts is the tab's side.

type Sub = { path?: string; home?: boolean };

// What a tab sends: hello when it starts (again when it comes back from the
// back-forward cache), pong in answer to ping, bye when it goes.
type FromTab =
  | { type: 'hello' | 'pong'; client: string; token: string; url: string; sub: Sub; again?: boolean }
  | { type: 'bye'; client: string };

// resync marks a tab that has missed events, until its next subscription.
type Tab = { port: MessagePort; client: string; sub: Sub; seen: boolean; resync: boolean };

const EVENTS = ['doc', 'asset', 'threads', 'gone', 'tree', 'counts', 'resync'];

const tabs = new Map<string, Tab>();
let token = '';
let url = '';
let es: EventSource | null = null;
let stream = ''; // the open stream's id; '' while connecting or down
let hello: unknown = null;

(self as unknown as { onconnect: (e: MessageEvent) => void }).onconnect = (e) => {
  const port = e.ports[0];
  port.onmessage = (m) => fromTab(port, m.data as FromTab);
  port.start();
};

function fromTab(port: MessagePort, m: FromTab) {
  if (m.type === 'bye') {
    drop(m.client);
    return;
  }
  const known = tabs.get(m.client);
  if (m.type === 'pong' && known) {
    known.seen = true;
    return;
  }
  // A new tab, one back from the cache, or one dropped for not answering
  // (a frozen tab), which has missed events and must resync.
  const t: Tab = { port, client: m.client, sub: m.sub, seen: true, resync: !!m.again || m.type === 'pong' };
  tabs.set(m.client, t);
  token = m.token;
  if (m.url !== url) {
    // A new token: serve restarted. The old stream cannot come back.
    url = m.url;
    close();
  }
  if (!es) open();
  else if (stream) {
    port.postMessage({ type: 'hello', hello });
    subscribe(t);
  }
}

function open() {
  const src = new EventSource(url);
  es = src;
  src.addEventListener('hello', (ev) => {
    const d = JSON.parse((ev as MessageEvent).data);
    // A second hello is a reconnect: the tabs missed whatever happened.
    const again = hello !== null;
    stream = d.stream;
    hello = d;
    for (const t of tabs.values()) {
      t.resync ||= again;
      t.port.postMessage({ type: 'hello', hello: d });
      subscribe(t);
    }
  });
  for (const name of EVENTS) {
    src.addEventListener(name, (ev) => {
      const d = JSON.parse((ev as MessageEvent).data);
      const msg = { type: 'event', event: name, data: d };
      if (d && typeof d.c === 'string') {
        msg.data = d.d;
        tabs.get(d.c)?.port.postMessage(msg);
      } else {
        for (const t of tabs.values()) t.port.postMessage(msg);
      }
    });
  }
  src.onerror = () => {
    stream = '';
    for (const t of tabs.values()) t.port.postMessage({ type: 'down' });
    // The browser retries by itself unless the server refused the stream.
    if (src.readyState === EventSource.CLOSED && es === src) es = null;
  };
}

// close lets the connection go. The tabs still open resync once the next
// one is up, since hello stays set.
function close() {
  es?.close();
  es = null;
  stream = '';
}

async function subscribe(t: Tab) {
  const s = stream;
  try {
    const res = await fetch('/_serve/api/live', {
      method: 'POST',
      headers: { 'X-Serve-Token': token, 'Content-Type': 'application/json' },
      body: JSON.stringify({ stream: s, client: t.client, ...t.sub }),
    });
    // A stream that went meanwhile resubscribes everyone when it is back.
    if (!res.ok || s !== stream || tabs.get(t.client) !== t) return;
    t.port.postMessage({ type: 'ready', resync: t.resync });
    t.resync = false;
  } catch {
    // Down; the stream's error handler has told the tabs.
  }
}

function drop(client: string) {
  if (!tabs.delete(client)) return;
  if (tabs.size === 0) {
    // No tabs left: let the connection go.
    close();
    hello = null;
    return;
  }
  if (!stream) return;
  fetch('/_serve/api/live', {
    method: 'DELETE',
    headers: { 'X-Serve-Token': token, 'Content-Type': 'application/json' },
    body: JSON.stringify({ stream, client }),
    keepalive: true,
  }).catch(() => {});
}

// A closed tab whose bye never came, or a frozen one, stops answering; it
// is dropped after missing one ping, and comes back with a pong if it thaws.
setInterval(() => {
  for (const t of [...tabs.values()]) {
    if (!t.seen) {
      drop(t.client);
      continue;
    }
    t.seen = false;
    t.port.postMessage({ type: 'ping' });
  }
}, 15000);
