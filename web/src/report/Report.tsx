// "Report a problem": capture what is on screen (with its words blanked out),
// describe it, review exactly what the report contains, and export it as a
// zip to send. Nothing leaves this machine on its own.

import { useEffect, useState } from 'preact/hooks';
import { api, token } from '../api';
import { useStore, setState } from '../state';
import { errorToast, toast } from '../ui';
import { captureStructural } from './capture';

type Review = {
  report: { id: string; kind: string; title: string };
  title: string;
  markdown: string;
  attachments: { id: string; kind: string; mode?: string; bytes: number; included: boolean; text?: string; secrets?: { kind: string; excerpt: string }[] }[];
  body_secrets?: { kind: string; excerpt: string }[];
  secret_count: number;
  dir: string;
};

function size(n: number) {
  return n < 1024 ? n + ' B' : n < 1048576 ? (n / 1024).toFixed(1) + ' KB' : (n / 1048576).toFixed(1) + ' MB';
}

export function ReportDialog() {
  const open = useStore((s) => s.report);
  const kind = useStore((s) => s.page.doc?.kind || s.page.view);
  const [shot, setShot] = useState<Blob | null>(null);
  const [shotErr, setShotErr] = useState('');
  const [form, setForm] = useState({ kind: 'bug', title: '', body: '', log: true });
  const [review, setReview] = useState<Review | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!open) return;
    setReview(null);
    setShot(null);
    setShotErr('');
    // Capture before the dialog paints over the page.
    captureStructural().then(setShot, (e) => setShotErr(e.message || String(e)));
  }, [open]);

  if (!open) return null;
  const close = () => setState({ report: false });

  const create = async () => {
    if (!form.title.trim()) return;
    setBusy(true);
    try {
      let rv = await api<Review>('POST', 'reports', { kind: form.kind, title: form.title, body: form.body, with_log: form.log, browser: navigator.userAgent, view_kind: kind });
      if (shot) {
        const fd = new FormData();
        fd.append('file', shot, 'screenshot.png');
        fd.append('kind', 'screenshot');
        fd.append('mode', 'structural');
        rv = await api<Review>('POST', `reports/${rv.report.id}/attachments`, fd);
      }
      setReview(rv);
    } catch (e) {
      errorToast(e);
    } finally {
      setBusy(false);
    }
  };

  const toggle = async (aid: string, included: boolean) => {
    try {
      setReview(await api<Review>('PATCH', `reports/${review!.report.id}`, { include: aid, included }));
    } catch (e) {
      errorToast(e);
    }
  };

  return (
    <div class="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && close()}>
      <div class="modal report" role="dialog" aria-label="Report a problem">
        {!review ? (
          <>
            <h2>Report a problem</h2>
            <div class="seg">
              <button type="button" class={form.kind === 'bug' ? 'on' : ''} onClick={() => setForm({ ...form, kind: 'bug' })}>
                Something is wrong
              </button>
              <button type="button" class={form.kind === 'feature' ? 'on' : ''} onClick={() => setForm({ ...form, kind: 'feature', log: false })}>
                I'd like a feature
              </button>
            </div>
            <label class="field">
              Title
              <input type="text" value={form.title} onInput={(e) => setForm({ ...form, title: (e.target as HTMLInputElement).value })} autoFocus />
            </label>
            <label class="field">
              Details
              <textarea
                rows={5}
                value={form.body}
                placeholder={form.kind === 'bug' ? 'What you did, what you expected, what happened instead.' : 'What you want to do, and why it is awkward today.'}
                onInput={(e) => setForm({ ...form, body: (e.target as HTMLTextAreaElement).value })}
              />
            </label>
            <label class="check">
              <input type="checkbox" checked={form.log} onChange={() => setForm({ ...form, log: !form.log })} /> Attach serve's recent log (file paths are shortened before they are logged)
            </label>
            <p class="muted small">{shot ? 'A screenshot with all text blanked out was captured.' : shotErr ? 'No screenshot: ' + shotErr : 'Capturing the page…'}</p>
            <div class="modal-actions">
              <button type="button" class="ghost" onClick={close}>
                Cancel
              </button>
              <button type="button" class="primary" disabled={!form.title.trim() || busy} onClick={create}>
                Review
              </button>
            </div>
          </>
        ) : (
          <>
            <h2>Review the report</h2>
            <p class="muted small">This is exactly what an export contains. Attachments are left out unless you turn them on.</p>
            <pre class="report-text">{'# ' + review.title + '\n\n' + review.markdown}</pre>
            {review.attachments.map((a) => (
              <div class={'attachment' + (a.included ? ' on' : '')} key={a.id}>
                <label class="check">
                  <input type="checkbox" checked={a.included} onChange={() => toggle(a.id, !a.included)} />{' '}
                  {a.kind === 'screenshot' ? 'Screenshot (text blanked)' : a.kind === 'log' ? 'Recent log' : 'Source excerpt'} · {size(a.bytes)}
                  {!!a.secrets?.length && <span class="warn"> · {a.secrets.length} possible credentials</span>}
                </label>
                {a.kind === 'screenshot' ? (
                  <img src={`/_serve/api/reports/${review.report.id}/attachments/${a.id}?token=${token}`} alt="" loading="lazy" onError={(e) => ((e.target as HTMLImageElement).style.display = 'none')} />
                ) : (
                  a.text && <pre>{a.text.length > 3000 ? a.text.slice(0, 3000) + '\n…' : a.text}</pre>
                )}
              </div>
            ))}
            {review.secret_count > 0 && <p class="warn">{review.secret_count} item(s) look like credentials. Check them before you send the export.</p>}
            <div class="modal-actions">
              <span class="muted small">Saved in {review.dir}</span>
              <button type="button" class="ghost" onClick={() => api('POST', `reports/${review.report.id}/reveal`).catch(errorToast)}>
                Show folder
              </button>
              <a
                class="button primary"
                href={`/_serve/api/reports/${review.report.id}/export`}
                onClick={(e) => {
                  e.preventDefault();
                  download(review.report.id).then(() => toast('Exported'), errorToast);
                }}
              >
                Export zip
              </a>
              <button type="button" class="ghost" onClick={close}>
                Done
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

// The export needs the token header, so it is fetched and saved as a blob
// rather than linked.
async function download(id: string) {
  const res = await fetch(`/_serve/api/reports/${id}/export`, { headers: { 'X-Serve-Token': token } });
  if (!res.ok) throw new Error('export failed: ' + res.status);
  const blob = await res.blob();
  const a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = `serve-report-${id}.zip`;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(a.href), 2000);
}
