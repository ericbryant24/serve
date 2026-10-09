// Threads beside the document, each level with its highlight and pushed
// down when it would overlap the one above, as in Google Docs. The thread in
// focus sits exactly at its anchor and the others make room around it. On a
// narrow window, and on pages shown in a frame, the same threads are a panel
// instead.

import { useEffect, useLayoutEffect, useRef } from 'preact/hooks';
import { Threads } from '../api';
import { getState, setState, useStore, visibleThreads } from '../state';
import { errorToast } from '../ui';
import { refs } from '../doc/DocView';
import { ThreadCard } from './Thread';
import { Composer } from './Composer';
import { plural } from '../util';
import type { Thread, Draft } from '../types';

const GAP = 10;

function anchored(t: Thread): boolean {
  return t.scope !== 'page' && t.location.state !== 'unplaced' && t.location.state !== 'dom';
}

export function DraftCard({ draft, path }: { draft: Draft; path: string }) {
  const label =
    draft.kind === 'page' ? 'Whole page' : draft.kind === 'element' ? draft.label : '“' + draft.selection.quote.replace(/\s+/g, ' ').trim().slice(0, 120) + '”';
  const key =
    draft.kind === 'text' ? 'new:' + path + ':' + draft.selection.quote.slice(0, 60) : draft.kind === 'element' ? 'new:' + path + ':' + draft.element.key : 'new:' + path + ':page';
  return (
    <div class="thread draft active" data-thread="__draft">
      <div class="thread-head">
        <div class={'quote' + (draft.kind === 'page' ? ' quote-page' : '')}>{label}</div>
      </div>
      <Composer
        key={JSON.stringify(draft.kind === 'text' ? draft.selection : draft.kind === 'element' ? draft.element : 'page')}
        autoFocus
        placeholder={draft.kind === 'page' ? 'Comment on the whole page…' : 'Add a comment…'}
        submitLabel="Comment"
        draftKey={key}
        onCancel={() => setState({ draft: null })}
        onSubmit={async (text) => {
          try {
            let t: Thread;
            if (draft.kind === 'text') t = await Threads.createText(path, draft.selection, text);
            else if (draft.kind === 'element') t = await Threads.createElement(path, draft.element, text);
            else t = await Threads.createPage(path, text);
            setState((s) => ({ threads: [...s.threads.filter((x) => x.id !== t.id), t], draft: null, active: t.id }));
          } catch (e) {
            errorToast(e);
            throw e;
          }
        }}
      />
    </div>
  );
}

export function Margin() {
  const threads = useStore((s) => s.threads);
  const active = useStore((s) => s.active);
  const hover = useStore((s) => s.hover);
  const draft = useStore((s) => s.draft);
  const showResolved = useStore((s) => s.prefs.showResolved);
  const path = useStore((s) => s.page.path!);
  const col = useRef<HTMLDivElement>(null);
  const vis = visibleThreads(getState());
  // A resolved thread goes below the open ones, unless it is the one in focus.
  const atEnd = (t: Thread) => t.status === 'resolved' && t.id !== active;
  const top = vis.filter((t) => !anchored(t) && !atEnd(t));
  const flow = vis.filter((t) => anchored(t) || atEnd(t));

  const layout = () => {
    const root = refs.root, page = refs.page, c = col.current;
    if (!root || !page || !c) return;
    // Positions are relative to the margin column; drafts record theirs
    // relative to the page, which starts higher by the page's top padding.
    const colTop = c.getBoundingClientRect().top;
    const pageTop = page.getBoundingClientRect().top;
    const cards = Array.from(c.querySelectorAll(':scope > .thread-slot')) as HTMLElement[];
    type Item = { el: HTMLElement; want: number; h: number; id: string; atEnd: boolean };
    const items: Item[] = [];
    let fixed = 0;
    for (const el of cards) {
      const id = el.dataset.thread!;
      const atEnd = el.classList.contains('resolved-slot');
      let want: number | null;
      if (id === '__draft') want = (getState().draft?.top ?? 0) + pageTop - colTop;
      else if (el.classList.contains('unanchored')) want = null;
      else {
        const r = root.querySelector(`[data-thread="${CSS.escape(id)}"], [data-cm-el~="${CSS.escape(id)}"]`);
        want = r ? r.getBoundingClientRect().top - colTop : null;
      }
      if (want === null && !atEnd) {
        // Page comments and ones that could not be placed stack at the top.
        el.style.top = fixed + 'px';
        el.style.opacity = '1';
        fixed += el.offsetHeight + GAP;
        continue;
      }
      items.push({ el, want: want ?? 0, h: el.offsetHeight, id, atEnd });
    }
    // Open threads first, in document order, then the resolved ones, so a
    // resolved thread never pushes an open one down.
    items.sort((a, b) => Number(a.atEnd) - Number(b.atEnd) || a.want - b.want);
    const pos = items.map((i) => Math.max(i.want, 0));
    const focus = items.findIndex((i) => i.id === (getState().draft ? '__draft' : getState().active));
    const start = Math.max(fixed, 0);
    if (focus >= 0) {
      pos[focus] = Math.max(items[focus].want, start);
      for (let i = focus + 1; i < items.length; i++) pos[i] = Math.max(pos[i], pos[i - 1] + items[i - 1].h + GAP);
      for (let i = focus - 1; i >= 0; i--) pos[i] = Math.min(pos[i], pos[i + 1] - items[i].h - GAP);
      // Anything pushed above the top (or into the stacked group) flows down again.
      let floor = start;
      for (let i = 0; i < items.length; i++) {
        if (pos[i] < floor) pos[i] = floor;
        floor = pos[i] + items[i].h + GAP;
      }
    } else {
      let floor = start;
      for (let i = 0; i < items.length; i++) {
        pos[i] = Math.max(pos[i], floor);
        floor = pos[i] + items[i].h + GAP;
      }
    }
    items.forEach((it, i) => {
      // A card appears where it belongs; only later moves are animated.
      if (it.el.dataset.placed !== '1') {
        it.el.style.transition = 'none';
        it.el.style.top = pos[i] + 'px';
        void it.el.offsetHeight;
        it.el.style.transition = '';
        it.el.dataset.placed = '1';
      } else it.el.style.top = pos[i] + 'px';
      it.el.style.opacity = '1';
    });
    const bottom = items.length ? pos[items.length - 1] + items[items.length - 1].h : fixed;
    c.style.minHeight = bottom + 40 + 'px';
  };

  useLayoutEffect(layout);
  useEffect(() => {
    window.addEventListener('serve:layout', layout);
    window.addEventListener('resize', layout);
    const ro = new ResizeObserver(() => layout());
    if (col.current) ro.observe(col.current);
    return () => {
      window.removeEventListener('serve:layout', layout);
      window.removeEventListener('resize', layout);
      ro.disconnect();
    };
  }, []);

  // A card's height changes as it expands; re-run layout when any does.
  useEffect(() => {
    const ro = new ResizeObserver(() => layout());
    col.current?.querySelectorAll(':scope > .thread-slot').forEach((el) => ro.observe(el));
    return () => ro.disconnect();
  });

  return (
    <aside class="margin" ref={col} aria-label="Comments">
      {draft && (
        <div class="thread-slot" data-thread="__draft" style={{ opacity: 0 }}>
          <DraftCard draft={draft} path={path} />
        </div>
      )}
      {top.map((t) => (
        <div class="thread-slot unanchored" key={t.id} data-thread={t.id} style={{ opacity: 0 }}>
          <ThreadCard t={t} active={t.id === active} hovered={t.id === hover} compact />
        </div>
      ))}
      {flow.map((t) => (
        <div class={'thread-slot' + (atEnd(t) ? ' resolved-slot' : '')} key={t.id} data-thread={t.id} style={{ opacity: 0 }}>
          <ThreadCard t={t} active={t.id === active} hovered={t.id === hover} compact />
        </div>
      ))}
      {!vis.length && !draft && <EmptyHint resolved={threads.length - vis.length} showResolved={showResolved} />}
    </aside>
  );
}

function EmptyHint({ resolved, showResolved }: { resolved: number; showResolved: boolean }) {
  return (
    <div class="margin-empty">
      <p>No open comments.</p>
      <p class="muted">
        Select text and press <kbd>c</kbd>, or press <kbd>c</kbd> with nothing selected to point at a block.
      </p>
      {resolved > 0 && !showResolved && (
        <button type="button" class="link" onClick={() => setState((s) => ({ prefs: { ...s.prefs, showResolved: true } }))}>
          Show {plural(resolved, 'resolved comment')}
        </button>
      )}
    </div>
  );
}

// Panel lists every thread in document order, open ones first and resolved
// ones after them, for narrow windows and for pages shown in a frame.
export function Panel({ onPick }: { onPick?: (id: string) => void }) {
  const threads = useStore((s) => s.threads);
  const active = useStore((s) => s.active);
  const hover = useStore((s) => s.hover);
  const draft = useStore((s) => s.draft);
  const showResolved = useStore((s) => s.prefs.showResolved);
  const path = useStore((s) => s.page.path!);
  const vis = visibleThreads(getState());
  const order = (t: Thread) => (t.scope === 'page' ? -1 : t.location.line_start ?? 1e9);
  const sorted = [...vis].sort((a, b) => order(a) - order(b));
  const open = sorted.filter((t) => t.status === 'open');
  const resolved = sorted.filter((t) => t.status !== 'open');
  const hiddenResolved = threads.length - vis.length;
  const card = (t: Thread) => (
    <div key={t.id} onClick={() => onPick?.(t.id)}>
      <ThreadCard t={t} active={t.id === active} hovered={t.id === hover} />
    </div>
  );
  return (
    <aside class="panel" aria-label="Comments">
      <div class="panel-head">
        <h2>Comments</h2>
        <button type="button" class="icon ghost" aria-label="Close" onClick={() => setState({ panel: false })}>
          ×
        </button>
      </div>
      <div class="panel-body">
        {draft && <DraftCard draft={draft} path={path} />}
        {open.map(card)}
        {resolved.length > 0 && <div class="panel-divider">Resolved · {resolved.length}</div>}
        {resolved.map(card)}
        {!sorted.length && !draft && <EmptyHint resolved={hiddenResolved} showResolved={showResolved} />}
        {hiddenResolved > 0 && showResolved === false && sorted.length > 0 && (
          <button type="button" class="link" onClick={() => setState((s) => ({ prefs: { ...s.prefs, showResolved: true } }))}>
            Show {plural(hiddenResolved, 'resolved comment')}
          </button>
        )}
      </div>
    </aside>
  );
}

