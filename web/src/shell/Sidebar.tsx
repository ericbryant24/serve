// The file tree: one level loaded at a time, expansion remembered per folder,
// a count of open comments beside each file, and a filter box that searches
// every file under the folder.

import { useEffect, useLayoutEffect, useRef, useState } from 'preact/hooks';
import { Icon } from '../design/icons';
import serveMark from '../design/marks/serve.svg';
import { Docs } from '../api';
import { useStore, setState, setPrefs } from '../state';
import { onLive, clientId } from '../live';
import { errorToast, toast } from '../ui';
import { copyText, debounce } from '../util';
import type { Entry, RootInfo } from '../types';

const EXPAND_KEY = 'serve-tree:';
const TOPS_KEY = 'serve-tree-tops';
const SCROLL_KEY = 'serve-tree-scroll:';

const within = (p: string, d: string) => p === d || p.startsWith(d + '/');
const up = (s: string) => s.replace(/\/[^/]+\/?$/, '') || '/';

// The tree starts at the opened folder the server picks, unless the viewer
// chose a folder inside it. Chosen folders are kept per browser and never
// overlap, so at most one holds the current file.
function loadTops(): RootInfo[] {
  try {
    return JSON.parse(localStorage.getItem(TOPS_KEY) || '[]');
  } catch {
    return [];
  }
}

function treeTop(opened: RootInfo, current: string): RootInfo {
  return loadTops().find((t) => within(current, t.path) && within(t.path, opened.path)) || opened;
}

function saveTop(opened: RootInfo, top: RootInfo) {
  const rest = loadTops().filter((t) => !within(t.path, top.path) && !within(top.path, t.path));
  try {
    localStorage.setItem(TOPS_KEY, JSON.stringify(top.path === opened.path ? rest : [...rest, top]));
  } catch {}
}

// Folders the user opened, and folders on the way to the current file that
// the user closed (those are open by default).
type Expansion = { open: string[]; closed: string[] };

function loadExpanded(root: string): Expansion {
  try {
    const v = JSON.parse(localStorage.getItem(EXPAND_KEY + root) || '{}');
    return { open: v.open || [], closed: v.closed || [] };
  } catch {
    return { open: [], closed: [] };
  }
}

function saveExpanded(root: string, s: Expansion) {
  try {
    localStorage.setItem(EXPAND_KEY + root, JSON.stringify(s));
  } catch {}
}

function isOpen(x: Expansion, current: string, p: string): boolean {
  const onPath = (current + '/').startsWith(p + '/');
  return (x.open.includes(p) || onPath) && !x.closed.includes(p);
}

const cache = new Map<string, Entry[]>();
const listeners = new Set<() => void>();

async function load(dir: string, force = false): Promise<Entry[]> {
  if (!force && cache.has(dir)) return cache.get(dir)!;
  const r = await Docs.tree(dir, clientId);
  cache.set(dir, r.entries);
  listeners.forEach((l) => l());
  return r.entries;
}

function icon(e: Entry): string {
  if (e.dir) return 'folder';
  const ext = e.name.split('.').pop()?.toLowerCase() || '';
  if (['md', 'markdown'].includes(ext)) return 'file-text';
  if (['html', 'htm'].includes(ext)) return 'code';
  if (['png', 'jpg', 'jpeg', 'gif', 'svg', 'webp'].includes(ext)) return 'image';
  return 'file';
}

// The tree's width is dragged from its right edge and kept in the prefs. A
// width saved in a wider window is narrowed to fit this one.
const DEFAULT_WIDTH = 260;
const MIN_WIDTH = 180;

export function sidebarWidth(w: number): number {
  const max = Math.max(MIN_WIDTH, Math.min(640, Math.round(innerWidth * 0.6)));
  return Math.round(Math.min(max, Math.max(MIN_WIDTH, w || DEFAULT_WIDTH)));
}

// While dragging, only the tree's style changes; the width is saved on release,
// so the page does not re-render on every move.
function ResizeHandle({ nav, width }: { nav: { current: HTMLElement | null }; width: number }) {
  const down = (e: PointerEvent) => {
    const el = nav.current;
    if (e.button !== 0 || !el) return;
    e.preventDefault();
    const handle = e.currentTarget as HTMLElement;
    const offset = e.clientX - el.offsetWidth;
    let w = el.offsetWidth;
    handle.setPointerCapture(e.pointerId);
    document.documentElement.classList.add('resizing');
    const move = (ev: PointerEvent) => {
      w = sidebarWidth(ev.clientX - offset);
      el.style.width = w + 'px';
    };
    const up = () => {
      handle.removeEventListener('pointermove', move);
      handle.removeEventListener('pointerup', up);
      handle.removeEventListener('pointercancel', up);
      document.documentElement.classList.remove('resizing');
      setPrefs({ sidebarWidth: w });
    };
    handle.addEventListener('pointermove', move);
    handle.addEventListener('pointerup', up);
    handle.addEventListener('pointercancel', up);
  };
  const key = (e: KeyboardEvent) => {
    const step = e.key === 'ArrowLeft' ? -16 : e.key === 'ArrowRight' ? 16 : 0;
    if (!step) return;
    e.preventDefault();
    setPrefs({ sidebarWidth: sidebarWidth(width + step) });
  };
  return (
    <div
      class="sidebar-resize"
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize the file tree"
      aria-valuenow={width}
      aria-valuemin={MIN_WIDTH}
      title="Drag to resize, double-click to reset"
      tabIndex={0}
      onPointerDown={down}
      onDblClick={() => setPrefs({ sidebarWidth: DEFAULT_WIDTH })}
      onKeyDown={key}
    />
  );
}

type MenuState = { x: number; y: number; e: Entry } | null;

function Node({ e, depth, current, expanded, toggle, onMenu }: { e: Entry; depth: number; current: string; expanded: Expansion; toggle: (p: string) => void; onMenu: (m: MenuState) => void }) {
  const open = !!e.dir && isOpen(expanded, current, e.path);
  const [, force] = useState(0);
  useEffect(() => {
    const l = () => force((n) => n + 1);
    listeners.add(l);
    return () => listeners.delete(l);
  }, []);
  useEffect(() => {
    if (e.dir && open && !cache.has(e.path)) load(e.path).catch(() => {});
  }, [open]);
  const kids = e.dir && open ? cache.get(e.path) : undefined;
  const isCurrent = e.path === current;
  const ctx = (ev: MouseEvent) => {
    ev.preventDefault();
    onMenu({ x: ev.clientX, y: ev.clientY, e });
  };
  return (
    <li>
      {e.dir ? (
        <button type="button" class={'tree-row dir' + (open ? ' open' : '')} style={{ paddingLeft: 8 + depth * 16 + 'px' }} onClick={() => toggle(e.path)} onContextMenu={ctx} aria-expanded={open}>
          <Icon name="right" size={12} class="chev" />
          <Icon name="folder" />
          <span class="tree-name">{e.name}</span>
          {!!e.threads && <span class="count">{e.threads}</span>}
        </button>
      ) : (
        <a
          class={'tree-row file' + (isCurrent ? ' current' : '')}
          href={e.url}
          style={{ paddingLeft: 26 + depth * 16 + 'px' }}
          aria-current={isCurrent ? 'page' : undefined}
          draggable
          onDragStart={(ev) => {
            const u = new URL(e.url, location.href);
            u.searchParams.set('raw', '1');
            u.searchParams.set('dl', '1');
            ev.dataTransfer?.setData('DownloadURL', 'application/octet-stream:' + e.name + ':' + u.toString());
            ev.dataTransfer?.setData('text/plain', e.path);
            ev.dataTransfer?.setData('text/uri-list', 'file://' + encodeURI(e.path));
          }}
          onContextMenu={ctx}
        >
          <Icon name={icon(e)} />
          <span class="tree-name">{e.name}</span>
          {!!e.threads && <span class="count">{e.threads}</span>}
        </a>
      )}
      {kids && (
        <ul>
          {kids.map((k) => (
            <Node key={k.path} e={k} depth={depth + 1} current={current} expanded={expanded} toggle={toggle} onMenu={onMenu} />
          ))}
          {!kids.length && <li class="tree-empty" style={{ paddingLeft: 26 + (depth + 1) * 16 + 'px' }}>Empty</li>}
        </ul>
      )}
    </li>
  );
}

export function Sidebar() {
  const page = useStore((s) => s.page);
  const width = sidebarWidth(useStore((s) => s.prefs.sidebarWidth));
  const opened = page.root;
  const current = page.path || '';
  const [root, setRoot] = useState(() => opened && treeTop(opened, current));
  const [expanded, setExpanded] = useState<Expansion>(() => (root ? loadExpanded(root.path) : { open: [], closed: [] }));
  const [top, setTop] = useState<Entry[] | null>(root ? cache.get(root.path) || null : null);
  const [query, setQuery] = useState('');
  const [results, setResults] = useState<Entry[] | null>(null);
  const [menu, setMenu] = useState<MenuState>(null);
  const [sel, setSel] = useState(0);
  const input = useRef<HTMLInputElement>(null);
  const treeRef = useRef<HTMLDivElement>(null);
  const navRef = useRef<HTMLElement>(null);
  const placed = useRef('');

  useEffect(() => {
    if (!root) return;
    // The folders down to the current file load together, so the tree
    // first shows with the current file's row in it.
    const dirs = [root.path];
    for (let d = up(current); d !== root.path && within(d, root.path); d = up(d)) dirs.push(d);
    Promise.all(dirs.map((d) => load(d).catch(() => [] as Entry[]))).then(([t]) => setTop(t));
    const refresh = debounce(() => {
      [...cache.keys()].forEach((d) => load(d, true).then((r) => d === root.path && setTop(r)).catch(() => cache.delete(d)));
    }, 400);
    onLive({
      onTree: (dir) => load(dir, true).then((r) => dir === root.path && setTop(r)).catch(() => {}),
      onCounts: refresh,
    });
    const vis = () => document.visibilityState === 'visible' && refresh();
    document.addEventListener('visibilitychange', vis);
    return () => document.removeEventListener('visibilitychange', vis);
  }, [root?.path]);

  // Every file opens as a new page. The tree comes back scrolled where it was
  // on the last one, and moves only if the current file is out of view.
  useLayoutEffect(() => {
    const tree = treeRef.current;
    if (!tree || !top || !root || placed.current === root.path) return;
    placed.current = root.path;
    let saved: string | null = null;
    try {
      saved = sessionStorage.getItem(SCROLL_KEY + root.path);
    } catch {}
    if (saved !== null) tree.scrollTop = +saved;
    const row = tree.querySelector('.tree-row.current');
    if (!row) return;
    const r = row.getBoundingClientRect();
    const t = tree.getBoundingClientRect();
    if (r.top < t.top || r.bottom > t.bottom) row.scrollIntoView({ block: 'center' });
  }, [top, root?.path]);

  useEffect(() => {
    if (!root) return;
    const save = () => {
      const tree = treeRef.current;
      if (!tree || tree.querySelector('.results')) return;
      try {
        sessionStorage.setItem(SCROLL_KEY + root.path, String(tree.scrollTop));
      } catch {}
    };
    window.addEventListener('pagehide', save);
    return () => window.removeEventListener('pagehide', save);
  }, [root?.path]);

  const search = useRef(
    debounce(async (r: string, q: string) => {
      if (!q.trim()) return setResults(null);
      try {
        const res = await Docs.search(r, q);
        setResults(res.results);
        setSel(0);
      } catch (e) {
        errorToast(e);
      }
    }, 120),
  );

  useEffect(() => {
    const f = () => input.current?.focus();
    window.addEventListener('serve:filter', f);
    return () => window.removeEventListener('serve:filter', f);
  }, []);

  if (!root || !opened) return null;
  const startAt = (top: RootInfo) => {
    saveTop(opened, top);
    setRoot(top);
    setTop(cache.get(top.path) || null);
    setExpanded(loadExpanded(top.path));
    setQuery('');
    setResults(null);
  };
  const toggle = (p: string) => {
    const onPath = (current + '/').startsWith(p + '/');
    const open = isOpen(expanded, current, p);
    const next: Expansion = { open: expanded.open.filter((x) => x !== p), closed: expanded.closed.filter((x) => x !== p) };
    if (open && onPath) next.closed.push(p);
    if (!open && !onPath) next.open.push(p);
    setExpanded(next);
    saveExpanded(root.path, next);
  };

  // Inside the opened folder, ↑ only moves the tree up; at it, ↑ opens the
  // folder above in serve.
  const inside = root.path !== opened.path;
  const goUp = async () => {
    const path = up(root.path);
    if (inside) return startAt({ path, url: up(root.url), name: path.split('/').pop() || '/', display: up(root.display) });
    try {
      await Docs.openFolder(path);
      location.reload();
    } catch (e) {
      errorToast(e);
    }
  };
  const upLabel = inside ? 'Show the folder above' : 'Open the folder above';

  return (
    <nav class="sidebar" aria-label="Files" ref={navRef} style={{ width: width + 'px' }}>
      <div class="sidebar-head col-head">
        <a class="appmark" href="/" title="Start page" aria-label="Start page" dangerouslySetInnerHTML={{ __html: serveMark }} />
        <a class="sidebar-root" href={root.url} title={root.display}>
          {root.name}
        </a>
        <button type="button" class="icon" title={upLabel} aria-label={upLabel} onClick={goUp}>
          <Icon name="folder-up" />
        </button>
      </div>
      <label class="filter search">
        <Icon name="search" size={14} />
        <input
          ref={input}
          type="search"
          placeholder="Find a file"
          value={query}
          onInput={(e) => {
            const q = (e.target as HTMLInputElement).value;
            setQuery(q);
            search.current(root.path, q);
          }}
          onKeyDown={(e) => {
            if (e.key === 'Escape') {
              setQuery('');
              setResults(null);
              input.current?.blur();
            } else if (e.key === 'ArrowDown' && results) {
              e.preventDefault();
              setSel((n) => Math.min(n + 1, results.length - 1));
            } else if (e.key === 'ArrowUp' && results) {
              e.preventDefault();
              setSel((n) => Math.max(n - 1, 0));
            } else if (e.key === 'Enter' && results && results[sel]) {
              location.href = results[sel].url;
            }
          }}
        />
        {!query && <kbd>/</kbd>}
      </label>
      <div class="tree" ref={treeRef}>
        {results ? (
          <ul class="results">
            {results.map((r, i) => (
              <li key={r.path}>
                <a class={'tree-row file' + (i === sel ? ' selected' : '') + (r.path === current ? ' current' : '')} href={r.url}>
                  <Icon name={icon(r)} />
                  <span class="tree-name">{r.name}</span>
                  {!!r.threads && <span class="count">{r.threads}</span>}
                </a>
              </li>
            ))}
            {!results.length && <li class="tree-empty">No matching files</li>}
          </ul>
        ) : (
          <ul>
            {(top || []).map((e) => (
              <Node key={e.path} e={e} depth={0} current={current} expanded={expanded} toggle={toggle} onMenu={setMenu} />
            ))}
          </ul>
        )}
      </div>
      <ResizeHandle nav={navRef} width={width} />
      {menu && <ContextMenu m={menu} close={() => setMenu(null)} startAt={(e) => startAt({ path: e.path, url: e.url, name: e.name, display: root.display + e.path.slice(root.path.length) })} />}
    </nav>
  );
}

function ContextMenu({ m, close, startAt }: { m: NonNullable<MenuState>; close: () => void; startAt: (e: Entry) => void }) {
  useEffect(() => {
    const off = () => close();
    window.addEventListener('mousedown', off);
    window.addEventListener('scroll', off, true);
    window.addEventListener('blur', off);
    return () => {
      window.removeEventListener('mousedown', off);
      window.removeEventListener('scroll', off, true);
      window.removeEventListener('blur', off);
    };
  }, []);
  return (
    <div class="menu context" style={{ left: m.x + 'px', top: m.y + 'px' }} onMouseDown={(e) => e.stopPropagation()}>
      {m.e.dir && (
        <button type="button" onClick={() => (startAt(m.e), close())}>
          Start the file tree here
        </button>
      )}
      <button type="button" onClick={() => (copyText(m.e.path).then(() => toast('Copied path')), close())}>
        Copy path
      </button>
      <button type="button" onClick={() => (Docs.reveal(m.e.path).catch(errorToast), close())}>
        {m.e.dir ? 'Open in file manager' : 'Reveal in file manager'}
      </button>
      {!m.e.dir && (
        <a class="menu-item" href={m.e.url + '?raw=1'} target="_blank" rel="noopener" onClick={close}>
          Open raw
        </a>
      )}
    </div>
  );
}

export function toggleSidebar() {
  setState((s) => ({ prefs: { ...s.prefs, sidebar: !s.prefs.sidebar } }));
}
