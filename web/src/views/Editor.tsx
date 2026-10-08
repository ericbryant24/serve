// Editing a file in the browser. Saving sends the revision the editor
// started from; if the file changed on disk in the meantime (an agent edited
// it), serve says so and offers to keep yours, take theirs, or merge.
// Nothing is ever discarded without asking.

import { useEffect, useRef, useState } from 'preact/hooks';
import { Docs, ApiError } from '../api';
import { useStore, setState } from '../state';
import { ask, errorToast, toast } from '../ui';
import { debounce, modKey } from '../util';
import { renderDiagrams } from '../doc/mermaid';
import { resync } from '../live';
import type { EditorHandle } from '../editor';

// The open editor's save, for the app-wide ⌘S.
export let saveEditor: (() => void) | null = null;

export function EditorView() {
  const page = useStore((s) => s.page);
  const doc = page.doc!;
  const host = useRef<HTMLDivElement>(null);
  const preview = useRef<HTMLDivElement>(null);
  const ed = useRef<EditorHandle | null>(null);
  const base = useRef({ text: '', rev: '' });
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [split, setSplit] = useState(doc.kind === 'markdown');
  const [stale, setStale] = useState(false);
  const isMd = doc.kind === 'markdown';

  const updatePreview = useRef(
    debounce(async (text: string) => {
      if (!preview.current) return;
      try {
        const r = await Docs.preview(page.path!, text);
        preview.current.innerHTML = r.html;
        if (r.has_mermaid) renderDiagrams(preview.current);
      } catch {}
    }, 250),
  );

  useEffect(() => {
    let alive = true;
    (async () => {
      try {
        const [{ content, rev }, mod] = await Promise.all([Docs.file(page.path!), import('../editor')]);
        if (!alive || !host.current) return;
        base.current = { text: content, rev };
        ed.current = mod.mountEditor(host.current, {
          text: content,
          markdown: isMd,
          onChange: (t) => {
            setDirty(t !== base.current.text);
            if (isMd) updatePreview.current(t);
          },
          onSave: () => save(),
          onScroll: (f) => {
            const p = preview.current;
            if (p) p.scrollTop = f * (p.scrollHeight - p.clientHeight);
          },
        });
        ed.current.focus();
        if (isMd) updatePreview.current(content);
      } catch (e) {
        errorToast(e);
        setState({ editing: false });
      }
    })();
    const beforeUnload = (e: BeforeUnloadEvent) => {
      if (ed.current && ed.current.getText() !== base.current.text) {
        e.preventDefault();
        e.returnValue = '';
      }
    };
    window.addEventListener('beforeunload', beforeUnload);
    saveEditor = () => void save();
    return () => {
      alive = false;
      saveEditor = null;
      window.removeEventListener('beforeunload', beforeUnload);
      ed.current?.destroy();
    };
  }, []);

  // A change on disk while editing: say so; the save that follows decides.
  useEffect(() => {
    if (base.current.rev && doc.rev !== base.current.rev) setStale(true);
  }, [doc.rev]);

  const resolving = useRef(false);
  const save = async (force = false): Promise<boolean> => {
    if (!ed.current || resolving.current) return false;
    const text = ed.current.getText();
    setSaving(true);
    try {
      const r = await Docs.save(page.path!, text, base.current.rev, force);
      base.current = { text, rev: r.rev };
      setDirty(false);
      setStale(false);
      toast('Saved');
      return true;
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        resolving.current = true;
        try {
          return await conflict(text, e.body.content, e.body.rev);
        } finally {
          resolving.current = false;
        }
      }
      errorToast(e);
      return false;
    } finally {
      setSaving(false);
    }
  };

  const conflict = async (mine: string, theirs: string, theirRev: string): Promise<boolean> => {
    const choice = await ask(
      'This file changed on disk',
      'Someone (or an agent) saved a different version while you were editing.',
      [
        { label: 'Merge both', value: 'merge', primary: true },
        { label: 'Use theirs', value: 'theirs' },
        { label: 'Overwrite with mine', value: 'mine', danger: true },
      ],
      'cancel',
    );
    if (choice === 'mine') {
      resolving.current = false;
      return save(true);
    }
    if (choice === 'theirs') {
      base.current = { text: theirs, rev: theirRev };
      ed.current!.setText(theirs);
      setDirty(false);
      setStale(false);
      return false;
    }
    if (choice === 'merge') {
      try {
        const m = await Docs.merge(base.current.text, mine, theirs);
        base.current = { text: theirs, rev: theirRev };
        ed.current!.setText(m.merged);
        ed.current!.focus();
        setStale(false);
        toast(m.clean ? 'Merged. Review and save.' : `Merged, but ${m.failed} change${m.failed === 1 ? '' : 's'} could not be applied. Review before saving.`, { ms: 6000 });
      } catch (e) {
        errorToast(e);
      }
    }
    return false;
  };

  const close = async () => {
    if (ed.current && ed.current.getText() !== base.current.text) {
      const c = await ask('Save your changes?', '', [
        { label: 'Save', value: 'save', primary: true },
        { label: "Don't save", value: 'discard', danger: true },
        { label: 'Keep editing', value: 'cancel' },
      ], 'cancel');
      if (c === 'cancel') return;
      if (c === 'save' && !(await save())) return;
    }
    setState({ editing: false });
    resync();
  };

  return (
    <div class={'editor' + (split ? ' split' : '')}>
      <div class="editor-bar">
        <button type="button" class="primary" disabled={!dirty || saving} onClick={() => save()}>
          {saving ? 'Saving…' : 'Save'}
        </button>
        <button type="button" class="ghost" onClick={close}>
          Close
        </button>
        {isMd && (
          <button type="button" class={'ghost' + (split ? ' on' : '')} onClick={() => setSplit(!split)}>
            Preview
          </button>
        )}
        <span class="hint">{modKey}+S saves</span>
        {stale && <span class="banner-inline warn">Changed on disk since you opened it; saving will offer to merge.</span>}
        {dirty && !stale && <span class="hint">Unsaved changes</span>}
      </div>
      <div class="editor-body">
        <div class="editor-host" ref={host} />
        {split && <div class="editor-preview markdown-body" ref={preview} />}
      </div>
    </div>
  );
}

