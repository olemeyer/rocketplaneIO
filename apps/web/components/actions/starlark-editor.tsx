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

// Top-level globals — mirror agent/internal/actions/script.go exactly.
const BUILTIN_COMPLETIONS: Completion[] = [
  snip('step', 'step("${name}")', 'step(name)', 'Begin a new timeline step (shown live in the UI and Runs).'),
  snip('report', 'report("${detail}")', 'report(detail)', 'Live progress line of the current step (e.g. "rollout 1/3 ready").'),
  snip('fail', 'fail("${message}")', 'fail(message)', 'Abort the workflow as failed — triggers LIFO rollback of registered undos.'),
  snip('sleep', 'sleep(${5})', 'sleep(seconds)', 'Pause (max 30s per call).'),
  { label: 'args', type: 'variable', detail: 'dict[str,str]', info: 'Input parameters. All values are strings — cast with int()/float() as needed.' },
  { label: 'k8s', type: 'namespace', detail: 'module', info: 'Cluster operations. Type "k8s." for the full method list.' },
  snip('wait_rollout', 'wait_rollout(${ns}, "${Deployment}", ${name}, timeout=${120})', 'wait_rollout(namespace, kind, name, timeout=120) → bool', 'Wait until the new generation is fully rolled out on POD level (old gone, new ready). Returns False on timeout — you decide the rollback.'),
  snip('wait_ready', 'wait_ready(${ns}, "${Deployment}", ${name}, timeout=${120})', 'wait_ready(namespace, kind, name, timeout=120) → bool', 'Wait until pods == desired and every pod is ready. Returns False on timeout.'),
];

// k8s.<member> — every builtin the agent registers (script.go k8sModule +
// script_raw.go rawMembers). Applied after the "k8s." prefix.
const K8S_COMPLETIONS: Completion[] = [
  snip('get', 'get(${ns}, "${Deployment}", ${name})', 'k8s.get(namespace, kind, name) → dict', 'Read workload state: {desired, ready, updated, available, generationCaughtUp}.', 'method'),
  snip('pods', 'pods(${ns})', 'k8s.pods(namespace, selector="") → list', 'List pods: [{name, ready, phase, restarts, node}].', 'method'),
  snip('scale', 'scale(${ns}, "${Deployment}", ${name}, ${2})', 'k8s.scale(namespace, kind, name, replicas)', 'Set replicas (0–50). Auto-registers a rollback to the prior count.', 'method'),
  snip('rollout_restart', 'rollout_restart(${ns}, "${Deployment}", ${name})', 'k8s.rollout_restart(namespace, kind, name)', 'Trigger a rolling restart (patches a restartedAt annotation).', 'method'),
  snip('rollout_undo', 'rollout_undo(${ns}, ${name})', 'k8s.rollout_undo(namespace, name)', 'Roll a Deployment back to its previous revision.', 'method'),
  snip('set_image', 'set_image(${ns}, "${Deployment}", ${name}, "${image}", container="${c}")', 'k8s.set_image(namespace, kind, name, image, container="")', 'Set a container image. container defaults to the sole container. Registers a rollback to the prior image.', 'method'),
  snip('delete_pod', 'delete_pod(${ns}, ${name})', 'k8s.delete_pod(namespace, name)', 'Delete one pod (the owner recreates it). Irreversible.', 'method'),
  snip('pause', 'pause(${ns}, ${name})', 'k8s.pause(namespace, name)', 'Pause a Deployment rollout. Rollback = resume.', 'method'),
  snip('resume', 'resume(${ns}, ${name})', 'k8s.resume(namespace, name)', 'Resume a paused Deployment rollout.', 'method'),
  snip('cordon', 'cordon(${node})', 'k8s.cordon(node)', 'Mark a node unschedulable. Rollback = uncordon.', 'method'),
  snip('uncordon', 'uncordon(${node})', 'k8s.uncordon(node)', 'Mark a node schedulable again.', 'method'),
  snip('hpa_set', 'hpa_set(${ns}, ${name}, ${min}, ${max})', 'k8s.hpa_set(namespace, name, min, max)', 'Set an HPA\'s min/max replica bounds. Registers a rollback to the prior bounds.', 'method'),
  snip('cronjob_trigger', 'cronjob_trigger(${ns}, ${name})', 'k8s.cronjob_trigger(namespace, name)', 'Create a Job from a CronJob now. Irreversible.', 'method'),
  snip('cronjob_suspend', 'cronjob_suspend(${ns}, ${name})', 'k8s.cronjob_suspend(namespace, name)', 'Suspend a CronJob. Rollback = resume.', 'method'),
  snip('cronjob_resume', 'cronjob_resume(${ns}, ${name})', 'k8s.cronjob_resume(namespace, name)', 'Resume a suspended CronJob.', 'method'),
  snip('taint', 'taint(${node}, "${key}", value="${v}", effect="${NoSchedule}")', 'k8s.taint(node, key, value="", effect="NoSchedule")', 'Add a node taint. Rollback = untaint.', 'method'),
  snip('untaint', 'untaint(${node}, "${key}")', 'k8s.untaint(node, key)', 'Remove a node taint by key.', 'method'),
  snip('events', 'events(${ns}, ${name})', 'k8s.events(namespace, name) → list', 'Recent events for an object: [{type, reason, message, count, age}].', 'method'),
  // Generic escape hatch (script_raw.go) — any whitelisted GVR.
  snip('raw_get', 'raw_get("${v1}", "${Pod}", ${ns}, ${name})', 'k8s.raw_get(apiVersion, kind, namespace, name) → dict', 'Read any whitelisted resource as a plain dict.', 'method'),
  snip('raw_list', 'raw_list("${apps/v1}", "${ReplicaSet}", ${ns})', 'k8s.raw_list(apiVersion, kind, namespace) → list', 'List any whitelisted resource in a namespace.', 'method'),
  snip('raw_apply', 'raw_apply("${apps/v1}", "${Deployment}", ${ns}, ${name}, ${obj})', 'k8s.raw_apply(apiVersion, kind, namespace, name, object)', 'Server-side apply an object (registers a rollback to the before-state).', 'method'),
  snip('raw_patch', 'raw_patch("${apps/v1}", "${Deployment}", ${ns}, ${name}, ${patch})', 'k8s.raw_patch(apiVersion, kind, namespace, name, patch)', 'Strategic-merge patch (registers a rollback to the before-state).', 'method'),
  snip('raw_delete', 'raw_delete("${v1}", "${Pod}", ${ns}, ${name})', 'k8s.raw_delete(apiVersion, kind, namespace, name)', 'Delete any whitelisted resource. Irreversible.', 'method'),
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
