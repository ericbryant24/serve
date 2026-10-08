// HTML files run in an iframe served from a second origin (the content
// port), so a page of unknown origin cannot read serve's pages or call its
// API. A small script inside the frame (embed.ts) draws the highlights and
// reports selections and clicks back here by postMessage.

import { useEffect, useRef } from 'preact/hooks';
import { getState, setState, useStore, visibleThreads } from '../state';
import { dispatch } from '../keys';
import { onLive } from '../live';
import type { ElementPayload, SelectionPayload } from '../types';

let frame: HTMLIFrameElement | null = null;
let origin = '';

function post(msg: any) {
  frame?.contentWindow?.postMessage(msg, origin);
}

// frameCommand passes a command to the page in the frame.
export function frameCommand(name: 'comment' | 'focus', id?: string) {
  if (name === 'comment') {
    if (getState().commenting) setState({ commenting: false });
    else post({ type: 'serve:comment' });
  } else post({ type: 'serve:focus', id });
}

export function FrameView() {
  const doc = useStore((s) => s.page.doc)!;
  const contentOrigin = useStore((s) => s.page.content_origin)!;
  const threads = useStore((s) => s.threads);
  const active = useStore((s) => s.active);
  const commenting = useStore((s) => s.commenting);
  const prefs = useStore((s) => s.prefs);
  const ref = useRef<HTMLIFrameElement>(null);
  origin = contentOrigin;

  const sendThreads = () => {
    const s = getState();
    post({
      type: 'serve:threads',
      threads: s.prefs.showHighlights ? visibleThreads(s) : [],
      active: s.active,
    });
    post({ type: 'serve:mode', on: s.commenting });
  };

  useEffect(() => {
    frame = ref.current;
    const onMsg = (e: MessageEvent) => {
      if (e.origin !== contentOrigin || e.source !== frame?.contentWindow) return;
      const m = e.data || {};
      switch (m.type) {
        case 'serve:ready':
          sendThreads();
          break;
        case 'serve:select':
          setState({ draft: { kind: 'text', selection: m.selection as SelectionPayload, top: 0 }, panel: true, commenting: false, active: null });
          break;
        case 'serve:element':
          setState({ draft: { kind: 'element', element: m.element as ElementPayload, label: m.label, top: 0 }, panel: true, commenting: false, active: null });
          break;
        case 'serve:no-selection':
          setState({ commenting: true, draft: null });
          break;
        case 'serve:click':
          setState({ active: m.id, panel: true, draft: null });
          break;
        case 'serve:key':
          dispatch(new KeyboardEvent('keydown', { key: m.key, shiftKey: m.shiftKey }));
          break;
      }
    };
    window.addEventListener('message', onMsg);
    onLive({
      onDoc: (e) => {
        setState({ threads: e.threads, rev: e.rev });
        if (frame) frame.src = contentOrigin + doc.embed + '&t=' + Date.now();
      },
      onAsset: () => {
        if (frame) frame.src = contentOrigin + doc.embed + '&t=' + Date.now();
      },
      onResync: () => {
        if (frame) frame.src = contentOrigin + doc.embed + '&t=' + Date.now();
      },
    });
    setState({ panel: true });
    return () => window.removeEventListener('message', onMsg);
  }, []);

  useEffect(sendThreads, [threads, active, commenting, prefs.showHighlights, prefs.showResolved]);

  return (
    <iframe
      ref={ref}
      class="frame"
      title={doc.title || 'page'}
      src={contentOrigin + doc.embed}
      sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-modals allow-downloads allow-popups-to-escape-sandbox"
    />
  );
}
