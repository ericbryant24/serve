// One keyboard map for the whole app. Commands register here; the handler
// skips keys typed into inputs and keys with modifiers it does not expect, so
// nothing else needs its own keydown listener for shortcuts.

import { typing } from './util';

export type Command = {
  key: string; // e.g. "c", "Escape", "]", "?", "mod+s"
  label: string;
  run: (e: KeyboardEvent) => boolean | void; // return false to let the key through
  inInputs?: boolean; // also fires while typing (only for Escape and mod keys)
  hidden?: boolean;
};

const commands: Command[] = [];

export function register(c: Command): () => void {
  commands.unshift(c);
  return () => {
    const i = commands.indexOf(c);
    if (i >= 0) commands.splice(i, 1);
  };
}

export function list(): Command[] {
  const seen = new Set<string>();
  return commands.filter((c) => !c.hidden && !seen.has(c.key) && seen.add(c.key));
}

function keyName(e: KeyboardEvent): string {
  let k = e.key;
  if ((e.metaKey || e.ctrlKey) && k.length === 1) k = 'mod+' + k.toLowerCase();
  return k;
}

export function dispatch(e: KeyboardEvent): boolean {
  if (e.defaultPrevented || e.isComposing) return false;
  const name = keyName(e);
  if (e.altKey) return false;
  if (!name.startsWith('mod+') && (e.metaKey || e.ctrlKey)) return false;
  const inInput = typing(document.activeElement);
  for (const c of commands) {
    if (c.key !== name) continue;
    if (inInput && !c.inInputs) continue;
    if (c.run(e) === false) continue;
    e.preventDefault();
    return true;
  }
  return false;
}

export function installKeys() {
  window.addEventListener('keydown', (e) => dispatch(e));
}
