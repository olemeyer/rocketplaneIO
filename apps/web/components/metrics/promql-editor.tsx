'use client';

import { useEffect, useRef } from 'react';
import { EditorView, keymap, placeholder } from '@codemirror/view';
import { EditorState, Prec } from '@codemirror/state';
import { defaultKeymap, history, historyKeymap } from '@codemirror/commands';
import { autocompletion, closeBrackets } from '@codemirror/autocomplete';
import { bracketMatching, syntaxHighlighting, HighlightStyle } from '@codemirror/language';
import { tags } from '@lezer/highlight';
import { PromQLExtension } from '@prometheus-io/codemirror-promql';

// promql-editor.tsx — der offizielle Prometheus-CodeMirror-Editor (Syntax,
// Linting, Autocomplete) gegen UNSERE PromQL-API (eingebettete Engine auf
// ClickHouse). Eine Zeile Fokus, Shift/Cmd+Enter führt aus.

const highlight = HighlightStyle.define([
  { tag: tags.keyword, color: '#9C8DC9' },
  { tag: tags.string, color: '#5FAFA7' },
  { tag: tags.number, color: '#C78FAD' },
  { tag: tags.function(tags.variableName), color: '#7C9EC9' },
  { tag: tags.labelName, color: '#6FA8CC' },
  { tag: tags.operator, color: 'var(--rp-ink-mid)' },
]);

const theme = EditorView.theme({
  '&': {
    backgroundColor: 'var(--rp-inset)',
    color: 'var(--rp-ink)',
    fontSize: '13px',
    borderRadius: '6px',
    border: '1px solid var(--rp-line)',
  },
  '&.cm-focused': { outline: '2px solid var(--rp-accent)', outlineOffset: '2px' },
  '.cm-scroller': { fontFamily: 'var(--font-mono, ui-monospace, monospace)', lineHeight: '1.7', padding: '4px 2px' },
  '.cm-tooltip': {
    backgroundColor: 'var(--rp-overlay)',
    border: '1px solid var(--rp-line)',
    borderRadius: '6px',
    color: 'var(--rp-ink)',
    fontFamily: 'var(--font-mono, ui-monospace, monospace)',
    fontSize: '11px',
    boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)',
  },
  '.cm-tooltip-autocomplete ul li[aria-selected]': { backgroundColor: 'var(--rp-hover)', color: 'var(--rp-ink)' },
  '.cm-completionDetail': { color: 'var(--rp-ink-muted)', fontStyle: 'normal' },
  '.cm-completionInfo': { backgroundColor: 'var(--rp-overlay)', border: '1px solid var(--rp-line)', color: 'var(--rp-ink-mid)', maxWidth: '360px' },
  '.cm-diagnostic-error': { borderLeft: '3px solid var(--rp-red)' },
  '.cm-cursor': { borderLeftColor: 'var(--rp-ink)' },
  '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': {
    backgroundColor: 'color-mix(in oklab, var(--rp-ink) 14%, transparent) !important',
  },
});

export function PromQLEditor({
  value,
  apiPrefix,
  onChange,
  onRun,
}: {
  value: string;
  /** z.B. /api/orgs/…/clusters/…/promql — der Editor hängt /api/v1/… an */
  apiPrefix: string;
  onChange: (v: string) => void;
  onRun: () => void;
}) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const viewRef = useRef<EditorView | null>(null);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  const onRunRef = useRef(onRun);
  onRunRef.current = onRun;

  useEffect(() => {
    if (!hostRef.current) return;
    const promql = new PromQLExtension()
      .activateCompletion(true)
      .activateLinter(true)
      .setComplete({ remote: { url: apiPrefix, httpMethod: 'GET' } });

    const state = EditorState.create({
      doc: value,
      extensions: [
        history(),
        bracketMatching(),
        closeBrackets(),
        autocompletion(),
        promql.asExtension(),
        syntaxHighlighting(highlight),
        theme,
        placeholder('histogram_quantile(0.95, sum by (le, service_name) (rate(http_server_request_duration_bucket[5m])))'),
        Prec.highest(
          keymap.of([
            {
              key: 'Shift-Enter',
              run: () => {
                onRunRef.current();
                return true;
              },
            },
            {
              key: 'Mod-Enter',
              run: () => {
                onRunRef.current();
                return true;
              },
            },
          ]),
        ),
        keymap.of([...defaultKeymap, ...historyKeymap]),
        EditorView.updateListener.of((u) => {
          if (u.docChanged) onChangeRef.current(u.state.doc.toString());
        }),
      ],
    });
    const view = new EditorView({ state, parent: hostRef.current });
    viewRef.current = view;
    return () => {
      view.destroy();
      viewRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apiPrefix]);

  useEffect(() => {
    const view = viewRef.current;
    if (view && view.state.doc.toString() !== value) {
      view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: value } });
    }
  }, [value]);

  return <div ref={hostRef} className="min-h-[44px]" />;
}
