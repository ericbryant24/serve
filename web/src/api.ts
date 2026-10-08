// The server's JSON API. Every request carries the page's token in a header,
// which is what the server checks; a page on another site can neither read
// the token nor send the header without a preflight the server refuses.

import type { PageData, Thread, DocData, Entry, HomeData, SelectionPayload, ElementPayload } from './types';

export const token = (document.querySelector('meta[name="serve-token"]') as HTMLMetaElement | null)?.content || '';

export class ApiError extends Error {
  status: number;
  body: any;
  constructor(status: number, message: string, body: any) {
    super(message);
    this.status = status;
    this.body = body;
  }
}

export async function api<T = any>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { 'X-Serve-Token': token };
  const name = guestName();
  if (name) headers['X-Serve-Name'] = name;
  let payload: BodyInit | undefined;
  if (body instanceof FormData) payload = body;
  else if (body !== undefined) {
    headers['Content-Type'] = 'application/json';
    payload = JSON.stringify(body);
  }
  let res: Response;
  try {
    res = await fetch('/_serve/api/' + path, { method, headers, body: payload });
  } catch {
    throw new ApiError(0, 'serve is not responding', null);
  }
  const text = await res.text();
  let data: any = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = null;
  }
  if (!res.ok) throw new ApiError(res.status, (data && data.error) || text || res.statusText, data);
  return data as T;
}

export function eventsURL(params: Record<string, string>): string {
  const q = new URLSearchParams({ ...params, token });
  return '/_serve/events?' + q.toString();
}

function guestName(): string {
  try {
    return localStorage.getItem('serve-guest-name') || '';
  } catch {
    return '';
  }
}

export function readPageData(): PageData {
  const el = document.getElementById('serve-data');
  return JSON.parse(el?.textContent || '{}');
}

const q = (p: string) => encodeURIComponent(p);

export const Threads = {
  list: (path: string) => api<{ threads: Thread[]; cursor: number; rev: string }>('GET', 'threads?path=' + q(path)),
  createText: (path: string, selection: SelectionPayload, text: string) =>
    api<Thread>('POST', 'threads', { path, scope: 'text', selection, text }),
  createElement: (path: string, element: ElementPayload, text: string) =>
    api<Thread>('POST', 'threads', { path, scope: 'element', element, text }),
  createPage: (path: string, text: string) => api<Thread>('POST', 'threads', { path, scope: 'page', text }),
  reply: (id: string, text: string) => api<Thread>('POST', `threads/${id}/messages`, { text }),
  setStatus: (id: string, status: 'open' | 'resolved') => api<Thread>('PATCH', `threads/${id}`, { status }),
  remove: (id: string) => api('DELETE', `threads/${id}`),
  editMessage: (id: string, text: string) => api<Thread>('PATCH', `messages/${id}`, { text }),
  removeMessage: (id: string) => api<{ ok: boolean; thread_deleted: boolean }>('DELETE', `messages/${id}`),
};

export const Docs = {
  get: (path: string) => api<{ doc: DocData; threads: Thread[]; cursor: number }>('GET', 'doc?path=' + q(path)),
  tree: (path: string, client: string) => api<{ entries: Entry[] }>('GET', `tree?path=${q(path)}&client=${q(client)}`),
  search: (root: string, query: string) => api<{ results: Entry[] }>('GET', `search?root=${q(root)}&q=${q(query)}`),
  changes: (path: string) => api<{ available: boolean; since?: string; unchanged?: boolean; blocks?: { kind: string; html: string }[] }>('GET', 'changes?path=' + q(path)),
  preview: (path: string, content: string) => api<{ html: string; kind: string; has_mermaid: boolean }>('POST', 'preview', { path, content }),
  file: (path: string) => api<{ content: string; rev: string }>('GET', 'file?path=' + q(path)),
  save: (path: string, content: string, base_rev: string, force = false) => api<{ rev: string }>('PUT', 'file', { path, content, base_rev, force }),
  merge: (base: string, mine: string, theirs: string) => api<{ merged: string; clean: boolean; failed: number }>('POST', 'merge', { base, mine, theirs }),
  openFolder: (path: string) => api<{ path: string; url: string }>('POST', 'folders', { path }),
  forgetFolder: (path: string) => api('DELETE', 'folders', { path }),
  relink: (from: string, path: string) => api<{ threads: Thread[] }>('POST', 'relink', { from, path }),
  reveal: (path: string) => api('POST', 'reveal', { path }),
  home: () => api<HomeData>('GET', 'home'),
};
