import { useEffect, useLayoutEffect, useRef, useState } from 'preact/hooks';
import { modKey } from '../util';

const DRAFT_PREFIX = 'serve-draft:';

function loadDraft(key?: string): string {
  if (!key) return '';
  try {
    return localStorage.getItem(DRAFT_PREFIX + key) || '';
  } catch {
    return '';
  }
}

function saveDraft(key: string | undefined, text: string) {
  if (!key) return;
  try {
    if (text.trim()) localStorage.setItem(DRAFT_PREFIX + key, text);
    else localStorage.removeItem(DRAFT_PREFIX + key);
  } catch {}
}

type Props = {
  placeholder: string;
  submitLabel: string;
  onSubmit: (text: string) => Promise<unknown>;
  onCancel?: () => void;
  autoFocus?: boolean;
  draftKey?: string; // unsent text is kept under this key
  initial?: string;
  compact?: boolean; // a reply box that grows when focused
  inputRef?: (el: HTMLTextAreaElement | null) => void;
};

// Composer is a text box for a new comment, a reply, or an edit. Ctrl/Cmd+Enter
// sends; Escape cancels (unsent text is kept as a draft, never thrown away).
export function Composer(p: Props) {
  const [text, setText] = useState(() => p.initial ?? loadDraft(p.draftKey));
  const [busy, setBusy] = useState(false);
  const [focused, setFocused] = useState(!!p.autoFocus);
  const ta = useRef<HTMLTextAreaElement>(null);

  // Focus before the next paint, so typing straight after opening the box
  // lands in it rather than on the page (where "c" is a shortcut).
  useLayoutEffect(() => {
    if (p.autoFocus) {
      ta.current?.focus();
      const n = ta.current?.value.length || 0;
      ta.current?.setSelectionRange(n, n);
    }
    p.inputRef?.(ta.current);
    return () => p.inputRef?.(null);
  }, []);

  useEffect(() => {
    const el = ta.current;
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = Math.min(el.scrollHeight, 320) + 'px';
  }, [text, focused]);

  const submit = async () => {
    const t = text.trim();
    if (!t || busy) return;
    setBusy(true);
    try {
      await p.onSubmit(t);
      saveDraft(p.draftKey, '');
      setText('');
      if (p.compact) {
        setFocused(false);
        ta.current?.blur();
      }
    } catch {
      // the caller reported the error; keep the text
    } finally {
      setBusy(false);
    }
  };

  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      submit();
    } else if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      saveDraft(p.draftKey, text);
      if (p.onCancel) p.onCancel();
      else ta.current?.blur();
    }
  };

  const open = !p.compact || focused || !!text;
  return (
    <div class={'composer' + (open ? ' open' : '')}>
      <textarea
        ref={ta}
        value={text}
        rows={p.compact && !open ? 1 : 2}
        placeholder={p.placeholder}
        onInput={(e) => {
          const v = (e.target as HTMLTextAreaElement).value;
          setText(v);
          saveDraft(p.draftKey, v);
        }}
        onKeyDown={onKey}
        onFocus={() => setFocused(true)}
        onBlur={() => setFocused(false)}
        disabled={busy}
      />
      {open && (
        <div class="composer-actions">
          <span class="hint">{modKey}+Enter</span>
          {p.onCancel && (
            <button
              type="button"
              class="ghost"
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => {
                saveDraft(p.draftKey, text);
                p.onCancel!();
              }}
            >
              Cancel
            </button>
          )}
          <button type="button" class="primary" disabled={!text.trim() || busy} onMouseDown={(e) => e.preventDefault()} onClick={submit}>
            {p.submitLabel}
          </button>
        </div>
      )}
    </div>
  );
}
