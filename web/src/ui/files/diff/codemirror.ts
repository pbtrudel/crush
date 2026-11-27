import { MergeView, unifiedMergeView } from '@codemirror/merge';
import { syntaxHighlighting } from '@codemirror/language';
import { EditorState, type Extension } from '@codemirror/state';
import { EditorView, lineNumbers } from '@codemirror/view';
import { classHighlighter } from '@lezer/highlight';
import type { FileDiff } from '../../../api/types';
import type { DiffMode } from '../../../state/drawer';
import diffCss from '../../../styles/diff.css?inline';
import { languageExtension } from './languages';

let sheet: CSSStyleSheet | null = null;

// style-mod writes a <style> element into a document root, which the CSP
// blocks, and adopts a constructed sheet into a shadow root, which it
// allows. The editor therefore lives in a shadow root, with our own diff
// styles adopted next to CodeMirror's.
function shadowFor(host: HTMLElement): ShadowRoot {
  const root = host.shadowRoot ?? host.attachShadow({ mode: 'open' });
  if (!sheet) {
    sheet = new CSSStyleSheet();
    sheet.replaceSync(diffCss);
  }
  if (!root.adoptedStyleSheets.includes(sheet)) root.adoptedStyleSheets = [...root.adoptedStyleSheets, sheet];
  return root;
}

const COLLAPSE = { margin: 3, minSize: 6 };

export async function mountDiff(host: HTMLElement, d: FileDiff, mode: DiffMode, label: string): Promise<() => void> {
  const root = shadowFor(host);
  root.replaceChildren();
  const parent = document.createElement('div');
  parent.className = 'diff';
  root.append(parent);
  const base: Extension[] = [
    EditorState.readOnly.of(true),
    EditorView.editable.of(false),
    lineNumbers(),
    EditorView.lineWrapping,
    syntaxHighlighting(classHighlighter),
    await languageExtension(d.language),
  ];
  const named = (text: string) => EditorView.contentAttributes.of({ 'aria-label': text });
  if (mode === 'split') {
    const view = new MergeView({
      a: { doc: d.old, extensions: [...base, named(`${label}, version ${d.from}`)] },
      b: { doc: d.new, extensions: [...base, named(`${label}, version ${d.to}`)] },
      parent,
      root,
      gutter: true,
      collapseUnchanged: COLLAPSE,
    });
    return () => view.destroy();
  }
  const view = new EditorView({
    doc: d.new,
    parent,
    root,
    extensions: [
      ...base,
      unifiedMergeView({ original: d.old, mergeControls: false, gutter: true, collapseUnchanged: COLLAPSE }),
      named(`${label}, version ${d.from} to ${d.to}`),
    ],
  });
  return () => view.destroy();
}
