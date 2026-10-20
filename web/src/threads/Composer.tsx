import { useEffect, useLayoutEffect, useRef, useState } from 'preact/hooks';
import { Icon } from '../design/icons';
import { Images } from '../api';
import { errorToast } from '../ui';

const DRAFT_PREFIX = 'serve-draft:';

// A pasted image is a markdown image of the url the server keeps it at.
const IMAGE_RE = /!\[[^\]]*\]\((\/_serve\/images\/[0-9a-f]{32}\.(?:png|jpg|gif|webp))\)/g;
let uploads = 0;

function imageURLs(text: string): string[] {
  return [...new Set(Array.from(text.matchAll(IMAGE_RE), (m) => m[1]))];
}

function withoutImage(text: string, url: string): string {
  return text.replace(IMAGE_RE, (m, u) => (u === url ? '' : m)).replace(/\n{3,}/g, '\n\n');
}

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

// Composer is a text box for a new comment, a reply, or an edit. Enter sends
// (Cmd/Ctrl+Enter too) and Shift+Enter starts a new line; Escape cancels
// (unsent text is kept as a draft, never thrown away).
// A pasted image is uploaded and goes in as a markdown image, shown below the
// box until the message is sent.
export function Composer(p: Props) {
  const [text, setText] = useState(() => p.initial ?? loadDraft(p.draftKey));
  const [busy, setBusy] = useState(false);
  const [uploading, setUploading] = useState(0);
  const [focused, setFocused] = useState(!!p.autoFocus);
  const ta = useRef<HTMLTextAreaElement>(null);
  // The latest text, for uploads that finish after more typing.
  const latest = useRef(text);
  const caret = useRef<number | null>(null);

  const change = (v: string) => {
    latest.current = v;
    setText(v);
    saveDraft(p.draftKey, v);
  };

  useLayoutEffect(() => {
    if (caret.current === null || !ta.current) return;
    ta.current.setSelectionRange(caret.current, caret.current);
    caret.current = null;
  }, [text]);

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

  // attach puts a placeholder at the caret, on a line of its own, and swaps in
  // the image once the server has it.
  const attach = async (file: File) => {
    const el = ta.current!;
    const v = latest.current;
    // Several images pasted at once go one after another.
    const a = caret.current ?? el.selectionStart ?? v.length, b = caret.current ?? el.selectionEnd ?? v.length;
    const holder = `![Uploading image ${++uploads}…]()`;
    const ins = (a > 0 && v[a - 1] !== '\n' ? '\n' : '') + holder + (v[b] === '\n' ? '' : '\n');
    caret.current = a + ins.length;
    change(v.slice(0, a) + ins + v.slice(b));
    setUploading((n) => n + 1);
    try {
      const { url } = await Images.upload(file);
      change(latest.current.replace(holder, `![image](${url})`));
    } catch (e) {
      change(latest.current.replace(holder + '\n', '').replace(holder, ''));
      errorToast(e);
    } finally {
      setUploading((n) => n - 1);
    }
  };

  // Text wins over an image when the clipboard has both (a spreadsheet's
  // copied cells come with a picture of themselves), unless the text is only
  // the names of the files, as when they were copied in the Finder.
  const onPaste = (e: ClipboardEvent) => {
    const dt = e.clipboardData;
    if (!dt) return;
    const files = Array.from(dt.files).filter((f) => f.type.startsWith('image/'));
    if (!files.length) return;
    const plain = dt.getData('text/plain').trim();
    const names = new Set(files.map((f) => f.name));
    if (plain && !plain.split(/\r?\n/).every((l) => names.has(l.trim()))) return;
    e.preventDefault();
    files.forEach(attach);
  };

  const submit = async () => {
    const t = text.trim();
    if (!t || busy || uploading) return;
    setBusy(true);
    try {
      await p.onSubmit(t);
      change('');
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
    // Not while an input method is composing: there Enter picks the word.
    if (e.key === 'Enter' && !e.shiftKey && !e.altKey && !e.isComposing) {
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
  const images = imageURLs(text);
  return (
    <div class={'composer' + (open ? ' open' : '')}>
      <textarea
        ref={ta}
        value={text}
        rows={p.compact && !open ? 1 : 2}
        placeholder={p.placeholder}
        onInput={(e) => change((e.target as HTMLTextAreaElement).value)}
        onPaste={onPaste}
        onKeyDown={onKey}
        onFocus={() => setFocused(true)}
        onBlur={() => setFocused(false)}
        disabled={busy}
      />
      {(images.length > 0 || uploading > 0) && (
        <div class="composer-images">
          {images.map((url) => (
            <span class="composer-image" key={url}>
              <img src={url} alt="" />
              <button type="button" class="icon-only small" aria-label="Remove image" title="Remove image" onMouseDown={(e) => e.preventDefault()} onClick={() => change(withoutImage(latest.current, url))}>
                <Icon name="x" size={12} />
              </button>
            </span>
          ))}
          {uploading > 0 && (
            <span class="composer-image" aria-label="Uploading image">
              <span class="spinner" />
            </span>
          )}
        </div>
      )}
      {open && (
        <div class="composer-actions">
          <span class="hint">Shift+Enter for a new line</span>
          {p.onCancel && (
            <button
              type="button"
              class="plain"
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => {
                saveDraft(p.draftKey, text);
                p.onCancel!();
              }}
            >
              Cancel
            </button>
          )}
          <button type="button" class="primary" disabled={!text.trim() || busy || uploading > 0} onMouseDown={(e) => e.preventDefault()} onClick={submit}>
            {p.submitLabel}
          </button>
        </div>
      )}
    </div>
  );
}
