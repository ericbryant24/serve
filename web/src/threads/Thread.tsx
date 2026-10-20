// One thread: what it is about, its messages, and a reply box that stays
// open after sending. New messages arriving from an agent appear in place
// without disturbing anything being typed.

import { Icon } from '../design/icons';
import { Fragment } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { Threads } from '../api';
import { getState, setState } from '../state';
import { errorToast, withUndo } from '../ui';
import { scrollToThread } from '../doc/DocView';
import { frameCommand } from '../doc/FrameView';
import { timeAgo, fullDate, clip, plural, kindName, messageText } from '../util';
import { Composer } from './Composer';
import type { Thread as T, Message } from '../types';

const replyBoxes = new Map<string, HTMLTextAreaElement>();

export function focusReply(id: string) {
  const el = replyBoxes.get(id);
  if (el) {
    el.focus();
    return true;
  }
  return false;
}

function replaceThread(t: T) {
  setState((s) => ({ threads: s.threads.some((x) => x.id === t.id) ? s.threads.map((x) => (x.id === t.id ? t : x)) : [...s.threads, t] }));
}

// revealThread scrolls to a thread once the cards have moved: the panel to
// its card, the page to its highlight (or, for a thread with none, its card
// in the margin), and a framed page to its highlight too.
function revealThread(id: string) {
  requestAnimationFrame(() =>
    requestAnimationFrame(() => {
      const card = document.querySelector(`.panel [data-thread="${CSS.escape(id)}"]`) as HTMLElement | null;
      if (card) {
        const body = card.closest('.col-body') as HTMLElement;
        const r = card.getBoundingClientRect(), b = body.getBoundingClientRect();
        if (r.top < b.top || r.bottom > b.bottom) body.scrollTo({ top: body.scrollTop + r.top - b.top - 16, behavior: 'smooth' });
        if (getState().page.doc?.kind === 'html') frameCommand('focus', id);
        return;
      }
      const slot = document.querySelector(`.margin > .thread-slot[data-thread="${CSS.escape(id)}"]`) as HTMLElement | null;
      if (!slot) return;
      if (!slot.classList.contains('unanchored')) return scrollToThread(id);
      // Cards slide to their place, so aim for where this one is going.
      const main = slot.closest('.main') as HTMLElement;
      const y = slot.parentElement!.getBoundingClientRect().top - main.getBoundingClientRect().top + main.scrollTop + parseFloat(slot.style.top || '0');
      main.scrollTo({ top: Math.max(0, y - 16), behavior: 'smooth' });
    }),
  );
}

export async function resolveThread(t: T) {
  const status = t.status === 'open' ? 'resolved' : 'open';
  const before = getState().threads;
  // A reopened thread takes focus, and the view follows it to its place
  // among the open ones.
  setState((s) => ({ threads: s.threads.map((x) => (x.id === t.id ? { ...x, status, awaiting: '' } : x)), active: status === 'resolved' ? null : t.id }));
  if (status === 'open') revealThread(t.id);
  if (status === 'resolved') {
    withUndo(
      'Resolved',
      () => Threads.setStatus(t.id, 'resolved').then(replaceThread),
      () => setState({ threads: before }),
      3500,
    );
  } else {
    try {
      replaceThread(await Threads.setStatus(t.id, 'open'));
    } catch (e) {
      setState({ threads: before });
      errorToast(e);
    }
  }
}

function deleteThread(t: T) {
  const before = getState().threads;
  setState((s) => ({ threads: s.threads.filter((x) => x.id !== t.id), active: null }));
  withUndo('Comment deleted', () => Threads.remove(t.id), () => setState({ threads: before }));
}

function deleteMessage(t: T, m: Message) {
  if (t.messages[0]?.id === m.id) return deleteThread(t);
  const before = getState().threads;
  setState((s) => ({ threads: s.threads.map((x) => (x.id === t.id ? { ...x, messages: x.messages.filter((y) => y.id !== m.id) } : x)) }));
  withUndo('Reply deleted', () => Threads.removeMessage(m.id), () => setState({ threads: before }));
}

function Quote({ t }: { t: T }) {
  const a = t.anchor;
  const st = t.location.state;
  if (t.scope === 'page') return <div class="quote quote-page">Whole page</div>;
  if (t.scope === 'element') {
    const label = a.display || a.element?.label || '';
    return (
      <div class="quote quote-element">
        <span class="quote-kind">{kindName(null, a.element?.tag || '')}</span> {clip(label, 80)}
        {st === 'changed' && <span class="tag">edited</span>}
      </div>
    );
  }
  const orig = a.display || a.quote || '';
  if (st === 'changed') {
    return (
      <div class="quote quote-changed" title="The text was edited after this comment">
        <del>{clip(orig, 90)}</del>
        <span class="arrow"> → </span>
        <span>{clip(t.location.rendered_text || t.location.current_text || '', 90)}</span>
      </div>
    );
  }
  if (st === 'deleted') {
    return (
      <div class="quote quote-deleted" title="The commented text was deleted">
        <del>{clip(orig, 120)}</del> <span class="tag">deleted</span>
      </div>
    );
  }
  if (st === 'unplaced') {
    return (
      <div class="quote quote-deleted">
        <del>{clip(orig, 120)}</del> <span class="tag">couldn't place</span>
        {t.location.section && <div class="quote-where">was in {t.location.section}</div>}
      </div>
    );
  }
  return <div class="quote">{clip(orig, 140)}</div>;
}

function initials(name: string): string {
  const words = name.split(/[\s._-]+/).filter(Boolean);
  return ((words[0]?.[0] || '?') + (words.length > 1 ? words[words.length - 1][0] : '')).toUpperCase();
}

function Author({ m, children }: { m: Message; children?: any }) {
  const kind = m.author.kind || 'unknown';
  const me = kind === 'human' && m.author.name === getState().page.user;
  const name = me ? 'You' : m.author.name || 'Unknown';
  return (
    <span class={'author author-' + kind}>
      <span class={'avatar sm' + (kind === 'agent' ? ' agent' : '')} aria-hidden="true">
        {kind === 'agent' ? <Icon name="agent" size={12} /> : initials(m.author.name || '?')}
      </span>
      <span class="author-name" title={kind === 'agent' ? 'Agent' : m.author.name}>
        {name}
        {children}
      </span>
    </span>
  );
}

function MessageView({ t, m, onEdit, editing }: { t: T; m: Message; onEdit: (id: string | null) => void; editing: boolean }) {
  const [menu, setMenu] = useState(false);
  const [, tick] = useState(0);
  useEffect(() => {
    const i = window.setInterval(() => tick((n) => n + 1), 30000);
    return () => clearInterval(i);
  }, []);
  return (
    <div class="message">
      <div class="message-head">
        <Author m={m}>
          <time dateTime={m.created_at} title={fullDate(m.created_at)}>
            {timeAgo(m.created_at)}
            {m.edited_at ? ' · edited' : ''}
          </time>
        </Author>
        <div class="menu-wrap">
          <button type="button" class="icon-only small" aria-label="Message actions" onClick={(e) => (e.stopPropagation(), setMenu(!menu))}>
            <Icon name="more" size={14} />
          </button>
          {menu && (
            <div class="menu" onMouseLeave={() => setMenu(false)}>
              <button type="button" onClick={() => (setMenu(false), onEdit(m.id))}>
                Edit
              </button>
              <button type="button" class="danger" onClick={() => (setMenu(false), deleteMessage(t, m))}>
                {t.messages[0]?.id === m.id ? 'Delete comment' : 'Delete reply'}
              </button>
            </div>
          )}
        </div>
      </div>
      {editing ? (
        <Composer
          placeholder="Edit"
          submitLabel="Save"
          initial={m.text}
          autoFocus
          onCancel={() => onEdit(null)}
          onSubmit={async (text) => {
            try {
              replaceThread(await Threads.editMessage(m.id, text));
              onEdit(null);
            } catch (e) {
              errorToast(e);
              throw e;
            }
          }}
        />
      ) : (
        <div
          class="message-body"
          dangerouslySetInnerHTML={{ __html: m.html }}
          onClick={(e) => {
            // A pasted image opens full size in a new tab.
            const img = (e.target as Element).closest('img');
            if (img) window.open(img.src, '_blank', 'noopener');
          }}
        />
      )}
    </div>
  );
}

type Props = { t: T; active: boolean; hovered: boolean; compact?: boolean };

export function ThreadCard({ t, active, hovered, compact }: Props) {
  const [editing, setEditing] = useState<string | null>(null);
  const ref = useRef<HTMLDivElement>(null);
  const resolved = t.status === 'resolved';
  // A resolved thread shows one line until it is picked, in the panel too.
  const expanded = active || (!compact && !resolved);
  const msgs = t.messages;
  let shown: Message[] = msgs;
  let hidden = 0;
  if (!expanded && msgs.length > 2) {
    shown = [msgs[0], msgs[msgs.length - 1]];
    hidden = msgs.length - 2;
  }
  return (
    <div
      ref={ref}
      class={'thread' + (active ? ' active' : '') + (hovered ? ' hovered' : '') + (resolved ? ' resolved' : '') + (t.awaiting === 'human' ? ' awaiting-you' : '')}
      data-thread={t.id}
      id={'thread-' + t.id}
      onMouseDown={(e) => {
        if ((e.target as Element).closest('button, textarea, a, input')) return;
        if (!active) setState({ active: t.id, draft: null });
      }}
      onMouseEnter={() => setState({ hover: t.id })}
      onMouseLeave={() => getState().hover === t.id && setState({ hover: null })}
    >
      <div class="thread-head">
        <Quote t={t} />
        <div class="thread-actions">
          {t.awaiting === 'human' && !resolved && <span class="status attn">Your turn</span>}
          <button
            type="button"
            class={'icon-only resolve' + (resolved ? ' on' : '')}
            title={resolved ? 'Reopen (e)' : 'Resolve (e)'}
            aria-label={resolved ? 'Reopen' : 'Resolve'}
            onClick={(e) => (e.stopPropagation(), resolveThread(t))}
          >
            <Icon name="check" size={14} />
          </button>
        </div>
      </div>
      {!expanded && resolved ? (
        <div class="thread-summary">
          {clip(messageText(msgs[0]?.text || ''), 80)} · {plural(msgs.length, 'message')}
        </div>
      ) : (
        <>
          {shown.map((m, i) => (
            <Fragment key={m.id}>
              {hidden > 0 && i === 1 && (
                <button type="button" class="more" onClick={() => setState({ active: t.id })}>
                  {plural(hidden, 'more reply', 'more replies')}
                </button>
              )}
              <MessageView t={t} m={m} editing={editing === m.id} onEdit={setEditing} />
            </Fragment>
          ))}
          {!resolved && (expanded || active) && (
            <Composer
              compact
              placeholder="Reply…"
              submitLabel="Reply"
              draftKey={'reply:' + t.id}
              inputRef={(el) => (el ? replyBoxes.set(t.id, el) : replyBoxes.delete(t.id))}
              onSubmit={async (text) => {
                try {
                  replaceThread(await Threads.reply(t.id, text));
                } catch (e) {
                  errorToast(e);
                  throw e;
                }
              }}
            />
          )}
          {resolved && active && (
            <div class="resolved-note">
              Resolved{t.resolved_by?.name ? ' by ' + t.resolved_by.name : ''}
              {t.resolved_at ? ' · ' + timeAgo(t.resolved_at) : ''}
            </div>
          )}
        </>
      )}
    </div>
  );
}
