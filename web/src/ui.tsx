// Toasts (with undo) and modal dialogs, shared by the whole app.

import { render } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';

type Toast = { id: number; text: string; action?: { label: string; run: () => void }; error?: boolean };

let toasts: Toast[] = [];
let toastListeners = new Set<() => void>();
let nextToast = 1;

export function toast(text: string, opts: { action?: Toast['action']; error?: boolean; ms?: number } = {}) {
  const t: Toast = { id: nextToast++, text, action: opts.action, error: opts.error };
  toasts = [...toasts.slice(-3), t];
  toastListeners.forEach((l) => l());
  window.setTimeout(() => dismissToast(t.id), opts.ms ?? (opts.action ? 6000 : 3500));
  return t.id;
}

export function dismissToast(id: number) {
  toasts = toasts.filter((t) => t.id !== id);
  toastListeners.forEach((l) => l());
}

export function errorToast(err: unknown) {
  const msg = err instanceof Error ? err.message : String(err);
  toast(msg, { error: true, ms: 6000 });
}

// withUndo runs commit after a delay unless the toast's Undo is pressed;
// revert puts the UI back. It is how deletes and resolves stay reversible
// without the server keeping deleted things around.
export function withUndo(text: string, commit: () => Promise<unknown>, revert: () => void, ms = 5000) {
  let undone = false;
  const id = toast(text, {
    ms,
    action: {
      label: 'Undo',
      run: () => {
        undone = true;
        dismissToast(id);
        revert();
      },
    },
  });
  window.setTimeout(() => {
    if (!undone) commit().catch((e) => {
      revert();
      errorToast(e);
    });
  }, ms);
}

export function Toasts() {
  const [, force] = useState(0);
  useEffect(() => {
    const l = () => force((n) => n + 1);
    toastListeners.add(l);
    return () => toastListeners.delete(l);
  }, []);
  return (
    <div class="toasts" role="status" aria-live="polite">
      {toasts.map((t) => (
        <div key={t.id} class={'toast' + (t.error ? ' toast-error' : '')}>
          <span>{t.text}</span>
          {t.action && (
            <button
              type="button"
              class="toast-action"
              onClick={() => {
                t.action!.run();
                dismissToast(t.id);
              }}
            >
              {t.action.label}
            </button>
          )}
        </div>
      ))}
    </div>
  );
}

// --- dialogs -----------------------------------------------------------------

export type DialogButton<T> = { label: string; value: T; primary?: boolean; danger?: boolean };

// ask shows a modal with buttons and resolves with the chosen value (or the
// cancel value on Escape).
export function ask<T>(title: string, body: string, buttons: DialogButton<T>[], cancel: T): Promise<T> {
  return new Promise((resolve) => {
    const host = document.createElement('div');
    document.body.appendChild(host);
    const done = (v: T) => {
      render(null, host);
      host.remove();
      resolve(v);
    };
    render(<Dialog title={title} body={body} buttons={buttons} onDone={done} cancel={cancel} />, host);
  });
}

function Dialog<T>(p: { title: string; body: string; buttons: DialogButton<T>[]; onDone: (v: T) => void; cancel: T }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    const primary = ref.current?.querySelector('button.primary') as HTMLButtonElement | null;
    (primary || (ref.current?.querySelector('button') as HTMLButtonElement | null))?.focus();
    const key = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        p.onDone(p.cancel);
      }
    };
    window.addEventListener('keydown', key, true);
    return () => {
      window.removeEventListener('keydown', key, true);
      prev?.focus();
    };
  }, []);
  return (
    <div class="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && p.onDone(p.cancel)}>
      <div class="modal" role="dialog" aria-modal="true" aria-label={p.title} ref={ref}>
        <h2>{p.title}</h2>
        {p.body && <p>{p.body}</p>}
        <div class="modal-actions">
          {p.buttons.map((b) => (
            <button type="button" class={(b.primary ? 'primary' : '') + (b.danger ? ' danger' : '')} onClick={() => p.onDone(b.value)}>
              {b.label}
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}
