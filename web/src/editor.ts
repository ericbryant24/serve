// CodeMirror, loaded only when someone presses Edit.

import { EditorState } from '@codemirror/state';
import { EditorView, keymap, lineNumbers, highlightActiveLine, drawSelection, highlightActiveLineGutter } from '@codemirror/view';
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
import { markdown } from '@codemirror/lang-markdown';
import { syntaxHighlighting, defaultHighlightStyle, bracketMatching } from '@codemirror/language';
import { searchKeymap, highlightSelectionMatches } from '@codemirror/search';

export type EditorHandle = {
  getText(): string;
  setText(text: string): void;
  focus(): void;
  destroy(): void;
  scrollTo(fraction: number): void;
};

export function mountEditor(
  parent: HTMLElement,
  opts: { text: string; markdown: boolean; onChange: (text: string) => void; onSave: () => void; onScroll?: (fraction: number) => void },
): EditorHandle {
  const theme = EditorView.theme({
    '&': { height: '100%', fontSize: '13.5px', backgroundColor: 'var(--bg)', color: 'var(--fg)' },
    '.cm-scroller': { fontFamily: 'var(--mono)', lineHeight: '1.6' },
    '.cm-gutters': { backgroundColor: 'var(--bg-subtle)', color: 'var(--muted)', borderRight: '1px solid var(--border)' },
    '.cm-activeLine': { backgroundColor: 'var(--bg-hover)' },
    '.cm-activeLineGutter': { backgroundColor: 'var(--bg-hover)' },
    '&.cm-focused': { outline: 'none' },
    '.cm-cursor': { borderLeftColor: 'var(--fg)' },
    '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection': { backgroundColor: 'var(--selection)' },
  });
  const view = new EditorView({
    parent,
    state: EditorState.create({
      doc: opts.text,
      extensions: [
        lineNumbers(),
        highlightActiveLineGutter(),
        highlightActiveLine(),
        drawSelection(),
        history(),
        bracketMatching(),
        highlightSelectionMatches(),
        EditorView.lineWrapping,
        opts.markdown ? markdown() : [],
        syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
        keymap.of([
          { key: 'Mod-s', preventDefault: true, run: () => (opts.onSave(), true) },
          indentWithTab,
          ...defaultKeymap,
          ...historyKeymap,
          ...searchKeymap,
        ]),
        EditorView.updateListener.of((u) => {
          if (u.docChanged) opts.onChange(u.state.doc.toString());
        }),
        EditorView.domEventHandlers({
          scroll: (e, v) => {
            const s = v.scrollDOM;
            const range = s.scrollHeight - s.clientHeight;
            opts.onScroll?.(range > 0 ? s.scrollTop / range : 0);
            return false;
          },
        }),
        theme,
      ],
    }),
  });
  return {
    getText: () => view.state.doc.toString(),
    setText: (text: string) => view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: text } }),
    focus: () => view.focus(),
    destroy: () => view.destroy(),
    scrollTo: (f: number) => {
      const s = view.scrollDOM;
      s.scrollTop = f * (s.scrollHeight - s.clientHeight);
    },
  };
}
