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
  snippetCompletion,
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

// snip builds a snippet completion (tab-through placeholders) with a signature
// (detail) and prose (info) — the full contract shown as you type.
const snip = (label: string, template: string, detail: string, info: string, type = 'function'): Completion =>
  snippetCompletion(template, { label, detail, info, type });

// Top-level globals — mirror the snapshot surface (agent/internal/actions/
// script_snapshot.go), the exact surface built-in scripts, their forks and custom
// workflows all execute on. shown == executed.
const BUILTIN_COMPLETIONS: Completion[] = [
  snip('step', 'step("${name}")', 'step(name)', 'Begin a new timeline step (shown live in the UI and Runs).'),
  snip('report', 'report("${detail}")', 'report(detail)', 'Progress detail attached to the current step.'),
  snip('fail', 'fail("${message}")', 'fail(message)', 'Abort as failed — every snapshotted change is rolled back automatically.'),
  snip('sleep', 'sleep(${5})', 'sleep(seconds)', 'Pause (bounded).'),
  snip('snapshot', 'snapshot(${ns}, "${Deployment}", ${name})', 'snapshot(namespace, kind, name) → dict', 'Explicitly capture an object before mutating it (mutators also auto-capture). Restore replays these captures.'),
  { label: 'args', type: 'variable', detail: 'dict[str,str]', info: 'Input parameters. All values are strings — cast with int()/float() as needed.' },
  { label: 'k8s', type: 'namespace', detail: 'module', info: 'Cluster operations. Type "k8s." for the full method list.' },
  { label: 'json', type: 'namespace', detail: 'module', info: 'json.decode(str) / json.encode(value) — parse or build JSON (e.g. a merge patch).' },
  snip('wait_rollout', 'wait_rollout(${ns}, "${Deployment}", ${name}, timeout=${120})', 'wait_rollout(namespace, kind, name, timeout=120) → bool', 'Wait until the new generation is fully rolled out on POD level (old gone, new ready). Returns False on timeout — you decide (fail() → auto-rollback).'),
  snip('wait_ready', 'wait_ready(${ns}, "${Deployment}", ${name}, timeout=${120})', 'wait_ready(namespace, kind, name, timeout=120) → bool', 'Wait until pods == desired and every pod is ready. Returns False on timeout.'),
];

// k8s.<member> — the snapshot surface (script_snapshot.go). Mutators auto-capture
// what they touch, so a failure rolls back and a succeeded run stays revertible.
const K8S_COMPLETIONS: Completion[] = [
  // reads (no snapshot, safe)
  snip('get', 'get(${ns}, "${Deployment}", ${name})', 'k8s.get(namespace, kind, name) → dict', 'Workload state: {desired, ready, updated, available}.', 'method'),
  snip('raw_get', 'raw_get("${v1}", "${ConfigMap}", ${ns}, ${name})', 'k8s.raw_get(apiVersion, kind, namespace, name, resource="") → dict|None', 'Read any object (CRDs via resource=plural). Secret values are redacted.', 'method'),
  snip('raw_list', 'raw_list("${apps/v1}", "${ReplicaSet}", ${ns})', 'k8s.raw_list(apiVersion, kind, namespace, selector="", resource="") → list', 'List any kind in a namespace.', 'method'),
  snip('pods', 'pods(${ns})', 'k8s.pods(namespace, selector="") → list', '[{name, ready, phase, restarts, node}].', 'method'),
  snip('events', 'events(${ns}, "${name}")', 'k8s.events(namespace, name="") → list', '[{type, reason, message, count, object}].', 'method'),
  // mutators (auto-snapshot → auto-rollback on failure + revertible)
  snip('patch', 'patch(${ns}, "${Deployment}", ${name}, ${patch})', 'k8s.patch(namespace, kind, name, patch, strategic=False)', 'Whole-object merge patch (strategic=True for container arrays). Snapshots the object first.', 'method'),
  snip('set_field', 'set_field(${ns}, "${Deployment}", ${name}, ["metadata", "annotations", "${key}"], "${value}")', 'k8s.set_field(namespace, kind, name, path, value)', 'Set one field path (value=None removes it). Field-scoped restore keeps sibling keys.', 'method'),
  snip('set_fields', 'set_fields(${ns}, "${PodDisruptionBudget}", ${name}, [(["spec", "minAvailable"], ${1}), (["spec", "maxUnavailable"], None)])', 'k8s.set_fields(namespace, kind, name, entries)', 'Set several field paths in ONE atomic patch (mutually-exclusive pairs).', 'method'),
  snip('scale', 'scale(${ns}, "${Deployment}", ${name}, ${2})', 'k8s.scale(namespace, kind, name, replicas)', 'Set replicas. Snapshots the prior count.', 'method'),
  snip('patch_configmap', 'patch_configmap(${ns}, ${name}, "${key}", "${value}")', 'k8s.patch_configmap(namespace, name, key, value)', 'Set one ConfigMap key (field-scoped).', 'method'),
  snip('create', 'create(${manifest})', 'k8s.create(manifest)', 'Create an object (manifest needs apiVersion, kind, metadata.name). Restore deletes it.', 'method'),
  snip('delete', 'delete(${ns}, "${Pod}", ${name})', 'k8s.delete(namespace, kind, name)', 'Snapshot + delete. Restore recreates it from the capture.', 'method'),
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
