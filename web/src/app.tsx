// serve's browser app. The server renders every page as the same shell with
// the page's data embedded; this script reads that data and draws the page.

import './styles/app.css';
import { render } from 'preact';
import { useEffect, useLayoutEffect, useRef } from 'preact/hooks';
import { readPageData } from './api';
import { startComment } from './commands';
import { initState, useStore, setState, getState } from './state';
import { installKeys } from './keys';
import { installCommands } from './commands';
import { connect } from './live';
import { Toasts } from './ui';
import { TopBar, applyTheme } from './shell/TopBar';
import { Sidebar } from './shell/Sidebar';
import { DocView, refs, relayout, scrollToThread } from './doc/DocView';
import { FrameView } from './doc/FrameView';
import { Margin, Panel } from './threads/Margin';
import { EditorView } from './views/Editor';
import { DirView, NotFoundView, ClosedView, MessageView, HomeView } from './views/Pages';
import { Help, ModeBar, GoneBanner, RelinkBanner, ChangesView } from './views/Extras';
import { ReportDialog } from './report/Report';

const WIDTHS = { narrow: '680px', medium: '840px', wide: '1080px', full: 'none' };

function FileInfo() {
  const page = useStore((s) => s.page);
  const doc = page.doc!;
  const size = doc.size < 1024 ? doc.size + ' B' : doc.size < 1048576 ? (doc.size / 1024).toFixed(1) + ' KB' : (doc.size / 1048576).toFixed(1) + ' MB';
  return (
    <div class="page-view file-info">
      <h1>{page.name}</h1>
      <p class="muted">
        {size}
        {doc.size > 8 << 20 ? ' · too large to show here' : " · serve can't show this kind of file"}
      </p>
      <p>
        <a class="button primary" href={doc.raw + '&dl=1'}>
          Download
        </a>{' '}
        <a class="button ghost" href={doc.raw} target="_blank" rel="noopener">
          Open in a new tab
        </a>
      </p>
    </div>
  );
}

function DocArea() {
  const doc = useStore((s) => s.page.doc)!;
  const editing = useStore((s) => s.editing);
  const changes = useStore((s) => s.changes);
  const narrow = useStore((s) => s.narrow);
  const contentOrigin = useStore((s) => s.page.content_origin);
  const name = useStore((s) => s.page.name);
  const pageRef = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    refs.page = pageRef.current;
    relayout();
  });
  if (doc.kind === 'html') return <FrameView />;
  // The revision in the URL fetches the file again when it changes.
  if (doc.kind === 'pdf') return <iframe class="frame" title={doc.title || 'PDF'} src={contentOrigin + doc.raw + '&v=' + doc.rev} />;
  if (doc.kind === 'image')
    return (
      <div class="image-view">
        <img src={doc.raw + '&v=' + doc.rev} alt={name} />
      </div>
    );
  if (doc.kind === 'binary') return <FileInfo />;
  return (
    <>
      {editing && <EditorView />}
      {changes && !editing && <ChangesView />}
      <div class="page" ref={pageRef} style={editing || changes ? { display: 'none' } : undefined}>
        <div class="doc-col">
          <DocView />
        </div>
        {!narrow && <Margin />}
      </div>
    </>
  );
}

// EmbedBar is all the chrome an embedded page has: comment, the thread
// count, and a way out to serve itself.
function EmbedBar() {
  const threads = useStore((s) => s.threads);
  const commenting = useStore((s) => s.commenting);
  const narrow = useStore((s) => s.narrow);
  const panel = useStore((s) => s.panel);
  const url = useStore((s) => s.page.url);
  const open = threads.filter((t) => t.status === 'open').length;
  return (
    <div class="embed-bar">
      <button type="button" class={'comment-btn' + (commenting ? ' on' : '')} title="Comment (c)" onMouseDown={(e) => e.preventDefault()} onClick={() => startComment()}>
        <span class="i-comment" aria-hidden="true" /> Comment
      </button>
      {narrow && (
        <button type="button" class={'ghost threads-btn' + (panel ? ' on' : '')} onClick={() => setState({ panel: !panel })}>
          {open} open
        </button>
      )}
      <a class="button ghost" href={url} target="_blank" rel="noopener" title="Open in serve">
        ↗
      </a>
    </div>
  );
}

function App() {
  const view = useStore((s) => s.page.view);
  const root = useStore((s) => s.page.root);
  const prefs = useStore((s) => s.prefs);
  const panel = useStore((s) => s.panel);
  const narrow = useStore((s) => s.narrow);
  const editing = useStore((s) => s.editing);
  const kind = useStore((s) => s.page.doc?.kind);
  const embedded = useStore((s) => !!s.page.embedded);
  const scroller = useRef<HTMLElement>(null);

  useEffect(() => {
    refs.scroller = scroller.current;
    const measure = () => {
      const s = getState();
      const avail = innerWidth - (s.page.root && s.prefs.sidebar && !s.page.embedded ? 270 : 0);
      const n = avail < 1060;
      if (n !== s.narrow) setState({ narrow: n });
    };
    measure();
    window.addEventListener('resize', measure);
    return () => window.removeEventListener('resize', measure);
  }, [prefs.sidebar]);

  // A link to a thread (#thread-<id>) opens it.
  useEffect(() => {
    const open = () => {
      const m = location.hash.match(/^#thread-([0-9a-z]+)/);
      if (m && getState().threads.some((t) => t.id === m[1])) {
        setState({ active: m[1], panel: getState().narrow || getState().panel });
        setTimeout(() => scrollToThread(m[1]), 50);
      }
    };
    open();
    window.addEventListener('hashchange', open);
    return () => window.removeEventListener('hashchange', open);
  }, []);

  const framed = kind === 'html' || kind === 'pdf' || kind === 'image' || kind === 'binary';
  const showPanel = view === 'doc' && !editing && panel && (narrow || framed);
  const style = { '--doc-max': WIDTHS[prefs.width], '--text-scale': prefs.textSize / 100 } as any;
  return (
    <div class={'app view-' + view + (root && prefs.sidebar && !embedded ? ' with-sidebar' : '') + (showPanel ? ' with-panel' : '') + (framed ? ' framed' : '') + (embedded ? ' embedded' : '')} style={style}>
      {!embedded && <TopBar />}
      <div class="app-body">
        {root && prefs.sidebar && !embedded && <Sidebar />}
        <main class="main" ref={scroller} onScroll={relayout}>
          {embedded && view === 'doc' && <EmbedBar />}
          <GoneBanner />
          <RelinkBanner />
          {view === 'doc' && <DocArea />}
          {view === 'dir' && <DirView />}
          {view === 'notfound' && <NotFoundView />}
          {view === 'closed' && <ClosedView />}
          {view === 'message' && <MessageView />}
          {view === 'home' && <HomeView />}
        </main>
        {showPanel && <Panel />}
      </div>
      <ModeBar />
      <Help />
      <ReportDialog />
      <Toasts />
    </div>
  );
}

function boot() {
  const page = readPageData();
  initState(page);
  applyTheme(getState().prefs.theme);
  installKeys();
  installCommands();
  render(<App />, document.getElementById('app')!);
  if (page.view === 'doc' || page.view === 'home' || page.view === 'dir') connect();
  const open = page.threads.filter((t) => t.status === 'open').length;
  if (open && page.view === 'doc') document.title = `(${open}) ` + document.title;
}

boot();
