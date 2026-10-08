// The app's keyboard commands, and the actions buttons share with them.

import { register } from './keys';
import { getState, setState, visibleThreads } from './state';
import { currentSelection, draftFromSelection, scrollToThread } from './doc/DocView';
import { focusReply, resolveThread } from './threads/Thread';
import { frameCommand } from './doc/FrameView';
import { saveEditor } from './views/Editor';

// startComment is what "c" and the Comment button do: comment on the
// selection when there is one, otherwise switch comment mode on or off.
export function startComment() {
  const s = getState();
  if (s.page.view !== 'doc' || s.editing) return;
  const kind = s.page.doc?.kind;
  if (kind === 'html') {
    frameCommand('comment');
    return;
  }
  if (kind === 'pdf' || kind === 'image' || kind === 'binary') {
    setState({ draft: { kind: 'page', top: 0 }, panel: true });
    return;
  }
  const r = currentSelection();
  if (r && draftFromSelection(r)) {
    if (s.narrow) setState({ panel: true });
    return;
  }
  setState({ commenting: !s.commenting, draft: null });
}

export function commentOnPage() {
  setState((s) => ({ draft: { kind: 'page', top: 0 }, commenting: false, panel: s.narrow || s.panel }));
}

function step(dir: 1 | -1) {
  const s = getState();
  const list = visibleThreads(s)
    .filter((t) => t.scope !== 'page')
    .sort((a, b) => (a.location.line_start ?? 0) - (b.location.line_start ?? 0));
  if (!list.length) return;
  const i = list.findIndex((t) => t.id === s.active);
  const next = list[(i + dir + list.length) % list.length];
  setState({ active: next.id, draft: null });
  scrollToThread(next.id);
  if (s.page.doc?.kind === 'html') frameCommand('focus', next.id);
}

export function installCommands() {
  register({ key: 'c', label: 'Comment on the selection, or turn comment mode on and off', run: () => startComment() });
  register({
    key: 'Escape',
    label: 'Close, cancel, or leave comment mode',
    inInputs: true,
    run: (e) => {
      const s = getState();
      const t = document.activeElement as HTMLElement | null;
      if (t && (t.tagName === 'TEXTAREA' || t.tagName === 'INPUT')) {
        t.blur();
        return;
      }
      if (s.help) return void setState({ help: false });
      if (s.draft) return void setState({ draft: null });
      if (s.commenting) return void setState({ commenting: false });
      if (s.active) return void setState({ active: null });
      if (s.panel && s.narrow) return void setState({ panel: false });
      if (s.changes) return void setState({ changes: false });
      return false;
    },
  });
  register({ key: ']', label: 'Next comment', run: () => step(1) });
  register({ key: '[', label: 'Previous comment', run: () => step(-1) });
  register({
    key: 'r',
    label: 'Reply to the comment in focus',
    run: () => {
      const s = getState();
      if (!s.active) return false;
      return focusReply(s.active);
    },
  });
  register({
    key: 'e',
    label: 'Resolve (or reopen) the comment in focus',
    run: () => {
      const s = getState();
      const t = s.threads.find((x) => x.id === s.active);
      if (!t) return false;
      resolveThread(t);
    },
  });
  register({ key: 'p', label: 'Comment on the whole page', run: () => (getState().page.view === 'doc' ? commentOnPage() : false) });
  register({ key: '/', label: 'Find a file', run: () => void window.dispatchEvent(new Event('serve:filter')) });
  register({ key: '?', label: 'Keyboard shortcuts', run: () => setState((s) => ({ help: !s.help })) });
  register({
    key: 'mod+s',
    label: 'Save (while editing)',
    inInputs: true,
    hidden: true,
    run: () => {
      if (!getState().editing || !saveEditor) return false;
      saveEditor();
    },
  });
}
