// A small shared store: the app's state lives in one object, components read
// slices of it with useStore, and anything (an event stream, a keyboard
// command) can update it with setState.

import { useEffect, useState } from 'preact/hooks';
import type { PageData, Thread, Draft } from './types';

export type Prefs = {
  theme: 'system' | 'light' | 'dark';
  width: 'narrow' | 'medium' | 'wide' | 'full';
  textSize: number; // percent
  showResolved: boolean;
  showHighlights: boolean;
  sidebar: boolean;
};

export type State = {
  page: PageData;
  threads: Thread[];
  rev: string;
  active: string | null; // thread in focus
  hover: string | null;
  draft: Draft | null;
  commenting: boolean; // comment mode
  editing: boolean;
  changes: boolean; // "changes since my last comment" view
  panel: boolean; // threads panel open (narrow windows, HTML pages)
  narrow: boolean;
  gone: boolean; // the file disappeared
  help: boolean;
  report: boolean;
  connected: boolean;
  prefs: Prefs;
};

const PREFS_KEY = 'serve-prefs';

function loadPrefs(): Prefs {
  const d: Prefs = { theme: 'system', width: 'medium', textSize: 100, showResolved: false, showHighlights: true, sidebar: true };
  try {
    const raw = localStorage.getItem(PREFS_KEY);
    if (raw) return { ...d, ...JSON.parse(raw) };
  } catch {}
  return d;
}

let state: State;
const listeners = new Set<() => void>();

export function initState(page: PageData) {
  state = {
    page,
    threads: page.threads || [],
    rev: page.doc?.rev || '',
    active: null,
    hover: null,
    draft: null,
    commenting: false,
    editing: false,
    changes: false,
    panel: false,
    narrow: false,
    gone: false,
    help: false,
    report: false,
    connected: true,
    prefs: loadPrefs(),
  };
  try {
    if (sessionStorage.getItem('serve-commenting') === '1') state.commenting = true;
  } catch {}
}

export function getState(): State {
  return state;
}

export function setState(patch: Partial<State> | ((s: State) => Partial<State>)) {
  const p = typeof patch === 'function' ? patch(state) : patch;
  state = { ...state, ...p };
  if ('prefs' in p) {
    try {
      localStorage.setItem(PREFS_KEY, JSON.stringify(state.prefs));
    } catch {}
  }
  if ('commenting' in p) {
    try {
      sessionStorage.setItem('serve-commenting', state.commenting ? '1' : '0');
    } catch {}
  }
  listeners.forEach((l) => l());
}

export function setPrefs(p: Partial<Prefs>) {
  setState((s) => ({ prefs: { ...s.prefs, ...p } }));
}

export function subscribe(f: () => void): () => void {
  listeners.add(f);
  return () => listeners.delete(f);
}

// useStore re-renders the component when the selected slice changes.
export function useStore<T>(select: (s: State) => T): T {
  const [value, setValue] = useState(() => select(state));
  useEffect(() => {
    const check = () => {
      const next = select(state);
      setValue((prev) => (Object.is(prev, next) ? prev : next));
    };
    check();
    return subscribe(check);
  }, []);
  return value;
}

// Threads the page should show: open ones, plus resolved ones when asked.
export function visibleThreads(s: State): Thread[] {
  return s.threads.filter((t) => t.status === 'open' || s.prefs.showResolved || t.id === s.active);
}
