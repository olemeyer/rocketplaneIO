'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useParams } from 'next/navigation';
import { cn } from '@/lib/cn';
import { Spinner } from '@/components/ui';
import { useMe } from '@/components/app/me-context';
import { PageHeader } from '@/components/app/page-header';
import { useClusterEvents } from '@/lib/hooks/use-cluster-events';
import { useInfra } from '@/lib/hooks/use-infra';
import { ActionRunCard } from '@/components/actions/run-card';
import { StarlarkEditor } from '@/components/actions/starlark-editor';
import {
  cancelAction,
  createAction,
  createActionDefinition,
  deleteActionDefinition,
  getActionDefinitions,
  getActions,
  getServiceMap,
  runScriptAction,
  updateActionDefinition,
} from '@/lib/api/controlplane';
import type { ActionDefParam, ActionDefinition, ActionKind, ClusterAction, ServiceMap } from '@/lib/api/types';
import { BUILTIN_STARLARK } from '@/lib/action-templates';

// Actions — der AKT-Bereich: links die LIBRARY (eingebaute Handgriffe +
// org-weite Custom-WORKFLOWS in Starlark), rechts die LIVE-Runs des Clusters.
// Workflows sind vollwertige Problemlösungs-Pipelines: hermetisches Python
// (kein I/O, garantierte Terminierung), step()/report() speisen dieselbe
// Timeline wie die eingebauten Pipelines, wait_* verifiziert auf Pod-Ebene.

const POLL_MS = 2500;

// Eingebaute Pipelines — dieselben, die das Workload-Panel anbietet.
const BUILTINS: { kind: string; name: string; description: string; fields: ActionDefParam[] }[] = [
  {
    kind: 'rollout_restart',
    name: 'rollout restart',
    description: 'Restart a workload: snapshot → trigger → rollout → drain old → pods ready → verify.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'kind', type: 'enum', options: ['Deployment', 'StatefulSet', 'DaemonSet'], default: 'Deployment' },
      { name: 'name', type: 'workload', required: true },
    ],
  },
  {
    kind: 'scale',
    name: 'scale',
    description: 'Set replicas (0–50) and wait until every pod is ready and settled — up and down.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'kind', type: 'enum', options: ['Deployment', 'StatefulSet'], default: 'Deployment' },
      { name: 'name', type: 'workload', required: true },
      { name: 'replicas', type: 'int', min: 0, max: 50, default: '2', required: true },
    ],
  },
  {
    kind: 'delete_pod',
    name: 'recreate pod',
    description: 'Delete one pod, wait for full drain and a ready replacement.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'pod', type: 'string', required: true, description: 'exact pod name' },
    ],
  },
  {
    kind: 'rollout_undo',
    name: 'rollout undo',
    description: 'Roll back to the previous revision: snapshot → rollback → rollout → drain old → pods ready → verify.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'name', type: 'workload', required: true },
    ],
  },
  {
    kind: 'cordon',
    name: 'cordon node',
    description: 'Mark a node unschedulable (verified). Existing pods keep running.',
    fields: [{ name: 'node', type: 'node', required: true }],
  },
  {
    kind: 'uncordon',
    name: 'uncordon node',
    description: 'Make a node schedulable again (verified).',
    fields: [{ name: 'node', type: 'node', required: true }],
  },
  {
    kind: 'drain',
    name: 'drain node',
    description: 'Cordon, then evict every pod via the Eviction API (respects PodDisruptionBudgets, keeps DaemonSets) and wait until the node is empty. Cancel re-uncordons.',
    fields: [{ name: 'node', type: 'node', required: true }],
  },
  {
    kind: 'set_image',
    name: 'set image',
    description: 'Set a container image (kubectl set image) and run the full verified rollout. Cancel restores the previous image.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'kind', type: 'enum', options: ['Deployment', 'StatefulSet', 'DaemonSet'], default: 'Deployment' },
      { name: 'name', type: 'workload', required: true },
      { name: 'container', type: 'string', description: 'blank = the sole container' },
      { name: 'image', type: 'string', required: true, description: 'repo/image:tag' },
    ],
  },
  {
    kind: 'rollout_pause',
    name: 'rollout pause',
    description: 'Freeze a Deployment rollout (kubectl rollout pause). Cancel resumes it.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'name', type: 'workload', required: true },
    ],
  },
  {
    kind: 'rollout_resume',
    name: 'rollout resume',
    description: 'Resume a paused Deployment and watch the rollout to verified stability.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'name', type: 'workload', required: true },
    ],
  },
  {
    kind: 'hpa_set',
    name: 'hpa set bounds',
    description: 'Set a HorizontalPodAutoscaler min/max (the honest scaling in HPA clusters). Cancel restores the old bounds.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'name', type: 'string', required: true, description: 'HPA name' },
      { name: 'minReplicas', type: 'int', min: 1, max: 200, default: '1' },
      { name: 'maxReplicas', type: 'int', min: 1, max: 200, default: '5', required: true },
    ],
  },
  {
    kind: 'cronjob_trigger',
    name: 'cronjob trigger',
    description: 'Run a CronJob now as a one-off Job (kubectl create job --from) and watch it start.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'name', type: 'string', required: true, description: 'CronJob name' },
    ],
  },
  {
    kind: 'cronjob_suspend',
    name: 'cronjob suspend',
    description: 'Pause a CronJob (maintenance window). Cancel resumes it.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'name', type: 'string', required: true, description: 'CronJob name' },
    ],
  },
  {
    kind: 'cronjob_resume',
    name: 'cronjob resume',
    description: 'Resume a suspended CronJob.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'name', type: 'string', required: true, description: 'CronJob name' },
    ],
  },
  {
    kind: 'cleanup_pods',
    name: 'cleanup pods',
    description: 'Delete terminal pods (Failed/Succeeded, incl. Evicted) in a namespace. Running pods are never touched.',
    fields: [{ name: 'namespace', type: 'namespace', required: true, default: 'shop' }],
  },
  {
    kind: 'node_taint',
    name: 'taint node',
    description: 'Add a taint to steer workloads away (verified). Cancel removes it.',
    fields: [
      { name: 'node', type: 'node', required: true },
      { name: 'key', type: 'string', required: true },
      { name: 'value', type: 'string' },
      { name: 'effect', type: 'enum', options: ['NoSchedule', 'PreferNoSchedule', 'NoExecute'], default: 'NoSchedule' },
    ],
  },
  {
    kind: 'node_untaint',
    name: 'untaint node',
    description: 'Remove a taint by key (verified).',
    fields: [
      { name: 'node', type: 'node', required: true },
      { name: 'key', type: 'string', required: true },
    ],
  },
  {
    kind: 'debug_bundle',
    name: 'debug bundle',
    description: 'Read-only triage snapshot: rollout state + container statuses (OOMKilled/CrashLoop) + recent events — one click instead of five kubectl commands.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'kind', type: 'enum', options: ['Deployment', 'StatefulSet', 'DaemonSet'], default: 'Deployment' },
      { name: 'name', type: 'workload', required: true },
    ],
  },
  {
    kind: 'pod_events',
    name: 'pod events',
    description: 'Read-only: the most recent events of a workload and its pods (scheduling failures, ImagePullBackOff, OOMKilled).',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'kind', type: 'enum', options: ['Deployment', 'StatefulSet', 'DaemonSet'], default: 'Deployment' },
      { name: 'name', type: 'workload', required: true },
    ],
  },
];

const EXAMPLE_SOURCE = `# Safe scale-up with automatic rollback.
# Full contract: args (dict) · step(name) · report(detail) · fail(msg)
# k8s.get/pods/scale/rollout_restart/delete_pod · wait_rollout/wait_ready · sleep(s)

ns = args["namespace"]
name = args["name"]
target = int(args["replicas"])

step("snapshot")
before = k8s.get(ns, "Deployment", name)["desired"]
report("current replicas: %d" % before)

step("scale to %d" % target)
k8s.scale(ns, "Deployment", name, target)
ok = wait_ready(ns, "Deployment", name, timeout=120)

if not ok:
    step("rollback")
    report("not ready in time - rolling back to %d" % before)
    k8s.scale(ns, "Deployment", name, before)
    wait_ready(ns, "Deployment", name, timeout=120)
    fail("scale to %d did not settle - rolled back to %d" % (target, before))

step("verify")
pods = [p for p in k8s.pods(ns) if p["ready"]]
report("settled at %d replicas" % target)
`;

// buildActionBody übersetzt (kind, Formularwerte) in den createAction-Body:
// Target-Auflösung (Node/Namespace/Pod/HPA/CronJob/Workload) + typed Params.
// Ein Ort für die ganze Zuordnung — neue Kinds hängen genau hier ein.
const NODE_KINDS = ['cordon', 'uncordon', 'drain', 'node_taint', 'node_untaint'];
const CRON_KINDS = ['cronjob_trigger', 'cronjob_suspend', 'cronjob_resume'];

function buildActionBody(kind: string, values: Record<string, string>) {
  let targetNamespace = values.namespace ?? '';
  let targetKind = values.kind ?? 'Deployment';
  let targetName = values.name ?? '';
  let params: Record<string, unknown> = {};

  if (NODE_KINDS.includes(kind)) {
    targetNamespace = '-';
    targetKind = 'Node';
    targetName = values.node ?? '';
  } else if (kind === 'cleanup_pods') {
    targetNamespace = '-';
    targetKind = 'Namespace';
    targetName = values.namespace ?? '';
  } else if (kind === 'delete_pod') {
    targetKind = 'Pod';
    targetName = values.pod ?? '';
  } else if (kind === 'hpa_set') {
    targetKind = 'HorizontalPodAutoscaler';
  } else if (CRON_KINDS.includes(kind)) {
    targetKind = 'CronJob';
  } else if (kind === 'rollout_pause' || kind === 'rollout_resume') {
    targetKind = 'Deployment';
  }

  if (kind === 'scale') params = { replicas: Number(values.replicas ?? 1) };
  else if (kind === 'set_image')
    params = { image: values.image ?? '', ...(values.container ? { container: values.container } : {}) };
  else if (kind === 'hpa_set')
    params = { minReplicas: Number(values.minReplicas ?? 1), maxReplicas: Number(values.maxReplicas ?? 1) };
  else if (kind === 'node_taint')
    params = { key: values.key ?? '', value: values.value ?? '', effect: values.effect ?? 'NoSchedule' };
  else if (kind === 'node_untaint') params = { key: values.key ?? '' };

  return { kind: kind as ActionKind, targetNamespace, targetKind, targetName, params };
}

/* ── Library-Katalog: Kategorien, Sicherheitsklassen, Icons (App-Store) ──── */

type Category = 'deploy' | 'scale' | 'batch' | 'pods' | 'node' | 'investigate';
type ActionClass = 'read' | 'reversible' | 'destructive';

const CATEGORY_ORDER: { key: Category; label: string }[] = [
  { key: 'investigate', label: 'Investigate' },
  { key: 'deploy', label: 'Deploy & Release' },
  { key: 'scale', label: 'Scaling' },
  { key: 'batch', label: 'Batch' },
  { key: 'pods', label: 'Pods & Housekeeping' },
  { key: 'node', label: 'Node ops' },
];

// Library-Tabs oben: Alle · je Kategorie · Custom (kürzere Labels für die Leiste).
const LIB_TABS: { key: string; label: string }[] = [
  { key: 'all', label: 'All' },
  { key: 'investigate', label: 'Investigate' },
  { key: 'deploy', label: 'Deploy' },
  { key: 'scale', label: 'Scaling' },
  { key: 'batch', label: 'Batch' },
  { key: 'pods', label: 'Pods' },
  { key: 'node', label: 'Node ops' },
  { key: 'custom', label: 'Custom' },
];

// Jede eingebaute Action trägt Kategorie + Sicherheitsklasse (die Klassen aus
// dem Actions-Katalog: read-only · auto-rollback · destructive).
const ACTION_META: Record<string, { category: Category; klass: ActionClass }> = {
  debug_bundle: { category: 'investigate', klass: 'read' },
  pod_events: { category: 'investigate', klass: 'read' },
  set_image: { category: 'deploy', klass: 'reversible' },
  rollout_restart: { category: 'deploy', klass: 'reversible' },
  rollout_pause: { category: 'deploy', klass: 'reversible' },
  rollout_resume: { category: 'deploy', klass: 'reversible' },
  rollout_undo: { category: 'deploy', klass: 'reversible' },
  scale: { category: 'scale', klass: 'reversible' },
  hpa_set: { category: 'scale', klass: 'reversible' },
  cronjob_trigger: { category: 'batch', klass: 'reversible' },
  cronjob_suspend: { category: 'batch', klass: 'reversible' },
  cronjob_resume: { category: 'batch', klass: 'reversible' },
  delete_pod: { category: 'pods', klass: 'reversible' },
  cleanup_pods: { category: 'pods', klass: 'destructive' },
  cordon: { category: 'node', klass: 'reversible' },
  uncordon: { category: 'node', klass: 'reversible' },
  drain: { category: 'node', klass: 'destructive' },
  node_taint: { category: 'node', klass: 'reversible' },
  node_untaint: { category: 'node', klass: 'reversible' },
};

const CLASS_LABEL: Record<ActionClass, string> = {
  read: 'read-only',
  reversible: 'auto-rollback',
  destructive: 'destructive',
};

// Monochrome Line-Icons (RETICLE: kein Farb-Spam — currentColor, 1.6px stroke).
function ActionIcon({ kind }: { kind: string }) {
  const glyphs: Record<string, React.ReactNode> = {
    set_image: (<><rect x="3.5" y="7" width="17" height="10" rx="1.5" /><path d="M12 21v-5M9.5 18l2.5-2.5L14.5 18" /></>),
    rollout_restart: (<><path d="M20 12a8 8 0 1 1-2.3-5.6" /><path d="M20 4v4h-4" /></>),
    rollout_pause: (<><rect x="7" y="5.5" width="3.4" height="13" rx="1" /><rect x="13.6" y="5.5" width="3.4" height="13" rx="1" /></>),
    rollout_resume: <path d="M8 5l11 7-11 7z" />,
    rollout_undo: (<><path d="M9 7L4 12l5 5" /><path d="M4 12h10a6 6 0 0 1 0 8h-3" /></>),
    scale: (<><path d="M8 4H4v4M16 20h4v-4M4 4l6 6M20 20l-6-6" /></>),
    hpa_set: (<><path d="M4 8h8M17 8h3M4 16h3M12 16h8" /><circle cx="14" cy="8" r="2" /><circle cx="9" cy="16" r="2" /></>),
    cronjob_trigger: (<><circle cx="12" cy="12" r="8" /><path d="M12 8v4l3 2" /></>),
    cronjob_suspend: (<><circle cx="12" cy="12" r="8" /><path d="M10 9.5v5M14 9.5v5" /></>),
    cronjob_resume: (<><circle cx="12" cy="12" r="8" /><path d="M10.5 8.5l5 3.5-5 3.5z" /></>),
    delete_pod: (<><path d="M5 7h14M10 7V5h4v2M6.5 7l1 12h9l1-12" /></>),
    cleanup_pods: (<><path d="M19 5l-9 9M6.5 12.5l5 5M4 20l3-1M8.5 16.5L4 21" /></>),
    cordon: (<><circle cx="12" cy="12" r="8" /><path d="M6.4 6.4l11.2 11.2" /></>),
    uncordon: (<><circle cx="12" cy="12" r="8" /><path d="M8.5 12l2.5 2.5L16 9" /></>),
    drain: <path d="M12 3s6.5 7.2 6.5 11.5a6.5 6.5 0 0 1-13 0C5.5 10.2 12 3 12 3z" />,
    node_taint: (<><path d="M4 4h7l9 9-7 7-9-9V4z" /><circle cx="8" cy="8" r="1.3" /></>),
    node_untaint: (<><path d="M4 4h7l9 9-7 7-9-9V4z" /><path d="M3.5 3.5l17 17" /></>),
    debug_bundle: (<><circle cx="10.5" cy="10.5" r="6" /><path d="M15 15l5 5" /></>),
    pod_events: <path d="M4 6h16M4 12h16M4 18h10" />,
  };
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      {glyphs[kind] ?? <rect x="4" y="4" width="16" height="16" rx="2" />}
    </svg>
  );
}

function matchAction(b: (typeof BUILTINS)[number], q: string): boolean {
  if (!q) return true;
  const s = q.toLowerCase();
  return (
    b.name.toLowerCase().includes(s) ||
    b.kind.toLowerCase().includes(s) ||
    b.description.toLowerCase().includes(s)
  );
}

type EditorState = {
  id: string | null; // null = neu
  name: string;
  description: string;
  params: ActionDefParam[];
  source: string;
  timeoutSeconds: number;
};

export default function ActionsPage() {
  const params = useParams<{ id: string }>();
  const clusterId = params.id;
  const { currentOrg } = useMe();
  const orgId = currentOrg?.id;

  const [defs, setDefs] = useState<ActionDefinition[] | null>(null);
  const [runs, setRuns] = useState<ClusterAction[] | null>(null);
  const [map, setMap] = useState<ServiceMap | null>(null);
  const { nodes: infraNodes } = useInfra(orgId, clusterId);
  const [editor, setEditor] = useState<EditorState | null>(null);
  const [runDialog, setRunDialog] = useState<
    | { type: 'builtin'; builtin: (typeof BUILTINS)[number] }
    | { type: 'script'; def: ActionDefinition }
    | null
  >(null);
  const [error, setError] = useState<string | null>(null);
  const [libQ, setLibQ] = useState('');
  const [libTab, setLibTab] = useState<string>('all');

  const loadDefs = useCallback(() => {
    if (!orgId) return;
    getActionDefinitions(orgId)
      .then((r) => setDefs(r.definitions))
      .catch(() => setDefs([]));
  }, [orgId]);

  useEffect(loadDefs, [loadDefs]);

  // Topologie für typed Pickers (workload/namespace) im Run-Dialog.
  useEffect(() => {
    if (!orgId) return;
    getServiceMap(orgId, clusterId).then(setMap).catch(() => {});
  }, [orgId, clusterId]);

  // Runs: SSE-getrieben (actions-Signal → refetch), Poll nur als Fallback.
  const pollRef = useRef<() => void>(() => {});
  const { live } = useClusterEvents(orgId, clusterId, {
    actions: () => pollRef.current(),
  });
  useEffect(() => {
    if (!orgId) return;
    let alive = true;
    const poll = () =>
      getActions(orgId, clusterId, '', '', 40)
        .then((r) => {
          if (alive) setRuns(r.actions);
        })
        .catch(() => {});
    pollRef.current = () => void poll();
    void poll();
    const t = setInterval(poll, live ? 20_000 : POLL_MS);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [orgId, clusterId, live]);

  const saveEditor = useCallback(async () => {
    if (!orgId || !editor) return;
    setError(null);
    const body = {
      name: editor.name,
      description: editor.description,
      params: editor.params.filter((p) => p.name.trim() !== ''),
      source: editor.source,
      timeoutSeconds: editor.timeoutSeconds,
    };
    try {
      if (editor.id) await updateActionDefinition(orgId, editor.id, body);
      else await createActionDefinition(orgId, body);
      setEditor(null);
      loadDefs();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'save failed');
    }
  }, [orgId, editor, loadDefs]);

  const running = useMemo(
    () => (runs ?? []).filter((a) => a.status === 'pending' || a.status === 'running').length,
    [runs],
  );

  // fork: eine eingebaute Action als editierbaren Starlark-Workflow öffnen —
  // die Source ist der KANONISCHE Code der Action, die Felder werden zu Params.
  const forkBuiltin = useCallback((b: (typeof BUILTINS)[number]) => {
    setEditor({
      id: null,
      name: b.kind.replace(/_/g, '-'),
      description: b.description,
      params: b.fields,
      source: BUILTIN_STARLARK[b.kind] ?? `# ${b.name}\n`,
      timeoutSeconds: 600,
    });
  }, []);

  return (
    <div className="flex h-[calc(100dvh-52px)] flex-col px-4 pt-4 sm:px-5">
      <PageHeader kicker="act / safe-actions" title="Actions">
        <div className="flex items-center gap-3 font-mono text-[11px] text-muted tnum">
          <span>{BUILTINS.length} built-in</span>
          <span>{defs?.length ?? 0} workflows</span>
          {running > 0 ? (
            <span className="flex items-center gap-1.5" style={{ color: 'var(--rp-ink-mid)' }}>
              <span
                className="rp-breath inline-block h-1.5 w-1.5 rounded-full"
                style={{ background: 'var(--rp-green)', color: 'var(--rp-green)' }}
              />
              {running} running
            </span>
          ) : null}
        </div>
      </PageHeader>

      <div className="mt-3 grid min-h-0 flex-1 grid-cols-1 gap-3 pb-3 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
        {/* ── Library ── */}
        <section className="min-h-0 overflow-y-auto pr-1">
          <div className="flex items-center justify-between">
            <span className="rp-micro !text-[10px]">library</span>
            <button
              type="button"
              onClick={() =>
                setEditor({
                  id: null,
                  name: '',
                  description: '',
                  params: [
                    { name: 'namespace', type: 'namespace', default: 'shop', required: true },
                    { name: 'name', type: 'workload', required: true },
                    { name: 'replicas', type: 'int', min: 0, max: 10, default: '2', required: true },
                  ],
                  source: EXAMPLE_SOURCE,
                  timeoutSeconds: 600,
                })
              }
              className="rp-focus h-8 rounded-skin-sm px-3 font-mono text-[11px] font-semibold transition-opacity hover:opacity-90"
              style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}
            >
              + New workflow
            </button>
          </div>

          {/* Tabs oben — Action-Typen + Custom */}
          <div className="mt-3 flex gap-1 overflow-x-auto pb-1">
            {LIB_TABS.map((t) => {
              const count =
                t.key === 'all'
                  ? BUILTINS.length
                  : t.key === 'custom'
                    ? (defs?.length ?? 0)
                    : BUILTINS.filter((b) => ACTION_META[b.kind]?.category === t.key).length;
              const active = libTab === t.key;
              return (
                <button
                  key={t.key}
                  type="button"
                  onClick={() => setLibTab(t.key)}
                  className={cn(
                    'rp-focus flex shrink-0 items-center gap-1.5 rounded-skin-sm px-2.5 py-1.5 font-mono text-[11px] transition-colors',
                    active ? 'bg-hover text-ink' : 'text-muted hover:text-ink',
                  )}
                  style={active ? { boxShadow: 'inset 0 0 0 1px var(--rp-line-strong)' } : undefined}
                >
                  {t.label}
                  <span className="text-[9.5px] text-faint tnum">{count}</span>
                </button>
              );
            })}
          </div>

          {libTab === 'custom' ? (
            defs === null ? (
              <div className="mt-3 flex items-center gap-2 text-muted">
                <Spinner /> <span className="font-mono text-[11px]">loading…</span>
              </div>
            ) : defs.length === 0 ? (
              <div
                className="mt-3 rounded-skin border border-dashed p-5 text-center font-mono text-[11px] leading-relaxed text-muted"
                style={{ borderColor: 'var(--rp-line-strong)' }}
              >
                No workflows yet — fork any built-in action or start from scratch. Hermetic
                Python, verified on the pod, auto-rolled-back. Hit “+ New workflow”.
              </div>
            ) : (
              <div className="mt-3 space-y-2">
                {defs.map((d) => (
                  <div
                    key={d.id}
                    className="rounded-skin border border-line bg-raised p-3"
                    style={{ boxShadow: 'var(--rp-rim)' }}
                  >
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-mono text-[12px] font-semibold text-ink">{d.name}</span>
                      {(d.params ?? []).map((p) => (
                        <span
                          key={p.name}
                          className="rounded-skin-chip bg-inset px-1.5 py-0.5 font-mono text-[9px] text-muted"
                        >
                          {p.name}
                        </span>
                      ))}
                      <span className="ml-auto flex items-center gap-1.5">
                        <button
                          type="button"
                          onClick={() =>
                            setEditor({
                              id: d.id,
                              name: d.name,
                              description: d.description,
                              params: d.params ?? [],
                              source: d.source,
                              timeoutSeconds: d.timeoutSeconds || 600,
                            })
                          }
                          className="rounded-skin-sm border border-line px-2 py-1 font-mono text-[10.5px] text-mid transition-colors hover:bg-hover hover:text-ink"
                        >
                          edit
                        </button>
                        <button
                          type="button"
                          onClick={() => setRunDialog({ type: 'script', def: d })}
                          className="rp-focus rounded-skin-sm border border-line px-2.5 py-1 font-mono text-[10.5px] text-ink transition-colors hover:bg-hover"
                        >
                          run →
                        </button>
                      </span>
                    </div>
                    {d.description ? (
                      <p className="mt-1 font-mono text-[10.5px] leading-relaxed text-muted">{d.description}</p>
                    ) : null}
                  </div>
                ))}
              </div>
            )
          ) : (
            <>
              <div className="mt-3">
                <input
                  value={libQ}
                  onChange={(e) => setLibQ(e.target.value)}
                  spellCheck={false}
                  placeholder="search actions…"
                  className="rp-focus h-9 w-full rounded-skin-sm border border-line bg-inset px-3 font-mono text-[12px] text-ink placeholder:text-faint"
                />
              </div>
              {(() => {
                const items = BUILTINS.filter(
                  (b) => (libTab === 'all' || ACTION_META[b.kind]?.category === libTab) && matchAction(b, libQ),
                );
                if (items.length === 0)
                  return <div className="mt-3 font-mono text-[11px] text-faint">no actions match.</div>;
                return (
                  <div className="mt-3 grid grid-cols-1 gap-2 lg:grid-cols-2 2xl:grid-cols-3">
                    {items.map((b) => {
                      const klass = ACTION_META[b.kind]?.klass ?? 'reversible';
                      return (
                        <div
                          key={b.kind}
                          className="group flex flex-col rounded-skin border border-line bg-raised p-3 text-left transition-colors hover:border-line-strong"
                          style={{ boxShadow: 'var(--rp-rim)' }}
                        >
                          <div className="flex items-center gap-2.5">
                            <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-skin-sm border border-line bg-inset text-mid transition-colors group-hover:text-ink">
                              <ActionIcon kind={b.kind} />
                            </span>
                            <div className="min-w-0">
                              <div className="truncate font-mono text-[12px] font-semibold text-ink">
                                {b.name}
                              </div>
                              <div
                                className="mt-0.5 font-mono text-[9px] uppercase tracking-[0.05em]"
                                style={{
                                  color:
                                    klass === 'destructive' ? 'var(--rp-ink-mid)' : 'var(--rp-ink-faint)',
                                }}
                              >
                                {klass === 'read' ? '◎ ' : klass === 'destructive' ? '△ ' : '↺ '}
                                {CLASS_LABEL[klass]}
                              </div>
                            </div>
                          </div>
                          <p className="mt-2 flex-1 font-mono text-[10.5px] leading-relaxed text-muted">
                            {b.description}
                          </p>
                          <div className="mt-2.5 flex items-center gap-2">
                            <button
                              type="button"
                              onClick={() => setRunDialog({ type: 'builtin', builtin: b })}
                              className="rp-focus rounded-skin-sm border border-line px-2.5 py-1 font-mono text-[11px] text-ink transition-colors hover:bg-hover"
                            >
                              run →
                            </button>
                            <button
                              type="button"
                              onClick={() => forkBuiltin(b)}
                              title="fork as an editable Starlark workflow — copy this code and build your own"
                              className="rp-focus rounded-skin-sm border border-line px-2 py-1 font-mono text-[11px] text-mid transition-colors hover:bg-hover hover:text-ink"
                            >
                              {'</>'} fork
                            </button>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                );
              })()}
            </>
          )}
        </section>

        {/* ── Runs ── */}
        <section
          className="flex min-h-0 flex-col overflow-hidden rounded-skin border border-line bg-raised"
          style={{ boxShadow: 'var(--rp-rim)' }}
        >
          <div className="flex shrink-0 items-center justify-between border-b border-line px-3 py-2">
            <span className="rp-micro !text-[10px]">runs · this cluster</span>
            <span className="font-mono text-[10px] text-muted tnum">{runs?.length ?? 0}</span>
          </div>
          <div className="min-h-0 flex-1 space-y-1.5 overflow-y-auto p-2">
            {runs === null ? (
              <div className="flex h-24 items-center justify-center gap-2 text-muted">
                <Spinner /> <span className="font-mono text-[11px]">loading…</span>
              </div>
            ) : runs.length === 0 ? (
              <div className="flex h-24 items-center justify-center font-mono text-[11px] text-faint">
                no runs yet
              </div>
            ) : (
              runs.map((a) => (
                <ActionRunCard
                  key={a.id}
                  action={a}
                  onCancel={(id) => {
                    if (orgId) void cancelAction(orgId, clusterId, id).catch(() => {});
                  }}
                />
              ))
            )}
          </div>
        </section>
      </div>

      {editor ? (
        <WorkflowEditor
          state={editor}
          error={error}
          onChange={setEditor}
          onSave={saveEditor}
          onDelete={
            editor.id && orgId
              ? async () => {
                  await deleteActionDefinition(orgId, editor.id!).catch(() => {});
                  setEditor(null);
                  loadDefs();
                }
              : undefined
          }
          onClose={() => {
            setEditor(null);
            setError(null);
          }}
        />
      ) : null}

      {runDialog && orgId ? (
        <RunDialog
          dialog={runDialog}
          map={map}
          nodeNames={(infraNodes ?? []).map((n) => n.name)}
          onClose={() => setRunDialog(null)}
          onRun={async (values) => {
            if (runDialog.type === 'script') {
              await runScriptAction(orgId, clusterId, runDialog.def.id, values);
            } else {
              await createAction(orgId, clusterId, buildActionBody(runDialog.builtin.kind, values));
            }
            setRunDialog(null);
          }}
        />
      ) : null}
    </div>
  );
}

/* ── Editor-Drawer ─────────────────────────────────────────────────────── */

function WorkflowEditor({
  state,
  error,
  onChange,
  onSave,
  onDelete,
  onClose,
}: {
  state: EditorState;
  error: string | null;
  onChange: (s: EditorState) => void;
  onSave: () => void;
  onDelete?: () => void;
  onClose: () => void;
}) {
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose();
    }
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);

  const set = (patch: Partial<EditorState>) => onChange({ ...state, ...patch });

  return (
    <div className="fixed inset-0 z-50" role="dialog" aria-modal="true" aria-label="Workflow editor">
      <button
        type="button"
        aria-label="Close"
        onClick={onClose}
        className="absolute inset-0 cursor-default"
        style={{ backgroundColor: 'var(--rp-scrim)' }}
      />
      <aside
        className="absolute inset-y-0 right-0 flex w-[min(760px,94vw)] flex-col border-l border-line bg-raised"
        style={{
          boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)',
          animation: 'rp-drawer-in var(--rp-dur-large) var(--rp-ease-enter)',
        }}
      >
        <header className="shrink-0 border-b border-line px-5 pb-3 pt-4">
          <div className="rp-micro !text-[10px]">workflow / starlark</div>
          <div className="rp-keyline mt-2 flex flex-wrap items-center justify-between gap-2 pb-3">
            <h2 className="font-display text-[20px] font-bold tracking-tightest text-ink">
              {state.id ? `Edit ${state.name}` : 'New workflow'}
            </h2>
            <div className="flex items-center gap-2">
              {onDelete ? (
                <button
                  type="button"
                  onClick={onDelete}
                  className="rounded-skin-sm border border-line px-2.5 py-1 font-mono text-[11px] transition-colors hover:bg-hover"
                  style={{ color: 'var(--rp-tone-red-fg)' }}
                >
                  delete
                </button>
              ) : null}
              <button
                type="button"
                onClick={onSave}
                className="rp-focus h-8 rounded-skin-sm px-3.5 font-mono text-[11.5px] font-semibold transition-opacity hover:opacity-90"
                style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}
              >
                Save
              </button>
              <button
                type="button"
                onClick={onClose}
                className="rounded-skin-sm border border-line px-3 py-1.5 font-mono text-[11.5px] text-mid transition-colors hover:bg-hover hover:text-ink"
              >
                Cancel
              </button>
            </div>
          </div>
        </header>

        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
          {error ? (
            <div
              className="mb-3 rounded-skin-sm px-3 py-2 font-mono text-[11px]"
              style={{ color: 'var(--rp-tone-red-fg)', background: 'var(--rp-tone-red-bg)' }}
            >
              {error}
            </div>
          ) : null}

          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label className="block">
              <span className="rp-micro !text-[10px]">name (kebab-case)</span>
              <input
                value={state.name}
                onChange={(e) => set({ name: e.target.value })}
                placeholder="safe-scale"
                className="rp-focus mt-1 h-9 w-full rounded-skin-sm border border-line bg-inset px-2.5 font-mono text-[12px] text-ink placeholder:text-faint"
              />
            </label>
            <label className="block">
              <span className="rp-micro !text-[10px]">description</span>
              <input
                value={state.description}
                onChange={(e) => set({ description: e.target.value })}
                placeholder="Scale up with verification and automatic rollback"
                className="rp-focus mt-1 h-9 w-full rounded-skin-sm border border-line bg-inset px-2.5 font-mono text-[12px] text-ink placeholder:text-faint"
              />
            </label>
          </div>

          {/* Params */}
          <div className="mt-4">
            <div className="flex items-center justify-between">
              <span className="rp-micro !text-[10px]">parameters — become `args` in the script</span>
              <button
                type="button"
                onClick={() => set({ params: [...state.params, { name: '', default: '' }] })}
                className="rounded-skin-chip border border-line px-1.5 py-0.5 font-mono text-[10px] text-muted transition-colors hover:text-ink"
              >
                + add
              </button>
            </div>
            <div className="mt-1.5 space-y-1.5">
              {state.params.map((p, i) => {
                const patch = (q: Partial<ActionDefParam>) => {
                  const params = [...state.params];
                  params[i] = { ...p, ...q };
                  set({ params });
                };
                return (
                  <div key={i} className="flex items-center gap-2">
                    <input
                      value={p.name}
                      onChange={(e) => patch({ name: e.target.value })}
                      placeholder="name"
                      className="rp-focus h-8 w-32 rounded-skin-sm border border-line bg-inset px-2 font-mono text-[11px] text-ink placeholder:text-faint"
                    />
                    <select
                      value={p.type ?? 'string'}
                      onChange={(e) => patch({ type: e.target.value as ActionDefParam['type'] })}
                      className="rp-focus h-8 rounded-skin-sm border border-line bg-inset px-1.5 font-mono text-[11px] text-ink"
                    >
                      {['string', 'int', 'bool', 'enum', 'namespace', 'workload'].map((t) => (
                        <option key={t} value={t}>{t}</option>
                      ))}
                    </select>
                    {p.type === 'int' ? (
                      <>
                        <input
                          value={p.min ?? ''}
                          onChange={(e) => patch({ min: e.target.value === '' ? undefined : Number(e.target.value) })}
                          placeholder="min"
                          className="rp-focus h-8 w-14 rounded-skin-sm border border-line bg-inset px-2 font-mono text-[11px] text-ink placeholder:text-faint tnum"
                        />
                        <input
                          value={p.max ?? ''}
                          onChange={(e) => patch({ max: e.target.value === '' ? undefined : Number(e.target.value) })}
                          placeholder="max"
                          className="rp-focus h-8 w-14 rounded-skin-sm border border-line bg-inset px-2 font-mono text-[11px] text-ink placeholder:text-faint tnum"
                        />
                      </>
                    ) : null}
                    {p.type === 'enum' ? (
                      <input
                        value={(p.options ?? []).join(',')}
                        onChange={(e) => patch({ options: e.target.value.split(',').map((s) => s.trim()).filter(Boolean) })}
                        placeholder="a,b,c"
                        className="rp-focus h-8 w-36 rounded-skin-sm border border-line bg-inset px-2 font-mono text-[11px] text-ink placeholder:text-faint"
                      />
                    ) : null}
                    <input
                      value={p.default ?? ''}
                      onChange={(e) => patch({ default: e.target.value })}
                      placeholder="default"
                      className="rp-focus h-8 min-w-0 flex-1 rounded-skin-sm border border-line bg-inset px-2 font-mono text-[11px] text-ink placeholder:text-faint"
                    />
                    <label className="flex shrink-0 items-center gap-1 font-mono text-[10px] text-muted" title="required">
                      <input
                        type="checkbox"
                        checked={p.required ?? false}
                        onChange={(e) => patch({ required: e.target.checked })}
                        className="rp-focus h-3.5 w-3.5"
                      />
                      req
                    </label>
                    <button
                      type="button"
                      onClick={() => set({ params: state.params.filter((_, j) => j !== i) })}
                      className="h-8 w-8 shrink-0 rounded-skin-sm border border-line font-mono text-[11px] text-muted transition-colors hover:text-ink"
                      aria-label="remove parameter"
                    >
                      ✕
                    </button>
                  </div>
                );
              })}
            </div>

            {/* Timeout — der Ablauf ist zeitbegrenzt; danach Rollback */}
            <label className="mt-3 flex items-center gap-2">
              <span className="rp-micro !text-[10px]">timeout</span>
              <input
                value={state.timeoutSeconds}
                onChange={(e) => set({ timeoutSeconds: Number(e.target.value) || 0 })}
                className="rp-focus h-8 w-20 rounded-skin-sm border border-line bg-inset px-2 font-mono text-[11px] text-ink tnum"
              />
              <span className="font-mono text-[10px] text-faint">seconds (30–1800) · on timeout the engine rolls back automatically</span>
            </label>
          </div>

          {/* Source */}
          <div className="mt-4">
            <div className="flex items-baseline justify-between">
              <span className="rp-micro !text-[10px]">source — hermetic python (starlark)</span>
              <span className="font-mono text-[9.5px] text-faint">
                step() · report() · fail() · k8s.* · wait_rollout() · wait_ready() · sleep()
              </span>
            </div>
            <div className="mt-1.5">
              <StarlarkEditor
                value={state.source}
                onChange={(v) => set({ source: v })}
                params={state.params}
              />
            </div>
            <p className="mt-1.5 font-mono text-[9.5px] leading-relaxed text-faint">
              Verified on save: the control-plane compiles the script (syntax + unknown
              names) — broken workflows cannot be stored. Ctrl+Space for completion.
            </p>
          </div>
        </div>
      </aside>
    </div>
  );
}

/* ── Run-Dialog ────────────────────────────────────────────────────────── */

function RunDialog({
  dialog,
  map,
  nodeNames,
  onClose,
  onRun,
}: {
  dialog:
    | { type: 'builtin'; builtin: (typeof BUILTINS)[number] }
    | { type: 'script'; def: ActionDefinition };
  map: ServiceMap | null;
  nodeNames: string[];
  onClose: () => void;
  onRun: (values: Record<string, string>) => Promise<void>;
}) {
  const fields: ActionDefParam[] =
    dialog.type === 'script' ? (dialog.def.params ?? []) : dialog.builtin.fields;
  const [values, setValues] = useState<Record<string, string>>(
    Object.fromEntries(fields.map((f) => [f.name, f.default ?? ''])),
  );
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose();
    }
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);

  const title = dialog.type === 'script' ? dialog.def.name : dialog.builtin.name;
  const klass = dialog.type === 'builtin' ? (ACTION_META[dialog.builtin.kind]?.klass ?? 'reversible') : null;
  const corner = 'pointer-events-none absolute h-3.5 w-3.5';
  const cs = { borderColor: 'var(--rp-line-strong)' };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-3" role="dialog" aria-modal="true">
      <button
        type="button"
        aria-label="Close"
        onClick={onClose}
        className="absolute inset-0 cursor-default"
        style={{ backgroundColor: 'var(--rp-scrim)' }}
      />
      <div
        className="relative w-[min(410px,100%)] rounded-skin border bg-raised"
        style={{ borderColor: 'var(--rp-line-strong)', boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)', animation: 'reveal-up var(--rp-dur-med) var(--rp-ease-enter)' }}
      >
        {/* Passer-Eckwinkel */}
        <span className={`${corner} -left-1.5 -top-1.5 border-l border-t`} style={cs} aria-hidden />
        <span className={`${corner} -right-1.5 -top-1.5 border-r border-t`} style={cs} aria-hidden />
        <span className={`${corner} -bottom-1.5 -left-1.5 border-b border-l`} style={cs} aria-hidden />
        <span className={`${corner} -bottom-1.5 -right-1.5 border-b border-r`} style={cs} aria-hidden />

        <div className="border-b border-line px-4 py-3">
          <div className="rp-micro !text-[9.5px] text-faint">act / run → cluster</div>
          <div className="mt-1 flex flex-wrap items-baseline gap-x-2 gap-y-1">
            <span className="font-display text-[17px] font-bold tracking-tightest text-ink">{title}</span>
            {klass ? (
              <span
                className="font-mono text-[9px] uppercase tracking-[0.05em]"
                style={{ color: klass === 'destructive' ? 'var(--rp-node-crit)' : 'var(--rp-ink-faint)' }}
              >
                {klass === 'read' ? '◎ ' : klass === 'destructive' ? '△ ' : '↺ '}
                {CLASS_LABEL[klass]}
              </span>
            ) : (
              <span className="rounded-skin-chip bg-inset px-1 py-px font-mono text-[9px] uppercase tracking-[0.05em] text-muted">
                workflow
              </span>
            )}
          </div>
        </div>
        <div className="max-h-[58vh] space-y-2.5 overflow-y-auto px-4 py-3.5">
          {fields.map((f) => (
            <TypedField
              key={f.name}
              spec={f}
              value={values[f.name] ?? ''}
              map={map}
              nodeNames={nodeNames}
              namespace={values['namespace'] ?? ''}
              onChange={(v) => setValues((prev) => ({ ...prev, [f.name]: v }))}
            />
          ))}
          {err ? (
            <div className="rounded-skin-sm bg-tone-red-bg px-2.5 py-1.5 font-mono text-[11px] leading-snug" style={{ color: 'var(--rp-tone-red-fg)' }}>
              {err}
            </div>
          ) : null}
        </div>
        <div className="flex items-center gap-2 border-t border-line px-4 py-3">
          <button
            type="button"
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              setErr(null);
              try {
                await onRun(values);
              } catch (e) {
                setErr(e instanceof Error ? e.message : 'dispatch failed');
                setBusy(false);
                return;
              }
              setBusy(false);
            }}
            className="rp-focus flex h-9 flex-1 items-center justify-between rounded-skin-sm px-3.5 font-mono text-[12px] font-semibold transition-opacity hover:opacity-90"
            style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)', opacity: busy ? 0.55 : 1 }}
          >
            <span>{busy ? 'dispatching…' : 'Execute'}</span>
            {!busy ? <span aria-hidden>→</span> : null}
          </button>
          <button
            type="button"
            onClick={onClose}
            className="h-9 rounded-skin-sm border border-line px-3.5 font-mono text-[12px] text-mid transition-colors hover:bg-hover hover:text-ink"
          >
            Cancel
          </button>
        </div>
      </div>
    </div>
  );
}

/* ── TypedField: der richtige Control je Parametertyp ─────────────────── */

function TypedField({
  spec,
  value,
  map,
  nodeNames = [],
  namespace,
  onChange,
}: {
  spec: ActionDefParam;
  value: string;
  map: ServiceMap | null;
  nodeNames?: string[];
  /** aktueller namespace-Wert — filtert die Workload-Vorschläge. */
  namespace: string;
  onChange: (v: string) => void;
}) {
  const label = spec.label || spec.name;
  const base =
    'rp-focus mt-1 h-9 w-full rounded-skin-sm border border-line bg-inset px-2.5 font-mono text-[12px] text-ink';

  let control: React.ReactNode;
  switch (spec.type) {
    case 'int':
      control = (
        <div className="mt-1 flex items-center gap-2">
          <input
            type="number"
            value={value}
            min={spec.min}
            max={spec.max}
            onChange={(e) => onChange(e.target.value)}
            className="rp-focus h-9 w-28 rounded-skin-sm border border-line bg-inset px-2.5 font-mono text-[12px] text-ink tnum"
          />
          {spec.min !== undefined || spec.max !== undefined ? (
            <span className="font-mono text-[10px] text-faint tnum">
              range {spec.min ?? 0}–{spec.max ?? '∞'}
            </span>
          ) : null}
        </div>
      );
      break;
    case 'bool':
      control = (
        <select value={value || 'false'} onChange={(e) => onChange(e.target.value)} className={base}>
          <option value="true">true</option>
          <option value="false">false</option>
        </select>
      );
      break;
    case 'enum':
      control = (
        <select value={value} onChange={(e) => onChange(e.target.value)} className={base}>
          {(spec.options ?? []).map((o) => (
            <option key={o} value={o}>{o}</option>
          ))}
        </select>
      );
      break;
    case 'namespace': {
      const namespaces = map?.namespaces ?? [];
      control = (
        <select value={value} onChange={(e) => onChange(e.target.value)} className={base}>
          {value === '' ? <option value="">— select —</option> : null}
          {namespaces.map((n) => (
            <option key={n} value={n}>{n}</option>
          ))}
        </select>
      );
      break;
    }
    case 'node':
      control = (
        <select value={value} onChange={(e) => onChange(e.target.value)} className={base}>
          {value === '' ? <option value="">— select —</option> : null}
          {nodeNames.map((n) => (
            <option key={n} value={n}>{n}</option>
          ))}
        </select>
      );
      break;
    case 'workload': {
      const workloads = (map?.nodes ?? []).filter((n) => !namespace || n.namespace === namespace);
      control = (
        <select value={value} onChange={(e) => onChange(e.target.value)} className={base}>
          {value === '' ? <option value="">— select —</option> : null}
          {workloads.map((n) => (
            <option key={n.id} value={n.name}>
              {n.name} ({n.kind}, {n.podsReady}/{n.podsTotal})
            </option>
          ))}
        </select>
      );
      break;
    }
    default:
      control = (
        <input value={value} onChange={(e) => onChange(e.target.value)} className={base} />
      );
  }

  return (
    <label className="block">
      <span className="rp-micro !text-[10px]">
        {label}
        {spec.required ? <span style={{ color: 'var(--rp-ink-muted)' }}> *</span> : null}
      </span>
      {control}
      {spec.description ? (
        <span className="mt-0.5 block font-mono text-[9.5px] text-faint">{spec.description}</span>
      ) : null}
    </label>
  );
}
