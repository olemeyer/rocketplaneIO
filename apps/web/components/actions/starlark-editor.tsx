'use client';

import { useEffect, useRef } from 'react';
import { EditorView, keymap, lineNumbers, highlightActiveLine, placeholder } from '@codemirror/view';
import { EditorState } from '@codemirror/state';
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
import { linter, lintGutter, type Diagnostic } from '@codemirror/lint';
import { tags } from '@lezer/highlight';
import {
  lintWorkflow, parseFrontmatter,
  RISK_VALUES, REVERSIBLE_VALUES, PARAM_TYPES, type Diag,
} from '@/lib/workflow-frontmatter';

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
  snip('net_probe', 'net_probe("${http}", "${target}")', 'net_probe(mode, target, method="GET") → str', 'Reachability/TLS/DNS diagnostics from the agent (mode: http|tcp|dns|tls). Read-only.'),
];

// k8s.<member> — the snapshot surface (script_snapshot.go). Mutators auto-capture
// what they touch, so a failure rolls back and a succeeded run stays revertible.
const K8S_COMPLETIONS: Completion[] = [
  // reads (no snapshot, safe)
  snip('get', 'get("${v1}", "${ConfigMap}", ${ns}, ${name})', 'k8s.get(apiVersion, kind, namespace, name, resource="") → dict|None', 'Read the full object of any kind (CRDs via resource=plural). Secret values are redacted.', 'method'),
  snip('list', 'list("${apps/v1}", "${ReplicaSet}", ${ns})', 'k8s.list(apiVersion, kind, namespace, selector="", resource="") → list', 'List any kind in a namespace.', 'method'),
  snip('status', 'status(${ns}, "${Deployment}", ${name})', 'k8s.status(namespace, kind, name) → dict', 'Workload rollout summary: {desired, ready, updated, available}.', 'method'),
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
  snip('apply', 'apply(${manifest})', 'k8s.apply(manifest)', 'Server-side apply a whole object as desired truth (snapshots first, so it stays revertible).', 'method'),
  // host-capability primitives (script_host.go) — generic like patch/scale
  snip('exec', 'exec(${ns}, ${pod}, [${"cat", "/path"}])', 'k8s.exec(namespace, name, command, container="", retry=False) → str', 'Run ONE read-only whitelisted argv in a container (no shell). retry catches a CrashLooping pod.', 'method'),
  snip('logs', 'logs(${ns}, ${pod})', 'k8s.logs(namespace, name, container="", previous=False, tail=200) → str', "Read a pod's logs from the kubelet (previous=True for the crashed container).", 'method'),
  snip('evict', 'evict(${ns}, ${pod})', 'k8s.evict(namespace, name, grace_seconds=None) → str', 'PDB-aware Eviction API for one pod. Returns "evicted"|"notfound"|"blocked". Final (not revertible).', 'method'),
  snip('node_pods', 'node_pods(${node})', 'k8s.node_pods(node) → list', 'Drainable pods on a node ([{namespace, name}]); DaemonSet/mirror/terminating pods excluded.', 'method'),
  snip('pod_status', 'pod_status(${ns}, ${pod})', 'k8s.pod_status(namespace, name) → dict|None', 'One pod run status {phase, terminated, exit_code, reason, stuck}.', 'method'),
];

// Front-matter completions — the `# @key value` block the editor co-evaluates.
const FM_KEYS: Completion[] = [
  snip('name', 'name ${my-workflow}', '# @name <kebab-case>', 'Workflow name (required).', 'keyword'),
  snip('summary', 'summary ${one-line description}', '# @summary <text>', 'One-line description shown in the library.', 'keyword'),
  snip('risk', 'risk ${medium}', '# @risk low|medium|high', 'Risk level for the guardrails.', 'keyword'),
  snip('reversible', 'reversible ${snapshot}', '# @reversible snapshot|readonly|none', 'How the run can be undone.', 'keyword'),
  snip('targets', 'targets ${Deployment,StatefulSet}', '# @targets <Kind,Kind>', 'Comma-separated target kinds.', 'keyword'),
  snip('param', 'param ${name} ${string}', '# @param <name> <type> [min max | opts…] [required] [=default]', 'A run parameter (becomes args["name"]).', 'keyword'),
  snip('timeout', 'timeout ${600}', '# @timeout <seconds>', 'Run timeout (30–1800). Default 600.', 'keyword'),
];
const kw = (labels: string[]): Completion[] => labels.map((l) => ({ label: l, type: 'constant' }));

// frontmatterCompletion suggests @keys and enum values inside the `# @…` block.
function frontmatterCompletion(ctx: import('@codemirror/autocomplete').CompletionContext) {
  const line = ctx.state.doc.lineAt(ctx.pos);
  const text = line.text;
  if (!/^\s*#/.test(text)) return null; // only inside comments
  // value slots
  let m = ctx.matchBefore(/@risk\s+\w*/);
  if (m && /@risk\s+\w*$/.test(text.slice(0, ctx.pos - line.from))) {
    const v = ctx.matchBefore(/\w*/);
    if (v) return { from: v.from, options: kw(RISK_VALUES), validFor: /^\w*$/ };
  }
  m = ctx.matchBefore(/@reversible\s+\w*/);
  if (m && /@reversible\s+\w*$/.test(text.slice(0, ctx.pos - line.from))) {
    const v = ctx.matchBefore(/\w*/);
    if (v) return { from: v.from, options: kw(REVERSIBLE_VALUES), validFor: /^\w*$/ };
  }
  if (/^\s*#\s*@param\s+\S+\s+\w*$/.test(text.slice(0, ctx.pos - line.from))) {
    const v = ctx.matchBefore(/\w*/);
    if (v) return { from: v.from, options: kw(PARAM_TYPES), validFor: /^\w*$/ };
  }
  // key slot: "# @<word>"
  const key = ctx.matchBefore(/@\w*/);
  if (key) return { from: key.from + 1, options: FM_KEYS, validFor: /^\w*$/ };
  return null;
}

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

// parseErrLine pulls the 1-based line from a compiler error like
// "workflow.star:12:3: undefined: foo" → 11 (0-based). Falls back to 0.
function parseErrLine(err: string): number {
  const m = err.match(/:(\d+):\d+:/) || err.match(/:(\d+):/);
  return m ? Math.max(0, Number(m[1]) - 1) : 0;
}

export function StarlarkEditor({
  value,
  onChange,
  check,
  onDiagnostics,
}: {
  value: string;
  onChange: (v: string) => void;
  /** async compile against the agent surface (parse + resolve) for live squiggles. */
  check?: (source: string) => Promise<{ ok: boolean; error?: string }>;
  /** merged client + compiler diagnostics — parent uses them to gate Save + summarise. */
  onDiagnostics?: (diags: Diag[]) => void;
}) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const viewRef = useRef<EditorView | null>(null);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  const checkRef = useRef(check);
  checkRef.current = check;
  const onDiagRef = useRef(onDiagnostics);
  onDiagRef.current = onDiagnostics;

  useEffect(() => {
    if (!hostRef.current) return;

    // args["…"] completions derive from the @param front-matter in the doc itself.
    const argCompletions = (doc: string): Completion[] =>
      parseFrontmatter(doc).params
        .filter((p) => p.name)
        .map((p) => ({
          label: `args["${p.name}"]`,
          type: 'property',
          detail: p.type,
          info: `Parameter ${p.name}${p.default ? ` (default ${p.default})` : ''}${p.required ? ' · required' : ''}`,
        }));

    // linter: client-side front-matter + snapshot-coherence lint, plus the async
    // control-plane compile (syntax/unknown-name) mapped to the failing line.
    const lintSource = async (view: EditorView): Promise<Diagnostic[]> => {
      const doc = view.state.doc.toString();
      const { diags } = lintWorkflow(doc);
      const all: Diag[] = [...diags];
      const fn = checkRef.current;
      if (fn) {
        try {
          const r = await fn(doc);
          if (!r.ok && r.error) all.push({ line: parseErrLine(r.error), severity: 'error', message: r.error });
        } catch {
          /* offline: keep client diagnostics only */
        }
      }
      onDiagRef.current?.(all);
      const nLines = view.state.doc.lines;
      return all.map((d): Diagnostic => {
        const ln = view.state.doc.line(Math.min(d.line + 1, nLines));
        return { from: ln.from, to: ln.to, severity: d.severity, message: d.message };
      });
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
        lintGutter(),
        linter(lintSource, { delay: 400 }),
        autocompletion({
          override: [
            frontmatterCompletion,
            // k8s.<member> — kontextsensitiv vor dem Punkt
            (ctx) => {
              const before = ctx.matchBefore(/k8s\.\w*/);
              if (!before) return null;
              return { from: before.from + 4, options: K8S_COMPLETIONS, validFor: /^\w*$/ };
            },
            (ctx) => {
              const word = ctx.matchBefore(/[\w"[\]]*/);
              if (!word && !ctx.explicit) return null;
              return completeFromList([...BUILTIN_COMPLETIONS, ...argCompletions(ctx.state.doc.toString())])(ctx);
            },
          ],
        }),
        keymap.of([...defaultKeymap, ...historyKeymap, indentWithTab]),
        placeholder('# @name my-workflow\n# @reversible snapshot\n#\n# your workflow…'),
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
    // bewusst nur beim Mount — value-Sync unten
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Externe value-Änderungen (z.B. Fork/Definition wechseln) einspielen.
  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    if (view.state.doc.toString() !== value) {
      view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: value } });
    }
  }, [value]);

  return <div ref={hostRef} className="h-full min-h-[360px] overflow-auto [&_.cm-editor]:h-full" />;
}
