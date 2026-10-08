import { useEffect, useRef, useState } from 'preact/hooks';
import { useStore, setState, setPrefs } from '../state';
import { Docs } from '../api';
import { errorToast, toast } from '../ui';
import { timeAgo, fullDate, copyText, plural } from '../util';
import { startComment } from '../commands';
import { toggleSidebar } from './Sidebar';
import { rerenderDiagrams } from '../doc/mermaid';
import { refs } from '../doc/DocView';
import type { Crumb } from '../types';

function Breadcrumb({ crumbs: all }: { crumbs: Crumb[] }) {
  // Long paths keep their first and last few folders; the rest fold into
  // one item that names the whole path on hover.
  let crumbs: (Crumb & { fold?: string })[] = all;
  if (all.length > 5) {
    const hidden = all.slice(1, all.length - 3);
    crumbs = [all[0], { name: '…', url: hidden[hidden.length - 1].url, opened: hidden[hidden.length - 1].opened, fold: hidden.map((h) => h.name).join(' / ') }, ...all.slice(-3)];
  }
  return (
    <ol class="crumbs" aria-label="Path">
      {crumbs.map((c, i) => (
        <li key={c.url + i} title={c.fold}>
          {i === crumbs.length - 1 ? (
            <span class="crumb current" aria-current="page">
              {c.name}
            </span>
          ) : (
            <a class={'crumb' + (c.opened ? '' : ' closed')} href={c.url} title={c.opened ? '' : 'Not open in serve'}>
              {c.name}
            </a>
          )}
        </li>
      ))}
    </ol>
  );
}

export function applyTheme(theme: string) {
  const el = document.documentElement;
  if (theme === 'light' || theme === 'dark') el.setAttribute('data-theme', theme);
  else el.removeAttribute('data-theme');
  try {
    localStorage.setItem('serve-theme', theme);
  } catch {}
  if (refs.root) rerenderDiagrams(refs.root);
}

function ViewMenu({ close }: { close: () => void }) {
  const prefs = useStore((s) => s.prefs);
  const page = useStore((s) => s.page);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const off = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && close();
    const key = (e: KeyboardEvent) => e.key === 'Escape' && (e.stopPropagation(), close());
    setTimeout(() => window.addEventListener('mousedown', off));
    window.addEventListener('keydown', key, true);
    return () => {
      window.removeEventListener('mousedown', off);
      window.removeEventListener('keydown', key, true);
    };
  }, []);
  const doc = page.doc;
  const owner = page.role === 'owner';
  return (
    <div class="menu view-menu" ref={ref} role="menu">
      {doc && (doc.kind === 'markdown' || doc.kind === 'code' || doc.kind === 'text') && (
        <>
          <div class="menu-label">Width</div>
          <div class="seg">
            {(['narrow', 'medium', 'wide', 'full'] as const).map((w) => (
              <button type="button" class={prefs.width === w ? 'on' : ''} onClick={() => setPrefs({ width: w })}>
                {w[0].toUpperCase() + w.slice(1)}
              </button>
            ))}
          </div>
          <div class="menu-label">Text size</div>
          <div class="seg">
            <button type="button" onClick={() => setPrefs({ textSize: Math.max(80, prefs.textSize - 10) })} aria-label="Smaller">
              A−
            </button>
            <button type="button" class="on" onClick={() => setPrefs({ textSize: 100 })}>
              {prefs.textSize}%
            </button>
            <button type="button" onClick={() => setPrefs({ textSize: Math.min(160, prefs.textSize + 10) })} aria-label="Larger">
              A+
            </button>
          </div>
        </>
      )}
      <div class="menu-label">Theme</div>
      <div class="seg">
        {(['system', 'light', 'dark'] as const).map((t) => (
          <button
            type="button"
            class={prefs.theme === t ? 'on' : ''}
            onClick={() => {
              setPrefs({ theme: t });
              applyTheme(t);
            }}
          >
            {t[0].toUpperCase() + t.slice(1)}
          </button>
        ))}
      </div>
      <div class="menu-sep" />
      {doc && (
        <>
          <label class="check">
            <input type="checkbox" checked={prefs.showHighlights} onChange={() => setPrefs({ showHighlights: !prefs.showHighlights })} /> Show highlights
          </label>
          <label class="check">
            <input type="checkbox" checked={prefs.showResolved} onChange={() => setPrefs({ showResolved: !prefs.showResolved })} /> Show resolved comments
          </label>
          <div class="menu-sep" />
          <button type="button" onClick={() => (copyText(page.path!).then(() => toast('Copied path')), close())}>
            Copy path
          </button>
          {owner && (
            <button type="button" onClick={() => (Docs.reveal(page.path!).catch(errorToast), close())}>
              Reveal in file manager
            </button>
          )}
          <a class="menu-item" href={doc.raw} target="_blank" rel="noopener" onClick={close}>
            Open raw file
          </a>
          <a class="menu-item" href={doc.raw + '&dl=1'} onClick={close}>
            Download
          </a>
          <div class="menu-sep" />
        </>
      )}
      <button type="button" onClick={() => (setState({ help: true }), close())}>
        Keyboard shortcuts <kbd>?</kbd>
      </button>
      {owner && (
        <button type="button" onClick={() => (setState({ report: true }), close())}>
          Report a problem
        </button>
      )}
      {owner && (
        <a class="menu-item" href="/" onClick={close}>
          Start page
        </a>
      )}
    </div>
  );
}

export function TopBar() {
  const page = useStore((s) => s.page);
  const threads = useStore((s) => s.threads);
  const commenting = useStore((s) => s.commenting);
  const editing = useStore((s) => s.editing);
  const changes = useStore((s) => s.changes);
  const narrow = useStore((s) => s.narrow);
  const panel = useStore((s) => s.panel);
  const connected = useStore((s) => s.connected);
  const [menu, setMenu] = useState(false);
  const [, tick] = useState(0);
  useEffect(() => {
    const i = window.setInterval(() => tick((n) => n + 1), 60000);
    return () => clearInterval(i);
  }, []);
  const doc = page.doc;
  const open = threads.filter((t) => t.status === 'open');
  const yours = open.filter((t) => t.awaiting === 'human').length;
  const framed = doc && doc.kind !== 'markdown' && doc.kind !== 'code' && doc.kind !== 'text';
  return (
    <header class="topbar">
      {page.root && (
        <button type="button" class="icon ghost" title="Toggle the file tree" aria-label="Toggle the file tree" onClick={toggleSidebar}>
          <span class="i-sidebar" aria-hidden="true" />
        </button>
      )}
      {page.view === 'home' ? <span class="brand">serve</span> : page.crumbs && <Breadcrumb crumbs={page.crumbs} />}
      <div class="spacer" />
      {!connected && <span class="offline" title="Reconnecting to serve…">Offline</span>}
      {doc?.modified && (
        <span class="meta" title={'Modified ' + fullDate(doc.modified)}>
          Edited {timeAgo(doc.modified)}
        </span>
      )}
      {doc && !editing && (
        <>
          {page.role === 'owner' && threads.some((t) => t.messages.some((m) => m.author.kind === 'human')) && doc.kind === 'markdown' && (
            <button type="button" class={'ghost' + (changes ? ' on' : '')} onClick={() => setState({ changes: !changes })} title="What changed since your last comment">
              Changes
            </button>
          )}
          {doc.is_marp && (
            <a class="button ghost" href={page.url + '?present=1'} target="_blank" rel="noopener">
              Present
            </a>
          )}
          {doc.editable && page.role === 'owner' && (
            <button type="button" class="ghost" onClick={() => setState({ editing: true })}>
              Edit
            </button>
          )}
          <button
            type="button"
            class={'comment-btn' + (commenting ? ' on' : '')}
            aria-pressed={commenting}
            title={`Comment (c) — select text first, or pick a block`}
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => startComment()}
          >
            <span class="i-comment" aria-hidden="true" /> Comment
          </button>
          {(narrow || framed) && (
            <button type="button" class={'ghost threads-btn' + (panel ? ' on' : '')} onClick={() => setState({ panel: !panel })} title="Show comments">
              {plural(open.length, 'comment')}
              {yours > 0 && <span class="dot" title={plural(yours, 'thread') + ' waiting for you'} />}
            </button>
          )}
        </>
      )}
      <div class="menu-wrap">
        <button type="button" class="icon ghost" aria-label="View options" aria-expanded={menu} onClick={() => setMenu(!menu)}>
          ⋯
        </button>
        {menu && <ViewMenu close={() => setMenu(false)} />}
      </div>
    </header>
  );
}

