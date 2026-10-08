// The pages that are not a document: the start page, a folder, a missing
// file, a folder that is not open in serve, and plain messages.

import { useEffect, useState } from 'preact/hooks';
import { useStore } from '../state';
import { Docs } from '../api';
import { errorToast } from '../ui';
import { timeAgo, clip, plural } from '../util';
import { onLive } from '../live';
import type { Entry, HomeData } from '../types';

function Listing({ entries }: { entries: Entry[] }) {
  if (!entries.length) return <p class="muted">This folder is empty.</p>;
  return (
    <ul class="listing">
      {entries.map((e) => (
        <li key={e.path}>
          <a href={e.url} class={e.dir ? 'dir' : 'file'}>
            <span class={e.dir ? 'ficon ficon-dir' : 'ficon ficon-file'} aria-hidden="true" />
            {e.name}
            {e.dir ? '/' : ''}
          </a>
          {!!e.threads && <span class="count">{plural(e.threads, 'comment')}</span>}
        </li>
      ))}
    </ul>
  );
}

export function DirView() {
  const page = useStore((s) => s.page);
  return (
    <div class="page-view">
      <h1>{page.name}</h1>
      <Listing entries={page.entries || []} />
      {page.readme?.html && (
        <section class="readme">
          <div class="readme-title">{page.readme.title}</div>
          <article class="markdown-body" dangerouslySetInnerHTML={{ __html: page.readme.html }} />
        </section>
      )}
    </div>
  );
}

export function NotFoundView() {
  const page = useStore((s) => s.page);
  const sug = page.suggestions || [];
  return (
    <div class="page-view">
      <h1>Not found</h1>
      <p>
        <code>{page.display}</code> is not there any more. It was moved, renamed or deleted.
      </p>
      {sug.length > 0 && (
        <>
          <h2>Did it move?</h2>
          <ul class="suggestions">
            {sug.map((s) => (
              <li key={s.path}>
                <a href={s.url}>{s.display}</a> <span class="muted">{s.reason}</span>
              </li>
            ))}
          </ul>
          <p class="muted">Comments follow a file when it moves, so opening it at its new place shows the same comments.</p>
        </>
      )}
      <h2>
        In <a href={page.nearest_url}>{page.nearest}</a>
      </h2>
      <Listing entries={page.entries || []} />
    </div>
  );
}

export function ClosedView() {
  const page = useStore((s) => s.page);
  const open = async () => {
    try {
      await Docs.openFolder(page.nearest_url!);
      location.reload();
    } catch (e) {
      errorToast(e);
    }
  };
  return (
    <div class="page-view">
      <h1>Not open in serve</h1>
      <p>
        serve only shows folders you have opened. <code>{page.display}</code> is outside all of them.
      </p>
      <p>
        <button type="button" class="primary" onClick={open}>
          Open {page.nearest}
        </button>
      </p>
    </div>
  );
}

export function MessageView() {
  const msg = useStore((s) => s.page.message);
  return (
    <div class="page-view">
      <p>{msg}</p>
    </div>
  );
}

export function HomeView() {
  const initial = useStore((s) => s.page.home!);
  const [home, setHome] = useState<HomeData>(initial);
  useEffect(() => {
    const refresh = () => Docs.home().then(setHome).catch(() => {});
    onLive({ onCounts: refresh, onResync: refresh });
  }, []);
  const forget = async (path: string) => {
    try {
      await Docs.forgetFolder(path);
      setHome({ ...home, folders: home.folders.filter((f) => f.path !== path) });
    } catch (e) {
      errorToast(e);
    }
  };
  return (
    <div class="page-view home">
      <section>
        <h2>Waiting for you {home.inbox.length > 0 && <span class="count">{home.inbox.length}</span>}</h2>
        {home.inbox.length === 0 ? (
          <p class="muted">No replies waiting. When an agent answers one of your comments, it shows up here.</p>
        ) : (
          <ul class="inbox">
            {home.inbox.map((i) => (
              <li key={i.thread_id}>
                <a href={i.url + '#thread-' + i.thread_id}>
                  <div class="inbox-doc">{i.display}</div>
                  {i.quote && <div class="quote">{clip(i.quote, 100)}</div>}
                  <div class="inbox-last">
                    <strong>{i.last.author.name}</strong> {clip(i.last.text, 160)} <span class="muted">· {timeAgo(i.last.created_at)}</span>
                  </div>
                </a>
              </li>
            ))}
          </ul>
        )}
      </section>
      <section>
        <h2>Recent documents</h2>
        {home.recent.length === 0 ? (
          <p class="muted">Documents you comment on appear here.</p>
        ) : (
          <ul class="recent">
            {home.recent.map((d) => (
              <li key={d.path}>
                <a href={d.url}>{d.name}</a>
                <span class="muted path">{d.display}</span>
                {d.open > 0 && <span class="count">{plural(d.open, 'open comment')}</span>}
                {d.awaiting > 0 && <span class="badge">{d.awaiting} for you</span>}
              </li>
            ))}
          </ul>
        )}
      </section>
      <section>
        <h2>Folders</h2>
        {home.folders.length === 0 ? (
          <p class="muted">
            Run <code>serve &lt;folder&gt;</code> to open one.
          </p>
        ) : (
          <ul class="folders">
            {home.folders.map((f) => (
              <li key={f.path} class={f.exists ? '' : 'missing'}>
                <a href={f.url}>{f.name}</a>
                <span class="muted path">{f.display}</span>
                <span class="muted">{timeAgo(f.used)}</span>
                <button type="button" class="link" onClick={() => forget(f.path)} title="Stop serving this folder">
                  Forget
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
