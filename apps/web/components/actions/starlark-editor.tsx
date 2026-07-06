'use client';

import { useEffect, useRef } from 'react';
import { EditorView, keymap, lineNumbers, highlightActiveLine, placeholder } from '@codemirror/view';
import { EditorState, Compartment } from '@codemirror/state';
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
import { python } from '@codemirror/lang-python';
import {
  autocompletion,
  closeBrackets,
  completeFromList,
  type Completion,
} from '@codemirror/autocomplete';
import { bracketMatching, indentUnit, syntaxHighlighting, HighlightStyle } from '@codemirror/language';
import { tags } from '@lezer/highlight';
import type { ActionDefParam } from '@/lib/api/types';

// starlark-editor.tsx — echter Code-Editor für Workflows: CodeMirror 6 mit
// Python-Grammatik (Starlark ist ein Subset), RETICLE-Theme über CSS-Variablen
// (folgt Dark/Light automatisch) und COMPLETION für den kompletten Script-
// Vertrag: Builtins mit Signatur + Doku, k8s.-Member kontextsensitiv, und die
// definierten Parameter als args["…"]-Vorschläge.

const BUILTIN_COMPLETIONS: Completion[] = [
  { label: 'step', apply: 'step("")', type: 'function', detail: 'step(name)', info: 'Begin a new timeline step (shown live in the UI).' },
  { label: 'report', apply: 'report("")', type: 'function', detail: 'report(detail)', info: 'Live progress line of the current step.' },
  { label: 'fail', apply: 'fail("")', type: 'function', detail: 'fail(msg)', info: 'Abort the workflow as failed.' },
  { label: 'sleep', apply: 'sleep(5)', type: 'function', detail: 'sleep(seconds)', info: 'Pause (max 30s per call).' },
  { label: 'args', type: 'variable', detail: 'dict', info: 'Input parameters (all values are strings — cast with int()).' },
  { label: 'k8s', type: 'namespace', detail: 'module', info: 'Cluster operations: get, pods, scale, rollout_restart, delete_pod.' },
  {
    label: 'wait_rollout',
    apply: 'wait_rollout(ns, "Deployment", name, timeout=120)',
    type: 'function',
    detail: 'wait_rollout(namespace, kind, name, timeout=120) → bool',
    info: 'Wait until the new generation is fully rolled out (pod-level). False on timeout.',
  },
  {
    label: 'wait_ready',
    apply: 'wait_ready(ns, "Deployment", name, timeout=120)',
    type: 'function',
    detail: 'wait_ready(namespace, kind, name, timeout=120) → bool',
    info: 'Wait until pods == desired and every pod is ready. False on timeout.',
  },
];

const K8S_COMPLETIONS: Completion[] = [
  { label: 'get', apply: 'get(ns, "Deployment", name)', type: 'method', detail: 'k8s.get(namespace, kind, name) → dict', info: 'Read workload state: {desired, ready, updated, available}.' },
  { label: 'pods', apply: 'pods(ns)', type: 'method', detail: 'k8s.pods(namespace, selector="") → list', info: 'List pods: [{name, ready, phase, restarts, node}].' },
  { label: 'scale', apply: 'scale(ns, "Deployment", name, 2)', type: 'method', detail: 'k8s.scale(namespace, kind, name, replicas)', info: 'Set replicas (0–50). Auto-registers a rollback entry.' },
  { label: 'rollout_restart', apply: 'rollout_restart(ns, "Deployment", name)', type: 'method', detail: 'k8s.rollout_restart(namespace, kind, name)', info: 'Trigger a rolling restart.' },
  { label: 'delete_pod', apply: 'delete_pod(ns, name)', type: 'method', detail: 'k8s.delete_pod(namespace, name)', info: 'Delete one pod (owner recreates it).' },
];

// RETICLE-Syntax-Farben: ruhige, gedeckte Hues — Status-Farben bleiben Daten.
const highlight = HighlightStyle.define([
  { tag: tags.keyword, color: '#9C8DC9' },
  { tag: tags.string, color: '#5FAFA7' },
  { tag: tags.number, color: '#C78FAD' },
  { tag: tags.comment, color: 'var(--rp-ink-faint)', fontStyle: 'italic' },
  { tag: tags.function(tags.variableName), color: '#7C9EC9' },
  { tag: tags.definition(tags.variableName), color: 'var(--rp-ink)' },
  { tag: tags.operator, color: 'var(--rp-ink-mid)' },
  { tag: tags.bool, color: '#C78FAD' },
]);

const reticleTheme = EditorView.theme({
  '&': {
    backgroundColor: 'var(--rp-inset)',
    color: 'var(--rp-ink)',
    fontSize: '12px',
    borderRadius: 'var(--rp-radius-sm, 6px)',
    border: '1px solid var(--rp-line)',
  },
  '&.cm-focused': { outline: '2px solid var(--rp-accent)', outlineOffset: '2px' },
  '.cm-scroller': { fontFamily: 'var(--font-mono, ui-monospace, monospace)', lineHeight: '1.6' },
  '.cm-gutters': {
    backgroundColor: 'var(--rp-inset)',
    color: 'var(--rp-ink-faint)',
    border: 'none',
    borderRight: '1px solid var(--rp-line)',
  },
  '.cm-activeLine': { backgroundColor: 'var(--rp-hover)' },
  '.cm-activeLineGutter': { backgroundColor: 'var(--rp-hover)', color: 'var(--rp-ink-muted)' },
  '.cm-cursor': { borderLeftColor: 'var(--rp-ink)' },
  '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': {
    backgroundColor: 'color-mix(in oklab, var(--rp-ink) 14%, transparent) !important',
  },
  '.cm-matchingBracket': {
    backgroundColor: 'color-mix(in oklab, var(--rp-ink) 12%, transparent)',
    outline: '1px solid var(--rp-line-strong)',
  },
  '.cm-tooltip': {
    backgroundColor: 'var(--rp-overlay)',
    border: '1px solid var(--rp-line)',
    borderRadius: '6px',
    color: 'var(--rp-ink)',
    fontFamily: 'var(--font-mono, ui-monospace, monospace)',
    fontSize: '11px',
    boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)',
  },
  '.cm-tooltip-autocomplete ul li[aria-selected]': {
    backgroundColor: 'var(--rp-hover)',
    color: 'var(--rp-ink)',
  },
  '.cm-completionDetail': { color: 'var(--rp-ink-muted)', fontStyle: 'normal', marginLeft: '0.8em' },
  '.cm-completionInfo': {
    backgroundColor: 'var(--rp-overlay)',
    border: '1px solid var(--rp-line)',
    color: 'var(--rp-ink-mid)',
  },
});

export function StarlarkEditor({
  value,
  onChange,
  params,
}: {
  value: string;
  onChange: (v: string) => void;
  /** Definierte Parameter → args["…"]-Completions. */
  params: ActionDefParam[];
}) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const viewRef = useRef<EditorView | null>(null);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  const paramsRef = useRef(params);
  paramsRef.current = params;
  const completionCompartment = useRef(new Compartment());

  useEffect(() => {
    if (!hostRef.current) return;

    const completionSource = () => {
      const argCompletions: Completion[] = paramsRef.current
        .filter((p) => p.name)
        .map((p) => ({
          label: `args["${p.name}"]`,
          type: 'property',
          detail: p.type ?? 'string',
          info: p.description || `Parameter ${p.name}${p.default ? ` (default ${p.default})` : ''}`,
        }));
      return [
        autocompletion({
          override: [
            // k8s.<member> — kontextsensitiv vor dem Punkt
            (ctx) => {
              const before = ctx.matchBefore(/k8s\.\w*/);
              if (!before) return null;
              return {
                from: before.from + 4,
                options: K8S_COMPLETIONS,
                validFor: /^\w*$/,
              };
            },
            completeFromList([...BUILTIN_COMPLETIONS, ...argCompletions]),
          ],
        }),
      ];
    };

    const state = EditorState.create({
      doc: value,
      extensions: [
        lineNumbers(),
        history(),
        highlightActiveLine(),
        bracketMatching(),
        closeBrackets(),
        indentUnit.of('    '),
        python(),
        syntaxHighlighting(highlight),
        reticleTheme,
        completionCompartment.current.of(completionSource()),
        keymap.of([...defaultKeymap, ...historyKeymap, indentWithTab]),
        placeholder('# your workflow…'),
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
    // bewusst nur beim Mount — value-Sync unten, Completions via Ref
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Externe value-Änderungen (z.B. Definition wechseln) einspielen.
  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    if (view.state.doc.toString() !== value) {
      view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: value } });
    }
  }, [value]);

  return <div ref={hostRef} className="max-h-[520px] min-h-[360px] overflow-auto" />;
}
