// Smaller pieces around a document: the keyboard help, the comment-mode bar,
// "changes since my last comment", and banners for a missing file and for
// comments that may belong to this file.

import { useEffect, useState } from 'preact/hooks';
import { useStore, setState } from '../state';
import { list } from '../keys';
import { Docs } from '../api';
import { errorToast, toast } from '../ui';
import { timeAgo, plural } from '../util';
import { commentOnPage } from '../commands';
import { renderDiagrams } from '../doc/mermaid';

export function Help() {
  const open = useStore((s) => s.help);
  if (!open) return null;
  return (
    <div class="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && setState({ help: false })}>
      <div class="modal help" role="dialog" aria-label="Keyboard shortcuts">
        <h2>Keyboard shortcuts</h2>
        <table>
          <tbody>
            {list().map((c) => (
              <tr key={c.key}>
                <td>
                  <kbd>{c.key === 'Escape' ? 'Esc' : c.key.replace('mod+', '⌘ ')}</kbd>
                </td>
                <td>{c.label}</td>
              </tr>
            ))}
          </tbody>
        </table>
        <div class="modal-actions">
          <button type="button" class="primary" onClick={() => setState({ help: false })}>
            Done
          </button>
        </div>
      </div>
    </div>
  );
}

export function ModeBar() {
  const on = useStore((s) => s.commenting);
  if (!on) return null;
  return (
    <div class="mode-bar" role="status">
      <span>Click a block or select text to comment</span>
      <button type="button" class="ghost" onClick={commentOnPage}>
        Whole page
      </button>
      <button type="button" class="ghost" onClick={() => setState({ commenting: false })}>
        Done <kbd>Esc</kbd>
      </button>
    </div>
  );
}

export function GoneBanner() {
  const gone = useStore((s) => s.gone);
  if (!gone) return null;
  return (
    <div class="banner warn">
      This file was moved or deleted.{' '}
      <button type="button" class="link" onClick={() => location.reload()}>
        Find it
      </button>
    </div>
  );
}

export function RelinkBanner() {
  const page = useStore((s) => s.page);
  const threads = useStore((s) => s.threads);
  const [hidden, setHidden] = useState(false);
  const c = page.relink?.[0];
  if (!c || hidden || threads.length) return null;
  const move = async () => {
    try {
      const r = await Docs.relink(c.id, page.path!);
      setState({ threads: r.threads });
      toast('Moved ' + plural(c.threads, 'comment') + ' here');
    } catch (e) {
      errorToast(e);
    }
  };
  return (
    <div class="banner">
      {plural(c.threads, 'comment')} from <code>{c.display}</code>, which no longer exists. Is this the same file?{' '}
      <button type="button" class="primary small" onClick={move}>
        Move them here
      </button>{' '}
      <button type="button" class="link" onClick={() => setHidden(true)}>
        No
      </button>
    </div>
  );
}

export function ChangesView() {
  const path = useStore((s) => s.page.path!);
  const rev = useStore((s) => s.rev);
  const [data, setData] = useState<Awaited<ReturnType<typeof Docs.changes>> | null>(null);
  useEffect(() => {
    Docs.changes(path).then(setData).catch(errorToast);
  }, [rev]);
  useEffect(() => {
    const el = document.querySelector('.changes-view');
    if (el) renderDiagrams(el);
  }, [data]);
  if (!data) return <div class="page-view muted">Loading changes…</div>;
  if (!data.available) return <div class="banner">serve has no copy of this file from your last comment, so it cannot show what changed.</div>;
  const blocks = data.blocks || [];
  const changed = blocks.filter((b) => b.kind !== 'same').length;
  return (
    <div class="changes-view">
      <div class="banner">
        {data.unchanged || !changed ? 'Nothing has changed' : plural(changed, 'block') + ' changed'} since your last comment ({timeAgo(data.since || '')}).{' '}
        <button type="button" class="link" onClick={() => setState({ changes: false })}>
          Back to the document
        </button>
      </div>
      <article class="markdown-body">
        {blocks.map((b, i) => (
          <div key={i} class={'change change-' + b.kind} dangerouslySetInnerHTML={{ __html: b.html }} />
        ))}
      </article>
    </div>
  );
}
