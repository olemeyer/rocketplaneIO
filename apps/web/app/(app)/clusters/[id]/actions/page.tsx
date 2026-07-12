'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useParams } from 'next/navigation';
import { cn } from '@/lib/cn';
import { Spinner } from '@/components/ui';
import { useMe } from '@/components/app/me-context';
import { PageHeader } from '@/components/app/page-header';
import { useClusterEvents } from '@/lib/hooks/use-cluster-events';
import { useInfra } from '@/lib/hooks/use-infra';
import { GuardrailsMenu } from '@/components/app/copilot';
import { StarlarkEditor } from '@/components/actions/starlark-editor';
import {
  cancelAction,
  checkWorkflow,
  createAction,
  createActionDefinition,
  deleteActionDefinition,
  getActionDefinitions,
  getActions,
  getInventory,
  getServiceMap,
  runScriptAction,
  updateActionDefinition,
  type InventoryKind,
} from '@/lib/api/controlplane';
import type { ActionDefParam, ActionDefinition, ActionKind, ClusterAction, ServiceMap } from '@/lib/api/types';
import { BUILTIN_ACTIONS, type BuiltinMeta } from '@/lib/builtin-actions.generated';
import { parseFrontmatter, lintWorkflow, hasBlockingError, type Diag, type WorkflowMeta } from '@/lib/workflow-frontmatter';
import { LEVEL_META, RISK_LEVELS, levelColor, type RiskLevel } from '@/lib/approval';
import Link from 'next/link';

// Actions — der AKT-Bereich: links die LIBRARY (eingebaute Handgriffe +
// org-weite Custom-WORKFLOWS in Starlark), rechts die LIVE-Runs des Clusters.
// Workflows sind vollwertige Problemlösungs-Pipelines: hermetisches Python
// (kein I/O, garantierte Terminierung), step()/report() speisen dieselbe
// Timeline wie die eingebauten Pipelines, wait_* verifiziert auf Pod-Ebene.

const POLL_MS = 2500;

// BuiltinField erweitert ActionDefParam um Client-only-Picker: resourceKind
// bindet ein Feld an das Cluster-Inventar (ConfigMap-/Secret-/PDB-Namen …),
// multiline rendert eine Textarea (Patches, Kommandos, Daten).
type BuiltinField = ActionDefParam & { resourceKind?: string; multiline?: boolean };
type Builtin = { kind: string; name: string; description: string; fields: BuiltinField[] };

// Eingebaute Pipelines — dieselben, die das Workload-Panel anbietet.
const BUILTINS: Builtin[] = [
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
      { name: 'name', type: 'string', required: true, resourceKind: 'HorizontalPodAutoscaler', description: 'HPA name' },
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
      { name: 'name', type: 'string', required: true, resourceKind: 'CronJob', description: 'CronJob name' },
    ],
  },
  {
    kind: 'cronjob_suspend',
    name: 'cronjob suspend',
    description: 'Pause a CronJob (maintenance window). Cancel resumes it.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'name', type: 'string', required: true, resourceKind: 'CronJob', description: 'CronJob name' },
    ],
  },
  {
    kind: 'cronjob_resume',
    name: 'cronjob resume',
    description: 'Resume a suspended CronJob.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'name', type: 'string', required: true, resourceKind: 'CronJob', description: 'CronJob name' },
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
  {
    kind: 'set_resources',
    name: 'set resources',
    description: 'Set CPU/memory requests & limits on a container and run the verified rollout — the OOMKilled / CPU-throttle fix. Cancel restores the previous resources.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'kind', type: 'enum', options: ['Deployment', 'StatefulSet', 'DaemonSet'], default: 'Deployment' },
      { name: 'name', type: 'workload', required: true },
      { name: 'container', type: 'string', description: 'blank = the sole container' },
      { name: 'requestsCpu', type: 'string', description: 'e.g. 100m' },
      { name: 'requestsMemory', type: 'string', description: 'e.g. 128Mi' },
      { name: 'limitsCpu', type: 'string', description: 'e.g. 500m' },
      { name: 'limitsMemory', type: 'string', description: 'e.g. 256Mi' },
    ],
  },
  {
    kind: 'set_env',
    name: 'set env var',
    description: 'Set or unset a plaintext env var on a container and roll out — log level, feature flag, bad config. Cancel restores the previous value.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'kind', type: 'enum', options: ['Deployment', 'StatefulSet', 'DaemonSet'], default: 'Deployment' },
      { name: 'name', type: 'workload', required: true },
      { name: 'container', type: 'string', description: 'blank = the sole container' },
      { name: 'envName', type: 'string', required: true, description: 'env var name, e.g. LOG_LEVEL' },
      { name: 'value', type: 'string', description: 'value (ignored when remove=true)' },
      { name: 'remove', type: 'bool', description: 'unset the var instead' },
    ],
  },
  {
    kind: 'rollout_history',
    name: 'rollout history',
    description: 'Read-only: the revision history of a Deployment (revision, image, change-cause, age) — pick a target for rollout to revision.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'name', type: 'workload', required: true },
      { name: 'limit', type: 'int', min: 1, max: 20, default: '10' },
    ],
  },
  {
    kind: 'rollout_to_revision',
    name: 'rollout to revision',
    description: 'Roll a Deployment to a specific historical revision (verified rollout). Cancel restores the current template.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'name', type: 'workload', required: true },
      { name: 'revision', type: 'int', min: 1, required: true, description: 'from rollout history' },
    ],
  },
  {
    kind: 'statefulset_partition',
    name: 'staged canary (partition)',
    description: 'Set a StatefulSet RollingUpdate partition — only ordinals ≥ partition update on the next change. Cancel restores the prior partition.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'name', type: 'workload', required: true },
      { name: 'partition', type: 'int', min: 0, required: true },
    ],
  },
  {
    kind: 'hpa_toggle',
    name: 'freeze / unfreeze HPA',
    description: 'Freeze an HPA (pin min=max at the current size) so it stops fighting a manual fix — or unfreeze to restore its bounds.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'name', type: 'workload', required: true, description: 'HPA name' },
      { name: 'enabled', type: 'bool', description: 'on = unfreeze (restore), off = freeze' },
    ],
  },
  {
    kind: 'patch_configmap',
    name: 'patch configmap',
    description: 'Set or remove a single key in a ConfigMap. NOTE: running pods pick it up only after a rollout restart. Cancel restores the prior value.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'name', type: 'string', required: true, resourceKind: 'ConfigMap', description: 'configmap name' },
      { name: 'key', type: 'string', required: true },
      { name: 'value', type: 'string', description: 'ignored when remove=true' },
      { name: 'remove', type: 'bool' },
    ],
  },
  {
    kind: 'annotate',
    name: 'annotate object',
    description: 'Set or remove a single metadata annotation on almost any object (pause an operator, tag for triage). Cancel restores the prior value.',
    fields: [
      { name: 'kind', type: 'enum', options: ['Deployment', 'StatefulSet', 'DaemonSet', 'Pod', 'ConfigMap', 'Service', 'PersistentVolumeClaim', 'CronJob', 'HorizontalPodAutoscaler', 'Namespace', 'Node'], default: 'Deployment' },
      { name: 'namespace', type: 'namespace', default: 'shop', description: 'blank for Node/Namespace' },
      { name: 'name', type: 'string', required: true, description: 'object name' },
      { name: 'key', type: 'string', required: true },
      { name: 'value', type: 'string', description: 'ignored when remove=true' },
      { name: 'remove', type: 'bool' },
    ],
  },
  {
    kind: 'set_label',
    name: 'set label',
    description: 'Set or remove a label on a Node or Namespace — steer scheduling / namespace admission. Cancel restores the prior value.',
    fields: [
      { name: 'kind', type: 'enum', options: ['Node', 'Namespace'], default: 'Node' },
      { name: 'name', type: 'string', required: true, description: 'node or namespace name' },
      { name: 'key', type: 'string', required: true },
      { name: 'value', type: 'string', description: 'ignored when remove=true' },
      { name: 'remove', type: 'bool' },
    ],
  },
  {
    kind: 'evict_pod',
    name: 'evict pod',
    description: 'The safe recreate: graceful, PDB-aware Eviction API (never force), wait for a ready replacement.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'pod', type: 'string', required: true, description: 'exact pod name' },
      { name: 'gracePeriodSeconds', type: 'int', min: 0, max: 300, description: 'optional' },
    ],
  },
  {
    kind: 'cleanup_jobs',
    name: 'cleanup jobs',
    description: 'Delete finished (Complete/Failed) Jobs and their pods in a namespace — active jobs untouched.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'olderThanHours', type: 'int', min: 0, description: '0 = all matching' },
    ],
  },
  {
    kind: 'drain_preview',
    name: 'drain preview',
    description: 'Read-only blast radius before a drain: evictable pods, per-workload loss, blocking PDBs.',
    fields: [{ name: 'node', type: 'node', required: true }],
  },
  // ── Katalog Batch 2 — Diagnose ──────────────────────────────────────────
  {
    kind: 'get_resource',
    name: 'get resource (yaml)',
    description: 'Read the FULL live spec of any object as YAML (kubectl get -o yaml, noise stripped). ConfigMaps show their full data; Secrets only keys + hashes.',
    fields: [
      { name: 'kind', type: 'enum', options: ['ConfigMap', 'Deployment', 'StatefulSet', 'DaemonSet', 'Pod', 'Secret', 'Service', 'Ingress', 'NetworkPolicy', 'PodDisruptionBudget', 'HorizontalPodAutoscaler', 'CronJob', 'Job', 'PersistentVolumeClaim', 'Node', 'Namespace', 'ServiceAccount', 'ResourceQuota', 'LimitRange'], default: 'ConfigMap', required: true },
      { name: 'namespace', type: 'namespace', default: 'shop', description: 'blank for Node/Namespace' },
      { name: 'name', type: 'string', required: true, description: 'object name' },
    ],
  },
  {
    kind: 'describe_resource',
    name: 'describe resource',
    description: 'kubectl-describe-like view: status, conditions and the recent events of one object — the current state and WHY.',
    fields: [
      { name: 'kind', type: 'enum', options: ['Deployment', 'StatefulSet', 'DaemonSet', 'Pod', 'Service', 'Ingress', 'PodDisruptionBudget', 'HorizontalPodAutoscaler', 'CronJob', 'Job', 'PersistentVolumeClaim', 'Node', 'Namespace'], default: 'Deployment', required: true },
      { name: 'namespace', type: 'namespace', default: 'shop', description: 'blank for Node/Namespace' },
      { name: 'name', type: 'string', required: true },
    ],
  },
  {
    kind: 'get_secret',
    name: 'inspect secret',
    description: 'Read-only: keys, value lengths and sha256 hashes of a Secret — compare hashes to spot changed values. Plaintext never leaves the cluster.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'name', type: 'string', required: true, resourceKind: 'Secret', description: 'secret name' },
    ],
  },
  {
    kind: 'helm_releases',
    name: 'helm releases',
    description: 'Read-only: every Helm release (from sh.helm.release.v1 secrets) with revision and status — see what Helm manages.',
    fields: [{ name: 'namespace', type: 'namespace', description: 'blank = all namespaces' }],
  },
  {
    kind: 'exec_readonly',
    name: 'exec (read-only)',
    description: 'Run ONE whitelisted diagnostic command in a container (cat, ls, head, tail, df, du, stat, wc, env, id, uname, find — argv, no shell). THE way to read in-container file logs.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'pod', type: 'string', required: true, description: 'exact pod name' },
      { name: 'container', type: 'string', description: 'blank = first container' },
      { name: 'command', type: 'string', required: true, multiline: true, description: 'one argument per line, e.g.\ncat\n/var/log/app/error.log' },
      { name: 'retry', type: 'bool', description: 'keep retrying to catch a CrashLoop pod\u2019s brief run window' },
    ],
  },
  {
    kind: 'net_probe',
    name: 'net probe (reachability / tls)',
    description: 'Probe from inside the cluster (the agent runs the check): http status+latency, tcp port open, dns resolution, or a TLS handshake with days-until-cert-expiry. The fast answer to “is it reachable / is the cert expiring?”.',
    fields: [
      { name: 'mode', type: 'enum', options: ['http', 'tcp', 'dns', 'tls'], default: 'http', required: true },
      { name: 'target', type: 'string', required: true, description: 'http: http(s)://svc.ns:port/path · tcp/tls: host:port · dns: a hostname' },
      { name: 'method', type: 'enum', options: ['GET', 'HEAD'], default: 'GET', description: 'http mode only' },
    ],
  },
  {
    kind: 'pod_logs',
    name: 'pod logs (kubelet)',
    description: 'kubectl logs straight from the kubelet — including the PREVIOUS crashed container (the way to read a crash reason). Independent of the log pipeline.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'pod', type: 'string', required: true, description: 'exact pod name' },
      { name: 'container', type: 'string', description: 'blank = first container' },
      { name: 'previous', type: 'bool', description: 'read the previous (crashed) container' },
      { name: 'tailLines', type: 'int', min: 1, max: 500, default: '200' },
    ],
  },
  {
    kind: 'list_events',
    name: 'namespace events',
    description: 'ALL recent events of a namespace, newest first — “what happened here around 17:30?”. Optionally warnings only.',
    fields: [
      { name: 'namespace', type: 'namespace', description: 'blank = all namespaces' },
      { name: 'warningsOnly', type: 'bool' },
      { name: 'limit', type: 'int', min: 1, max: 100, default: '40' },
    ],
  },
  {
    kind: 'run_debug_pod',
    name: 'run debug pod',
    description: 'Ephemeral probe pod: image + argv, optional PVC mounted read-only at /pvc, optional node pinning. Logs are the result, the pod is always cleaned up. Image bisecting, in-cluster HTTP/DNS probes, PVC inspection.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'image', type: 'string', required: true, description: 'e.g. curlimages/curl or clickhouse/clickhouse-server:24.12.5.81' },
      { name: 'command', type: 'string', required: true, multiline: true, description: 'one argument per line, e.g.\ncurl\n-sS\nhttp://payments:8080/healthz' },
      { name: 'pvcName', type: 'string', description: 'PVC to mount read-only at /pvc (optional)' },
      { name: 'nodeName', type: 'node', description: 'pin to a node (optional)' },
    ],
  },
  // ── Katalog Batch 2 — Config & Secrets ──────────────────────────────────
  {
    kind: 'patch_secret',
    name: 'patch secret',
    description: 'Set or remove a single key in a Secret (value plain or base64). Cancel restores the prior value; consumers need a rollout restart.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'name', type: 'string', required: true, resourceKind: 'Secret', description: 'secret name' },
      { name: 'key', type: 'string', required: true },
      { name: 'value', type: 'string', description: 'plain or base64 — ignored when remove=true' },
      { name: 'remove', type: 'bool' },
    ],
  },
  {
    kind: 'create_configmap',
    name: 'create configmap',
    description: 'Create a new ConfigMap from key=value lines. Cancel deletes it again; Revert stays available on the run.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'name', type: 'string', required: true, description: 'new configmap name' },
      { name: 'data', type: 'string', required: true, multiline: true, description: 'one key=value per line' },
    ],
  },
  {
    kind: 'delete_configmap',
    name: 'delete configmap',
    description: 'Delete a ConfigMap — the before-state is kept as a snapshot, so the run offers a full restore.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'name', type: 'string', required: true, resourceKind: 'ConfigMap', description: 'configmap name' },
    ],
  },
  // ── Katalog Batch 2 — Netzwerk & Policies ───────────────────────────────
  {
    kind: 'pdb_set',
    name: 'set pdb bounds',
    description: 'Set minAvailable OR maxUnavailable (int or percentage) on a PodDisruptionBudget — unblock a stuck drain honestly. Cancel restores the prior bounds.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'name', type: 'string', required: true, resourceKind: 'PodDisruptionBudget', description: 'PDB name' },
      { name: 'mode', type: 'enum', options: ['minAvailable', 'maxUnavailable'], default: 'minAvailable', required: true },
      { name: 'value', type: 'string', required: true, description: 'e.g. 1 or 50%' },
    ],
  },
  {
    kind: 'patch_resource',
    name: 'patch resource (merge)',
    description: 'Generic JSON merge patch on a Service, Ingress, NetworkPolicy or PDB. The before-object is snapshotted and fully restorable.',
    fields: [
      { name: 'kind', type: 'enum', options: ['Service', 'Ingress', 'NetworkPolicy', 'PodDisruptionBudget'], default: 'Service', required: true },
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'name', type: 'string', required: true, description: 'object name' },
      { name: 'patch', type: 'string', required: true, multiline: true, description: 'JSON merge patch, e.g.\n{"spec":{"sessionAffinity":"ClientIP"}}' },
    ],
  },
  // ── Katalog Batch 2 — Storage & Batch ───────────────────────────────────
  {
    kind: 'pvc_expand',
    name: 'expand pvc',
    description: 'Grow a PersistentVolumeClaim (shrinking is impossible in Kubernetes — the agent enforces grow-only). Storage class must support expansion.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'name', type: 'string', required: true, resourceKind: 'PersistentVolumeClaim', description: 'PVC name' },
      { name: 'size', type: 'string', required: true, description: 'new size, e.g. 20Gi' },
    ],
  },
  {
    kind: 'delete_job',
    name: 'delete job',
    description: 'Delete one Job and its pods (background propagation) — clear a stuck or failed run so the next schedule can fire.',
    fields: [
      { name: 'namespace', type: 'namespace', required: true, default: 'shop' },
      { name: 'name', type: 'string', required: true, resourceKind: 'Job', description: 'job name' },
    ],
  },
];

const EXAMPLE_SOURCE = `# @name safe-scale
# @summary Scale a Deployment and verify readiness; auto-rollback on failure.
# @risk medium
# @reversible snapshot
# @targets Deployment
# @param namespace namespace required
# @param name workload required
# @param replicas int 0 50 required
#
# Metadata above is co-evaluated (name/risk/reversibility/params come from it).
# Surface: step · report · fail · sleep · snapshot · json · args · net_probe
#   k8s.patch/set_field/set_fields/scale/patch_configmap/create/delete/apply
#   k8s.get/list/status/pods/events · k8s.exec/logs/evict/node_pods/pod_status
#   wait_ready/wait_rollout
ns = args["namespace"]
name = args["name"]
target = int(args["replicas"])

step("snapshot")
snapshot(ns, "Deployment", name)
step("scale to %d" % target)
k8s.scale(ns, "Deployment", name, target)

step("verify")
if not wait_ready(ns, "Deployment", name, timeout=120):
    fail("did not settle in time — the snapshot rolls every change back automatically")
report("settled at %d replicas" % target)
`;

// buildActionBody übersetzt (kind, Formularwerte) in den createAction-Body:
// Target-Auflösung (Node/Namespace/Pod/HPA/CronJob/Workload) + typed Params.
// Ein Ort für die ganze Zuordnung — neue Kinds hängen genau hier ein.
const NODE_KINDS = ['cordon', 'uncordon', 'drain', 'node_taint', 'node_untaint', 'drain_preview'];
const isTrue = (v?: string) => v === 'true' || v === 'on' || v === '1';
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
  } else if (kind === 'cleanup_pods' || kind === 'cleanup_jobs') {
    targetNamespace = '-';
    targetKind = 'Namespace';
    targetName = values.namespace ?? '';
  } else if (kind === 'delete_pod' || kind === 'evict_pod') {
    targetKind = 'Pod';
    targetName = values.pod ?? '';
  } else if (kind === 'hpa_set' || kind === 'hpa_toggle') {
    targetKind = 'HorizontalPodAutoscaler';
  } else if (kind === 'patch_configmap') {
    targetKind = 'ConfigMap';
  } else if (kind === 'statefulset_partition') {
    targetKind = 'StatefulSet';
  } else if (kind === 'rollout_to_revision' || kind === 'rollout_pause' || kind === 'rollout_resume') {
    targetKind = 'Deployment';
  } else if (kind === 'set_label') {
    targetKind = values.kind ?? 'Node';
    targetNamespace = '-';
  } else if (kind === 'annotate') {
    targetKind = values.kind ?? 'Deployment';
    if (targetKind === 'Node' || targetKind === 'Namespace') targetNamespace = '-';
  } else if (CRON_KINDS.includes(kind)) {
    targetKind = 'CronJob';
  } else if (kind === 'get_resource' || kind === 'describe_resource') {
    targetKind = values.kind ?? 'ConfigMap';
    if (targetKind === 'Node' || targetKind === 'Namespace') targetNamespace = '-';
  } else if (kind === 'get_secret' || kind === 'patch_secret') {
    targetKind = 'Secret';
  } else if (kind === 'helm_releases') {
    targetKind = 'Namespace';
    targetName = values.namespace || '-';
    targetNamespace = '-';
  } else if (kind === 'exec_readonly') {
    targetKind = 'Pod';
    targetName = values.pod ?? '';
  } else if (kind === 'create_configmap' || kind === 'delete_configmap') {
    targetKind = 'ConfigMap';
  } else if (kind === 'pdb_set') {
    targetKind = 'PodDisruptionBudget';
  } else if (kind === 'patch_resource') {
    targetKind = values.kind ?? 'Service';
  } else if (kind === 'pvc_expand') {
    targetKind = 'PersistentVolumeClaim';
  } else if (kind === 'delete_job') {
    targetKind = 'Job';
  } else if (kind === 'pod_logs') {
    targetKind = 'Pod';
    targetName = values.pod ?? '';
  } else if (kind === 'list_events') {
    targetKind = 'Namespace';
    targetName = values.namespace || '-';
    targetNamespace = '-';
  } else if (kind === 'run_debug_pod') {
    targetKind = 'Namespace';
    targetName = `probe-${Date.now() % 100000}`;
  } else if (kind === 'net_probe') {
    targetKind = 'Namespace';
    targetName = values.namespace || '-';
  }

  if (kind === 'scale') params = { replicas: Number(values.replicas ?? 1) };
  else if (kind === 'set_image')
    params = { image: values.image ?? '', ...(values.container ? { container: values.container } : {}) };
  else if (kind === 'hpa_set')
    params = { minReplicas: Number(values.minReplicas ?? 1), maxReplicas: Number(values.maxReplicas ?? 1) };
  else if (kind === 'node_taint')
    params = { key: values.key ?? '', value: values.value ?? '', effect: values.effect ?? 'NoSchedule' };
  else if (kind === 'node_untaint') params = { key: values.key ?? '' };
  else if (kind === 'set_resources')
    params = {
      ...(values.container ? { container: values.container } : {}),
      ...(values.requestsCpu ? { requestsCpu: values.requestsCpu } : {}),
      ...(values.requestsMemory ? { requestsMemory: values.requestsMemory } : {}),
      ...(values.limitsCpu ? { limitsCpu: values.limitsCpu } : {}),
      ...(values.limitsMemory ? { limitsMemory: values.limitsMemory } : {}),
    };
  else if (kind === 'set_env')
    params = { ...(values.container ? { container: values.container } : {}), envName: values.envName ?? '', value: values.value ?? '', remove: isTrue(values.remove) };
  else if (kind === 'rollout_to_revision') params = { revision: Number(values.revision ?? 1) };
  else if (kind === 'rollout_history') params = { limit: Number(values.limit ?? 10) };
  else if (kind === 'statefulset_partition') params = { partition: Number(values.partition ?? 0) };
  else if (kind === 'hpa_toggle') params = { enabled: isTrue(values.enabled) };
  else if (kind === 'patch_configmap' || kind === 'annotate' || kind === 'set_label')
    params = { key: values.key ?? '', value: values.value ?? '', remove: isTrue(values.remove) };
  else if (kind === 'evict_pod') params = values.gracePeriodSeconds ? { gracePeriodSeconds: Number(values.gracePeriodSeconds) } : {};
  else if (kind === 'cleanup_jobs') params = values.olderThanHours ? { olderThanHours: Number(values.olderThanHours) } : {};
  else if (kind === 'patch_secret')
    params = { key: values.key ?? '', value: values.value ?? '', remove: isTrue(values.remove) };
  else if (kind === 'create_configmap') {
    // Textarea: eine key=value-Zeile je Eintrag → data-Map.
    const data: Record<string, string> = {};
    for (const line of (values.data ?? '').split('\n')) {
      const i = line.indexOf('=');
      if (i > 0) data[line.slice(0, i).trim()] = line.slice(i + 1);
    }
    params = { data };
  } else if (kind === 'pdb_set')
    params = values.mode === 'maxUnavailable' ? { maxUnavailable: values.value ?? '' } : { minAvailable: values.value ?? '' };
  else if (kind === 'patch_resource') {
    let patch: unknown = {};
    try { patch = JSON.parse(values.patch ?? '{}'); } catch { patch = { __invalid: values.patch } }
    params = { patch };
  } else if (kind === 'pvc_expand') params = { size: values.size ?? '' };
  else if (kind === 'exec_readonly') {
    const argv = (values.command ?? '').split('\n').map((s) => s.trim()).filter(Boolean);
    params = { command: argv, ...(values.container ? { container: values.container } : {}), ...(isTrue(values.retry) ? { retry: true } : {}) };
  } else if (kind === 'pod_logs')
    params = {
      ...(values.container ? { container: values.container } : {}),
      ...(isTrue(values.previous) ? { previous: true } : {}),
      ...(values.tailLines ? { tailLines: Number(values.tailLines) } : {}),
    };
  else if (kind === 'list_events')
    params = { ...(isTrue(values.warningsOnly) ? { warningsOnly: true } : {}), ...(values.limit ? { limit: Number(values.limit) } : {}) };
  else if (kind === 'run_debug_pod') {
    const argv = (values.command ?? '').split('\n').map((s) => s.trim()).filter(Boolean);
    params = {
      image: values.image ?? '',
      command: argv,
      ...(values.pvcName ? { pvcName: values.pvcName } : {}),
      ...(values.nodeName ? { nodeName: values.nodeName } : {}),
    };
  } else if (kind === 'net_probe')
    params = { mode: values.mode ?? 'http', target: values.target ?? '', ...(values.method ? { method: values.method } : {}) };

  // Generisch: optionaler Ablauf-Timeout (10–1800s) — gilt für jede Action.
  if (values.timeoutSeconds && Number(values.timeoutSeconds) > 0) {
    params = { ...params, timeoutSeconds: Number(values.timeoutSeconds) };
  }

  return { kind: kind as ActionKind, targetNamespace, targetKind, targetName, params };
}

/* ── Library-Katalog: Kategorien, Sicherheitsklassen, Icons (App-Store) ──── */

type Category = 'deploy' | 'scale' | 'config' | 'network' | 'storage' | 'batch' | 'pods' | 'node' | 'investigate';
type ActionClass = RiskLevel; // dieselbe 4er-Skala wie die Guardrails (lib/approval)

const CATEGORY_ORDER: { key: Category; label: string }[] = [
  { key: 'investigate', label: 'Investigate' },
  { key: 'deploy', label: 'Deploy & Release' },
  { key: 'scale', label: 'Scaling' },
  { key: 'config', label: 'Config & Secrets' },
  { key: 'network', label: 'Network & Policies' },
  { key: 'storage', label: 'Storage' },
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
  { key: 'config', label: 'Config' },
  { key: 'network', label: 'Network' },
  { key: 'storage', label: 'Storage' },
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
  rollout_restart: { category: 'deploy', klass: 'disruptive' },
  rollout_pause: { category: 'deploy', klass: 'reversible' },
  rollout_resume: { category: 'deploy', klass: 'reversible' },
  rollout_undo: { category: 'deploy', klass: 'disruptive' },
  scale: { category: 'scale', klass: 'reversible' },
  hpa_set: { category: 'scale', klass: 'reversible' },
  cronjob_trigger: { category: 'batch', klass: 'disruptive' },
  cronjob_suspend: { category: 'batch', klass: 'reversible' },
  cronjob_resume: { category: 'batch', klass: 'reversible' },
  delete_pod: { category: 'pods', klass: 'disruptive' },
  cleanup_pods: { category: 'pods', klass: 'disruptive' },
  cordon: { category: 'node', klass: 'reversible' },
  uncordon: { category: 'node', klass: 'reversible' },
  drain: { category: 'node', klass: 'destructive' },
  drain_preview: { category: 'node', klass: 'read' },
  node_taint: { category: 'node', klass: 'reversible' },
  node_untaint: { category: 'node', klass: 'reversible' },
  // Erweiterter Katalog (Batch 1)
  rollout_history: { category: 'deploy', klass: 'read' },
  rollout_to_revision: { category: 'deploy', klass: 'reversible' },
  statefulset_partition: { category: 'deploy', klass: 'reversible' },
  set_resources: { category: 'config', klass: 'reversible' },
  set_env: { category: 'config', klass: 'reversible' },
  patch_configmap: { category: 'config', klass: 'reversible' },
  annotate: { category: 'config', klass: 'reversible' },
  set_label: { category: 'config', klass: 'reversible' },
  hpa_toggle: { category: 'scale', klass: 'reversible' },
  evict_pod: { category: 'pods', klass: 'disruptive' },
  cleanup_jobs: { category: 'batch', klass: 'disruptive' },
  // Erweiterter Katalog (Batch 2) — Levels spiegeln copilot_policy.go
  get_resource: { category: 'investigate', klass: 'read' },
  describe_resource: { category: 'investigate', klass: 'read' },
  get_secret: { category: 'investigate', klass: 'read' },
  helm_releases: { category: 'investigate', klass: 'read' },
  exec_readonly: { category: 'investigate', klass: 'disruptive' },
  patch_secret: { category: 'config', klass: 'reversible' },
  create_configmap: { category: 'config', klass: 'destructive' },
  delete_configmap: { category: 'config', klass: 'destructive' },
  pdb_set: { category: 'network', klass: 'reversible' },
  patch_resource: { category: 'network', klass: 'destructive' },
  pvc_expand: { category: 'storage', klass: 'destructive' },
  delete_job: { category: 'batch', klass: 'disruptive' },
  pod_logs: { category: 'investigate', klass: 'read' },
  list_events: { category: 'investigate', klass: 'read' },
  run_debug_pod: { category: 'investigate', klass: 'destructive' },
  net_probe: { category: 'investigate', klass: 'read' },
};

// TARGET_LABEL: worauf eine Action wirkt — als Chip auf der Karte, damit man
// bei ~45 Actions auf einen Blick das Ziel-Objekt sieht.
const TARGET_LABEL: Record<string, string> = {
  rollout_restart: 'workload', rollout_undo: 'deployment', rollout_pause: 'deployment',
  rollout_resume: 'deployment', rollout_history: 'deployment', rollout_to_revision: 'deployment',
  set_image: 'workload', scale: 'workload', statefulset_partition: 'statefulset',
  set_resources: 'workload', set_env: 'workload', debug_bundle: 'workload', pod_events: 'workload',
  hpa_set: 'hpa', hpa_toggle: 'hpa',
  delete_pod: 'pod', evict_pod: 'pod', exec_readonly: 'pod',
  cleanup_pods: 'namespace', cleanup_jobs: 'namespace', helm_releases: 'namespace',
  cordon: 'node', uncordon: 'node', drain: 'node', drain_preview: 'node',
  node_taint: 'node', node_untaint: 'node', set_label: 'node · ns',
  patch_configmap: 'configmap', create_configmap: 'configmap', delete_configmap: 'configmap',
  get_secret: 'secret', patch_secret: 'secret',
  pdb_set: 'pdb', patch_resource: 'svc · ing · netpol · pdb',
  pvc_expand: 'pvc', delete_job: 'job',
  cronjob_trigger: 'cronjob', cronjob_suspend: 'cronjob', cronjob_resume: 'cronjob',
  get_resource: 'any object · crd', describe_resource: 'any object · crd', annotate: 'any object',
  pod_logs: 'pod', list_events: 'namespace', run_debug_pod: 'ephemeral pod', net_probe: 'url · host:port',
};

// HOST_NATIVE: die wenigen Kinds, die KEIN Starlark-Skript sind, weil sie eine
// Agent-Host-Fähigkeit brauchen (Eviction-API, Container-Exec, Kubelet-Logs,
// In-Cluster-Probe). Die UI zeigt deren echte native Pipeline — nicht ein
// Fake-Skript: „shown == executed" gilt auch hier.
const HOST_NATIVE: Record<string, string> = {
  drain:
    'Runs natively on the agent: cordon, then a PDB-aware Eviction API across every namespace (DaemonSet & mirror pods are kept), waiting until the node is empty. Cancel re-uncordons.',
  evict_pod:
    'Runs natively on the agent: the graceful, PDB-aware Eviction API — never a force delete — then waits for a ready replacement.',
  exec_readonly:
    'Runs natively on the agent: one whitelisted argv (no shell) exec into the container. The only way to read in-container file logs.',
  net_probe:
    'Runs natively on the agent: an in-cluster http / tcp / dns / tls probe — status, latency, and certificate days-to-expiry.',
  pod_logs:
    'Runs natively on the agent: kubectl logs straight from the kubelet, including the PREVIOUS crashed container.',
  run_debug_pod:
    'Runs natively on the agent: an ephemeral probe pod (image + argv, optional PVC mount). Its logs are the result; the pod is always cleaned up.',
};

// builtinSource returns the EXACT executed source for a kind: the snapshot
// Starlark for scriptable kinds (single source, generated from the agent's
// embedded scripts) or the native-primitive descriptor for host-native kinds.
function builtinSource(kind: string): string {
  const meta = BUILTIN_ACTIONS[kind];
  if (meta) return meta.source;
  const note = HOST_NATIVE[kind];
  return note ? `# ${kind} — native agent primitive\n#\n# ${note}\n` : `# ${kind}\n`;
}

// REVERSIBLE_META renders the snapshot-substrate reversibility as a first-class,
// colorblind-glyphed status. Snapshot = auto-rollback; readonly = nothing to
// undo; none = irreversible (verify carefully); native = runs on the agent host.
const REVERSIBLE_META = {
  snapshot: {
    label: 'auto-rollback',
    hint: 'The script snapshots what it touches; a failure rolls every mutation back automatically, and Revert stays available on the run.',
    klass: 'reversible',
  },
  readonly: {
    label: 'read-only',
    hint: 'Reads cluster state only — no mutation, nothing to undo.',
    klass: 'read',
  },
  none: {
    label: 'irreversible',
    hint: 'This action has lasting effects that cannot be snapshot-restored. Verify carefully before running.',
    klass: 'destructive',
  },
  native: {
    label: 'native primitive',
    hint: 'Runs as a built-in agent capability rather than a Starlark script.',
    klass: 'disruptive',
  },
} satisfies Record<string, { label: string; hint: string; klass: RiskLevel }>;

function reversibleOf(kind: string): keyof typeof REVERSIBLE_META {
  return (BUILTIN_ACTIONS[kind]?.reversible as keyof typeof REVERSIBLE_META) ?? 'native';
}

// parseSteps extracts the step("…") pipeline a script declares — the honest
// timeline the run will emit. Dynamic %-formatting is shown verbatim.
function parseSteps(src: string): string[] {
  const out: string[] = [];
  const re = /step\(\s*"((?:[^"\\]|\\.)*)"/g;
  let m: RegExpExecArray | null;
  // Soften Starlark %-format verbs (%d/%s/…) to a neutral placeholder so the
  // preview reads as a declared step, not a raw format string.
  const clean = (s: string) => s.replace(/%%/g, '%').replace(/%[#0-9.+\- ]*[a-zA-Z]/g, '…');
  while ((m = re.exec(src))) out.push(clean(m[1] ?? ''));
  return out;
}

// Level-Labels/Glyphs kommen aus lib/approval (LEVEL_META) — eine Quelle für
// Katalog, Copilot-Guardrails und Runs-Seite.

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
    set_resources: (<><rect x="3.5" y="4" width="17" height="16" rx="1.5" /><path d="M8 8v8M8 16l4-3 4 3M16 8v4" /></>),
    set_env: (<><path d="M4 7h6v10H4zM14 7h6v10h-6" /><path d="M10 12h4" /></>),
    rollout_history: (<><circle cx="12" cy="12" r="8" /><path d="M12 7v5l3 2" /><path d="M4 4v3h3" /></>),
    rollout_to_revision: (<><path d="M9 7L4 12l5 5" /><path d="M4 12h9a6 6 0 0 1 6 6" /><circle cx="19" cy="6" r="1.6" /></>),
    statefulset_partition: (<><rect x="4" y="4" width="16" height="6" rx="1" /><rect x="4" y="14" width="16" height="6" rx="1" /><path d="M4 12h7" /></>),
    hpa_toggle: (<><rect x="3.5" y="8" width="17" height="8" rx="4" /><circle cx="8" cy="12" r="2.4" /></>),
    patch_configmap: (<><rect x="4" y="4" width="16" height="16" rx="1.5" /><path d="M8 9h8M8 13h5" /></>),
    annotate: (<><path d="M4 5h16v10H10l-4 4v-4H4z" /></>),
    set_label: (<><path d="M4 8l6-4h9a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1h-9l-6-4z" /><circle cx="16" cy="9" r="1.2" /></>),
    evict_pod: (<><circle cx="12" cy="9" r="4" /><path d="M6 20a6 6 0 0 1 12 0" /><path d="M19 4l2 2-2 2" /></>),
    cleanup_jobs: (<><path d="M5 7h14M10 7V5h4v2M6.5 7l1 12h9l1-12" /><path d="M10 11v5M14 11v5" /></>),
    drain_preview: (<><path d="M12 3s6.5 7.2 6.5 11.5a6.5 6.5 0 0 1-13 0C5.5 10.2 12 3 12 3z" /><circle cx="12" cy="13" r="2.2" /></>),
    get_resource: (<><path d="M6 3.5h9l3.5 3.5v13.5H6z" /><path d="M15 3.5V7h3.5M9 12h6M9 15.5h4" /></>),
    describe_resource: (<><path d="M6 3.5h9l3.5 3.5v13.5H6z" /><path d="M15 3.5V7h3.5" /><circle cx="11" cy="13" r="2.6" /><path d="M13 15l2.5 2.5" /></>),
    get_secret: (<><rect x="5" y="10.5" width="14" height="9" rx="1.5" /><path d="M8 10.5V7.5a4 4 0 0 1 8 0v3" /><circle cx="12" cy="15" r="1.4" /></>),
    helm_releases: (<><circle cx="12" cy="12" r="8" /><path d="M12 4v16M5.5 7.5l13 9M5.5 16.5l13-9" /></>),
    exec_readonly: (<><rect x="3.5" y="5" width="17" height="14" rx="1.5" /><path d="M7 9.5l3 2.5-3 2.5M12.5 15H17" /></>),
    patch_secret: (<><rect x="5" y="10.5" width="14" height="9" rx="1.5" /><path d="M8 10.5V7.5a4 4 0 0 1 8 0v3" /><path d="M10.5 15h3" /></>),
    create_configmap: (<><rect x="4" y="4" width="16" height="16" rx="1.5" /><path d="M12 9v6M9 12h6" /></>),
    delete_configmap: (<><rect x="4" y="4" width="16" height="16" rx="1.5" /><path d="M9 12h6" /></>),
    pdb_set: (<><path d="M12 3l7.5 3.5v5c0 4.6-3 8-7.5 9.5C7.5 19.5 4.5 16.1 4.5 11.5v-5z" /><path d="M9 12h6" /></>),
    patch_resource: (<><path d="M4 20l4.5-1L20 7.5 16.5 4 5 15.5z" /><path d="M14 6.5L17.5 10" /></>),
    pvc_expand: (<><ellipse cx="12" cy="6" rx="7" ry="2.6" /><path d="M5 6v12c0 1.4 3.1 2.6 7 2.6s7-1.2 7-2.6V6" /><path d="M12 10.5v5M9.8 13.3L12 15.5l2.2-2.2" /></>),
    delete_job: (<><circle cx="12" cy="12" r="8" /><path d="M9 9l6 6M15 9l-6 6" /></>),
    pod_logs: (<><rect x="4" y="4" width="16" height="16" rx="1.5" /><path d="M7.5 8.5h9M7.5 12h9M7.5 15.5h5" /></>),
    list_events: (<><path d="M5 5h14M5 9.5h14M5 14h10M5 18.5h7" /><circle cx="19" cy="16.5" r="2.4" /></>),
    run_debug_pod: (<><rect x="4" y="7" width="12" height="12" rx="1.5" /><path d="M10 4h10v10" strokeDasharray="2.5 2" /><path d="M8 12l2 1.8L8 15.6M12 15.6h2" /></>),
    net_probe: (<><circle cx="12" cy="12" r="8" /><path d="M4 12h16M12 4c2.5 2.5 2.5 13 0 16M12 4c-2.5 2.5-2.5 13 0 16" /></>),
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

// The editor now works on ONE thing — the script. Name, summary, risk,
// reversibility, targets, params and timeout all live in its `# @…` front-matter
// (co-evaluated live). id=null means a new workflow.
type EditorState = {
  id: string | null;
  source: string;
};

// composeDefSource makes an existing (possibly front-matter-less) definition
// editable as a single front-matter-carrying script: if the stored source has no
// `# @name`, synthesise a front-matter block from the definition's columns.
function composeDefSource(d: ActionDefinition): string {
  if (/^\s*#\s*@name\b/m.test(d.source)) return d.source;
  const fm: string[] = [`# @name ${d.name}`];
  if (d.description) fm.push(`# @summary ${d.description}`);
  fm.push('# @reversible snapshot');
  for (const p of d.params ?? []) {
    const bits = [`# @param`, p.name, p.type ?? 'string'];
    if (p.type === 'int' && p.min != null && p.max != null) bits.push(String(p.min), String(p.max));
    if (p.type === 'enum' && p.options?.length) bits.push(...p.options);
    if (p.required) bits.push('required');
    if (p.default) bits.push(`=${p.default}`);
    fm.push(bits.join(' '));
  }
  if (d.timeoutSeconds && d.timeoutSeconds !== 600) fm.push(`# @timeout ${d.timeoutSeconds}`);
  return fm.join('\n') + '\n#\n' + d.source;
}

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
  const [detail, setDetail] = useState<Builtin | null>(null);
  const [runDialog, setRunDialog] = useState<
    | { type: 'builtin'; builtin: (typeof BUILTINS)[number] }
    | { type: 'script'; def: ActionDefinition }
    | null
  >(null);
  const [error, setError] = useState<string | null>(null);
  const [libQ, setLibQ] = useState('');
  const [libTab, setLibTab] = useState<string>('all');
  const [levelF, setLevelF] = useState<ActionClass | 'all'>('all');

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

  // Inventar für typed Pickers (ConfigMap/Secret/PDB/Job/… Namen).
  const [inventory, setInventory] = useState<InventoryKind[]>([]);
  useEffect(() => {
    if (!orgId) return;
    getInventory(orgId, clusterId).then((r) => setInventory(r.kinds ?? [])).catch(() => {});
  }, [orgId, clusterId]);

  // "/" fokussiert die Katalog-Suche (keyboard-first, wie überall im Produkt).
  const searchRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key !== '/' || e.metaKey || e.ctrlKey || e.altKey) return;
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.tagName === 'SELECT' || t.isContentEditable)) return;
      e.preventDefault();
      searchRef.current?.focus();
    }
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, []);

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
    // Everything is derived from the front-matter — the single source.
    const { meta, diags } = lintWorkflow(editor.source);
    if (hasBlockingError(diags)) {
      setError('fix the errors flagged in the editor before saving');
      return;
    }
    if (!meta.name) {
      setError('add a `# @name my-workflow` line');
      return;
    }
    const body = {
      name: meta.name,
      description: meta.summary ?? '',
      params: (meta.params ?? []) as ActionDefParam[],
      source: editor.source,
      timeoutSeconds: meta.timeout ?? 600,
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
  // die Source ist der KANONISCHE, EXAKT AUSGEFÜHRTE Code der Action (aus den
  // eingebetteten Agent-Skripten generiert), die Felder werden zu Params.
  const forkBuiltin = useCallback((b: (typeof BUILTINS)[number]) => {
    setDetail(null);
    // Fork the FULL script incl. front-matter (so name/risk/reversibility/params
    // are editable). Host-native kinds have no script → seed a front-matter stub.
    const full = BUILTIN_ACTIONS[b.kind]?.full;
    const source = full ?? `# @name ${b.kind.replace(/_/g, '-')}\n# @summary ${b.description}\n# @reversible none\n#\n# ${b.name} runs as a native agent primitive; write your own workflow here.\n`;
    setEditor({ id: null, source });
  }, []);

  return (
    <div className="flex h-[calc(100dvh-52px)] flex-col px-4 pt-4 sm:px-5">
      <PageHeader kicker="act / safe-actions" title="Actions" meta={`${BUILTINS.length} built-in · ${defs?.length ?? 0} custom`}>
        <div className="flex items-center gap-2">
          <Link
            href={`/clusters/${clusterId}/runs`}
            className="rp-focus flex h-8 items-center gap-1.5 rounded-skin-sm border border-line px-2.5 font-mono text-[11px] text-mid transition-colors hover:bg-hover hover:text-ink"
          >
            {running > 0 ? (
              <>
                <span className="rp-breath inline-block h-1.5 w-1.5 rounded-full" style={{ background: 'var(--rp-green)', color: 'var(--rp-green)' }} />
                <span className="tnum">{running} running</span>
              </>
            ) : (
              'runs'
            )}
            <span aria-hidden>→</span>
          </Link>
          <GuardrailsMenu clusterId={clusterId} />
          <button
            type="button"
            onClick={() => setEditor({ id: null, source: EXAMPLE_SOURCE })}
            className="rp-focus h-8 rounded-skin-sm px-3 font-mono text-[11px] font-semibold transition-opacity hover:opacity-90"
            style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}
          >
            + New workflow
          </button>
        </div>
      </PageHeader>

      <div className="mt-3 flex min-h-0 flex-1 flex-col pb-3">
        {/* ── Toolbar: Suche · Level-Filter · Kategorie-Tabs ── */}
        <div className="shrink-0">
          <div className="flex flex-wrap items-center gap-2">
            <input
              ref={searchRef}
              value={libQ}
              onChange={(e) => setLibQ(e.target.value)}
              spellCheck={false}
              placeholder={`search ${BUILTINS.length} actions…  ( / )`}
              className="rp-focus h-9 min-w-[220px] flex-1 rounded-skin-sm border border-line bg-inset px-3 font-mono text-[12px] text-ink placeholder:text-faint"
            />
            <div className="flex items-center gap-1 overflow-x-auto pb-1">
              {(['all', ...RISK_LEVELS] as const).map((l) => {
                const on = levelF === l;
                const count =
                  l === 'all'
                    ? BUILTINS.length
                    : BUILTINS.filter((b) => (ACTION_META[b.kind]?.klass ?? 'reversible') === l).length;
                return (
                  <button
                    key={l}
                    type="button"
                    onClick={() => setLevelF(l as ActionClass | 'all')}
                    className={cn(
                      'rp-focus flex shrink-0 items-center gap-1 whitespace-nowrap rounded-skin-chip border px-2 py-1 font-mono text-[10px] transition-colors',
                      on ? 'border-line-strong bg-hover text-ink' : 'border-line text-muted hover:text-ink',
                    )}
                  >
                    {l === 'all' ? (
                      'any level'
                    ) : (
                      <>
                        <span style={{ color: levelColor(l) }}>{LEVEL_META[l as RiskLevel].glyph}</span>
                        {LEVEL_META[l as RiskLevel].label}
                      </>
                    )}
                    <span className="text-[9px] text-faint tnum">{count}</span>
                  </button>
                );
              })}
            </div>
          </div>

          <div className="mt-2 flex gap-1 overflow-x-auto pb-1">
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
        </div>

        {/* ── Katalog ── */}
        <div className="mt-2 min-h-0 flex-1 overflow-y-auto pr-1">
          {libTab === 'custom' ? (
            defs === null ? (
              <div className="mt-2 flex items-center gap-2 text-muted">
                <Spinner /> <span className="font-mono text-[11px]">loading…</span>
              </div>
            ) : defs.length === 0 ? (
              <div
                className="mt-2 rounded-skin border border-dashed p-5 text-center font-mono text-[11px] leading-relaxed text-muted"
                style={{ borderColor: 'var(--rp-line-strong)' }}
              >
                No workflows yet — fork any built-in action or start from scratch. Hermetic
                Python, verified on the pod, auto-rolled-back. Hit “+ New workflow”.
              </div>
            ) : (
              <div className="mt-1 space-y-2">
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
                          onClick={() => setEditor({ id: d.id, source: composeDefSource(d) })}
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
            (() => {
              const visible = BUILTINS.filter(
                (b) =>
                  (libTab === 'all' || ACTION_META[b.kind]?.category === libTab) &&
                  (levelF === 'all' || (ACTION_META[b.kind]?.klass ?? 'reversible') === levelF) &&
                  matchAction(b, libQ),
              );
              if (visible.length === 0)
                return <div className="mt-3 font-mono text-[11px] text-faint">no actions match.</div>;
              // Auf „All" ohne Suche: nach Kategorie gruppiert (skaliert auf hunderte
              // Actions); gefiltert/gesucht: flache Treffer-Liste.
              const groups =
                libTab === 'all' && !libQ.trim()
                  ? CATEGORY_ORDER.map((c) => ({
                      label: c.label,
                      items: visible.filter((b) => ACTION_META[b.kind]?.category === c.key),
                    })).filter((g) => g.items.length > 0)
                  : [{ label: '', items: visible }];
              return groups.map((g) => (
                <div key={g.label || 'results'} className="mb-4">
                  {g.label ? (
                    <div className="sticky top-0 z-10 -mx-1 mb-1.5 flex items-baseline gap-2 px-1 py-1 backdrop-blur-sm" style={{ background: 'color-mix(in oklab, var(--rp-bg-base) 88%, transparent)' }}>
                      <span className="rp-micro !text-[10px]">{g.label}</span>
                      <span className="font-mono text-[9.5px] text-faint tnum">{g.items.length}</span>
                    </div>
                  ) : null}
                  <div className="grid grid-cols-1 gap-1.5 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
                    {g.items.map((b) => {
                      const klass = (ACTION_META[b.kind]?.klass ?? 'reversible') as RiskLevel;
                      return (
                        <div
                          key={b.kind}
                          role="button"
                          tabIndex={0}
                          onClick={() => setDetail(b)}
                          onKeyDown={(e) => {
                            if (e.key === 'Enter' || e.key === ' ') {
                              e.preventDefault();
                              setDetail(b);
                            }
                          }}
                          title="open the exact executed script"
                          className="rp-focus group flex cursor-pointer items-start gap-2.5 rounded-skin-sm border border-line bg-raised p-2.5 transition-colors hover:border-line-strong"
                        >
                          <span className="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-skin-sm border border-line bg-inset text-mid transition-colors group-hover:text-ink">
                            <ActionIcon kind={b.kind} />
                          </span>
                          <div className="min-w-0 flex-1">
                            <div className="flex items-center gap-1.5">
                              <span className="min-w-0 truncate font-mono text-[11.5px] font-semibold text-ink">{b.name}</span>
                              <span
                                className="ml-auto shrink-0 font-mono text-[9px] uppercase tracking-[0.05em]"
                                style={{ color: levelColor(klass) }}
                                title={LEVEL_META[klass].hint}
                              >
                                {LEVEL_META[klass].glyph} {LEVEL_META[klass].label}
                              </span>
                            </div>
                            <p className="mt-0.5 line-clamp-2 font-mono text-[10px] leading-snug text-muted" title={BUILTIN_ACTIONS[b.kind]?.summary ?? b.description}>
                              {BUILTIN_ACTIONS[b.kind]?.summary ?? b.description}
                            </p>
                            <div className="mt-1.5 flex items-center gap-1.5 opacity-70 transition-opacity group-hover:opacity-100">
                              <button
                                type="button"
                                onClick={(e) => {
                                  e.stopPropagation();
                                  setRunDialog({ type: 'builtin', builtin: b });
                                }}
                                className="rp-focus rounded-skin-chip border border-line px-2 py-0.5 font-mono text-[10px] text-ink transition-colors hover:bg-hover"
                              >
                                run →
                              </button>
                              <button
                                type="button"
                                onClick={(e) => {
                                  e.stopPropagation();
                                  forkBuiltin(b);
                                }}
                                title="fork as an editable Starlark workflow"
                                className="rp-focus rounded-skin-chip border border-line px-1.5 py-0.5 font-mono text-[10px] text-mid transition-colors hover:bg-hover hover:text-ink"
                              >
                                {'</>'}
                              </button>
                              {TARGET_LABEL[b.kind] ? (
                                <span className="ml-auto shrink-0 truncate rounded-skin-chip bg-inset px-1.5 py-0.5 font-mono text-[9px] text-faint" title={`acts on: ${TARGET_LABEL[b.kind]}`}>
                                  {TARGET_LABEL[b.kind]}
                                </span>
                              ) : null}
                            </div>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                </div>
              ));
            })()
          )}
        </div>
      </div>

      {detail ? (
        <ActionDetail
          b={detail}
          onClose={() => setDetail(null)}
          onRun={() => {
            setRunDialog({ type: 'builtin', builtin: detail });
            setDetail(null);
          }}
          onFork={() => forkBuiltin(detail)}
        />
      ) : null}

      {editor ? (
        <WorkflowEditor
          state={editor}
          error={error}
          onChange={(source) => setEditor({ ...editor, source })}
          check={orgId ? (src) => checkWorkflow(orgId, src) : undefined}
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
          inventory={inventory}
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

// ActionDetail — das „neue Gewand" jeder Action: ein deklarativer Header
// (Ziel · param-abhängiges Level · Reversibilität als Snapshot-Semantik · die
// step()-Pipeline) über dem EXAKT ausgeführten Snapshot-Skript. Built-in und
// Custom teilen dieselbe Substanz — hier sieht man, dass „shown == executed".
function ActionDetail({
  b,
  onClose,
  onRun,
  onFork,
}: {
  b: Builtin;
  onClose: () => void;
  onRun: () => void;
  onFork: () => void;
}) {
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose();
    }
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);

  const meta: BuiltinMeta | undefined = BUILTIN_ACTIONS[b.kind];
  const source = builtinSource(b.kind);
  const steps = parseSteps(source);
  const rev = REVERSIBLE_META[reversibleOf(b.kind)];
  const klass = (ACTION_META[b.kind]?.klass ?? 'reversible') as RiskLevel;
  const targets = meta?.targets ?? [];
  const isNative = !meta;

  return (
    <div className="fixed inset-0 z-50" role="dialog" aria-modal="true" aria-label={`${b.name} details`}>
      <button
        type="button"
        aria-label="Close"
        onClick={onClose}
        className="absolute inset-0 cursor-default"
        style={{ backgroundColor: 'var(--rp-scrim)' }}
      />
      <aside
        className="absolute inset-y-0 right-0 flex w-[min(720px,94vw)] flex-col border-l border-line bg-raised"
        style={{
          boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)',
          animation: 'rp-drawer-in var(--rp-dur-large) var(--rp-ease-enter)',
        }}
      >
        <header className="shrink-0 border-b border-line px-5 pb-3 pt-4">
          <div className="rp-micro !text-[10px]">action / {ACTION_META[b.kind]?.category ?? 'built-in'}</div>
          <div className="rp-keyline mt-2 flex flex-wrap items-center justify-between gap-2 pb-3">
            <div className="flex items-center gap-2.5">
              <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-skin-sm border border-line bg-inset text-mid">
                <ActionIcon kind={b.kind} />
              </span>
              <div>
                <h2 className="font-display text-[20px] font-bold tracking-tightest text-ink">{b.name}</h2>
                <div className="mt-0.5 font-mono text-[10px] text-faint">{b.kind}</div>
              </div>
            </div>
            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={onFork}
                title="fork as an editable Starlark workflow"
                className="rounded-skin-sm border border-line px-2.5 py-1.5 font-mono text-[11px] text-mid transition-colors hover:bg-hover hover:text-ink"
              >
                {'</>'} fork
              </button>
              <button
                type="button"
                onClick={onRun}
                className="rp-focus h-8 rounded-skin-sm px-3.5 font-mono text-[11.5px] font-semibold transition-opacity hover:opacity-90"
                style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}
              >
                run →
              </button>
            </div>
          </div>
        </header>

        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
          <p className="font-mono text-[12px] leading-relaxed text-mid">
            {meta?.summary ?? b.description}
          </p>

          {/* ── Deklarativer Header: Level · Reversibilität · Ziele ── */}
          <div className="mt-4 grid grid-cols-1 gap-2 sm:grid-cols-3">
            <div className="rounded-skin-sm border border-line bg-inset px-3 py-2">
              <div className="rp-micro !text-[9.5px]">level</div>
              <div className="mt-1 flex items-center gap-1.5 font-mono text-[11px]" style={{ color: levelColor(klass) }} title={LEVEL_META[klass].hint}>
                <span>{LEVEL_META[klass].glyph}</span>
                {LEVEL_META[klass].label}
              </div>
            </div>
            <div className="rounded-skin-sm border border-line bg-inset px-3 py-2">
              <div className="rp-micro !text-[9.5px]">reversibility</div>
              <div className="mt-1 flex items-center gap-1.5 font-mono text-[11px]" style={{ color: levelColor(rev.klass) }} title={rev.hint}>
                <span>{LEVEL_META[rev.klass].glyph}</span>
                {rev.label}
              </div>
            </div>
            <div className="rounded-skin-sm border border-line bg-inset px-3 py-2">
              <div className="rp-micro !text-[9.5px]">acts on</div>
              <div className="mt-1 truncate font-mono text-[11px] text-mid" title={TARGET_LABEL[b.kind] ?? targets.join(' · ')}>
                {TARGET_LABEL[b.kind] ?? (targets.join(' · ') || '—')}
              </div>
            </div>
          </div>

          <p className="mt-2 font-mono text-[10.5px] leading-relaxed text-faint">{rev.hint}</p>

          {/* ── Ziel-Kinds (aus dem Skript-Front-matter) ── */}
          {targets.length > 0 ? (
            <div className="mt-3 flex flex-wrap items-center gap-1">
              <span className="rp-micro !text-[9.5px] mr-1">kinds</span>
              {targets.map((t) => (
                <span key={t} className="rounded-skin-chip bg-inset px-1.5 py-0.5 font-mono text-[9.5px] text-muted">
                  {t}
                </span>
              ))}
            </div>
          ) : null}

          {/* ── Pipeline: die step()-Schritte, die der Run als Timeline emittiert ── */}
          {steps.length > 0 ? (
            <div className="mt-4">
              <span className="rp-micro !text-[10px]">pipeline — the run emits these as a timeline</span>
              <ol className="mt-1.5 space-y-1">
                {steps.map((s, i) => (
                  <li key={`${s}-${i}`} className="flex items-center gap-2 font-mono text-[11px] text-mid">
                    <span className="flex h-4 w-4 shrink-0 items-center justify-center rounded-full border border-line text-[9px] text-faint tnum">
                      {i + 1}
                    </span>
                    {s}
                  </li>
                ))}
              </ol>
            </div>
          ) : null}

          {/* ── Der EXAKT ausgeführte Source ── */}
          <div className="mt-4">
            <div className="flex items-center justify-between">
              <span className="rp-micro !text-[10px]">{isNative ? 'native agent primitive' : 'executed script — snapshot substrate'}</span>
              <span className="font-mono text-[9.5px] text-faint">{isNative ? 'go' : 'starlark · shown == executed'}</span>
            </div>
            <pre
              className="mt-1.5 overflow-x-auto rounded-skin-sm border border-line bg-inset p-3 font-mono text-[11px] leading-relaxed text-ink"
              style={{ tabSize: 4 }}
            >
              <code>{source}</code>
            </pre>
          </div>
        </div>
      </aside>
    </div>
  );
}

function WorkflowEditor({
  state,
  error,
  onChange,
  check,
  onSave,
  onDelete,
  onClose,
}: {
  state: EditorState;
  error: string | null;
  onChange: (source: string) => void;
  check?: (source: string) => Promise<{ ok: boolean; error?: string }>;
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

  const [diags, setDiags] = useState<Diag[]>([]);
  // Metadata is co-evaluated live from the front-matter — the single source.
  const meta: WorkflowMeta = useMemo(() => parseFrontmatter(state.source), [state.source]);
  const errorCount = diags.filter((d) => d.severity === 'error').length;
  const warnCount = diags.filter((d) => d.severity === 'warning').length;
  const revMeta = REVERSIBLE_META[(meta.reversible as keyof typeof REVERSIBLE_META) ?? 'native'];

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
        className="absolute inset-y-0 right-0 flex w-[min(920px,96vw)] flex-col border-l border-line bg-raised"
        style={{
          boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)',
          animation: 'rp-drawer-in var(--rp-dur-large) var(--rp-ease-enter)',
        }}
      >
        <header className="shrink-0 border-b border-line px-5 pb-3 pt-4">
          <div className="rp-micro !text-[10px]">workflow / starlark · front-matter drives name · risk · reversibility · params</div>
          <div className="mt-2 flex flex-wrap items-center justify-between gap-2">
            <h2 className="font-display text-[20px] font-bold tracking-tightest text-ink">
              {meta.name ? (state.id ? `Edit ${meta.name}` : meta.name) : 'New workflow'}
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
                disabled={errorCount > 0}
                title={errorCount > 0 ? 'fix the errors first' : 'save'}
                className="rp-focus h-8 rounded-skin-sm px-3.5 font-mono text-[11.5px] font-semibold transition-opacity enabled:hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
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

          {/* live meta strip — parsed from the front-matter as you type */}
          <div className="mt-2.5 flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-[10.5px]">
            <span className="text-faint">risk <span className="text-mid">{meta.risk ?? '—'}</span></span>
            <span className="flex items-center gap-1" style={{ color: levelColor(revMeta.klass) }} title={revMeta.hint}>
              {LEVEL_META[revMeta.klass].glyph} {revMeta.label}
            </span>
            <span className="text-faint">acts on <span className="text-mid">{meta.targets.length ? meta.targets.join(' · ') : '—'}</span></span>
            <span className="text-faint">{meta.params.length} param{meta.params.length === 1 ? '' : 's'}</span>
            <span className="ml-auto flex items-center gap-1.5">
              {errorCount > 0 ? (
                <span style={{ color: levelColor('destructive') }}>▲ {errorCount} error{errorCount === 1 ? '' : 's'}</span>
              ) : warnCount > 0 ? (
                <span style={{ color: levelColor('disruptive') }}>◆ {warnCount} warning{warnCount === 1 ? '' : 's'}</span>
              ) : (
                <span style={{ color: 'var(--rp-green)' }}>● valid</span>
              )}
            </span>
          </div>
        </header>

        {error ? (
          <div
            className="mx-5 mt-3 shrink-0 rounded-skin-sm px-3 py-2 font-mono text-[11px]"
            style={{ color: 'var(--rp-tone-red-fg)', background: 'var(--rp-tone-red-bg)' }}
          >
            {error}
          </div>
        ) : null}

        {/* the big editor — the whole workflow lives here, front-matter included */}
        <div className="min-h-0 flex-1 px-5 py-3">
          <StarlarkEditor value={state.source} onChange={onChange} check={check} onDiagnostics={setDiags} />
        </div>

        {/* validation panel — every diagnostic, with its line + severity */}
        {diags.length > 0 ? (
          <div className="max-h-[28%] shrink-0 overflow-y-auto border-t border-line px-5 py-2">
            <div className="rp-micro !text-[9.5px] mb-1">validation</div>
            <ul className="space-y-0.5">
              {diags
                .slice()
                .sort((a, b) => a.line - b.line)
                .map((d, i) => {
                  const c = d.severity === 'error' ? levelColor('destructive') : d.severity === 'warning' ? levelColor('disruptive') : 'var(--rp-ink-faint)';
                  const g = d.severity === 'error' ? '▲' : d.severity === 'warning' ? '◆' : '○';
                  return (
                    <li key={i} className="flex items-start gap-2 font-mono text-[10.5px] leading-snug">
                      <span className="shrink-0 tnum text-faint">L{d.line + 1}</span>
                      <span className="shrink-0" style={{ color: c }}>{g}</span>
                      <span className="text-mid">{d.message}</span>
                    </li>
                  );
                })}
            </ul>
          </div>
        ) : (
          <p className="shrink-0 border-t border-line px-5 py-2 font-mono text-[9.5px] leading-relaxed text-faint">
            Front-matter (# @name/@risk/@reversible/@param) is co-evaluated · Ctrl+Space for completion · the control-plane compiles on save — broken or incoherent workflows cannot be stored.
          </p>
        )}
      </aside>
    </div>
  );
}

/* ── Run-Dialog ────────────────────────────────────────────────────────── */

function RunDialog({
  dialog,
  map,
  inventory,
  nodeNames,
  onClose,
  onRun,
}: {
  dialog:
    | { type: 'builtin'; builtin: (typeof BUILTINS)[number] }
    | { type: 'script'; def: ActionDefinition };
  map: ServiceMap | null;
  inventory: InventoryKind[];
  nodeNames: string[];
  onClose: () => void;
  onRun: (values: Record<string, string>) => Promise<void>;
}) {
  // A custom workflow's params come from its script front-matter — the single
  // source — so the run form always matches what the code declares (the stored
  // columns are just a derived index and could lag an external edit).
  const fields: BuiltinField[] =
    dialog.type === 'script'
      ? ((parseFrontmatter(dialog.def.source).params as BuiltinField[]).length
          ? (parseFrontmatter(dialog.def.source).params as BuiltinField[])
          : (dialog.def.params ?? []))
      : dialog.builtin.fields;
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
                style={{ color: levelColor(klass) }}
              >
                {LEVEL_META[klass].glyph} {LEVEL_META[klass].label}
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
              inventory={inventory}
              nodeNames={nodeNames}
              namespace={values['namespace'] ?? ''}
              onChange={(v) => setValues((prev) => ({ ...prev, [f.name]: v }))}
            />
          ))}
          {dialog.type === 'builtin' ? (
            <label className="flex items-center gap-2 pt-1">
              <span className="rp-micro !text-[10px]">timeout</span>
              <input
                type="number"
                min={10}
                max={1800}
                value={values.timeoutSeconds ?? ''}
                onChange={(e) => setValues((prev) => ({ ...prev, timeoutSeconds: e.target.value }))}
                placeholder="180"
                className="rp-focus h-8 w-20 rounded-skin-sm border border-line bg-inset px-2 font-mono text-[11px] text-ink placeholder:text-faint tnum"
              />
              <span className="font-mono text-[9.5px] text-faint">seconds (10–1800, blank = 180) · on timeout the engine rolls back</span>
            </label>
          ) : null}
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
  inventory = [],
  nodeNames = [],
  namespace,
  onChange,
}: {
  spec: BuiltinField;
  value: string;
  map: ServiceMap | null;
  inventory?: InventoryKind[];
  nodeNames?: string[];
  /** aktueller namespace-Wert — filtert die Workload-/Inventar-Vorschläge. */
  namespace: string;
  onChange: (v: string) => void;
}) {
  const label = spec.label || spec.name;
  const base =
    'rp-focus mt-1 h-9 w-full rounded-skin-sm border border-line bg-inset px-2.5 font-mono text-[12px] text-ink';

  let control: React.ReactNode;
  // Client-only-Picker: Inventar-gebundene Namen (ConfigMap/Secret/PDB/Job/…)
  // mit Freitext-Fallback (datalist), Textareas für Patches/Kommandos/Daten.
  if (spec.multiline) {
    control = (
      <textarea
        value={value}
        onChange={(e) => onChange(e.target.value)}
        spellCheck={false}
        rows={4}
        placeholder={spec.description}
        className="rp-focus mt-1 w-full rounded-skin-sm border border-line bg-inset px-2.5 py-2 font-mono text-[12px] leading-relaxed text-ink placeholder:text-faint"
      />
    );
  } else if (spec.resourceKind) {
    const items = (inventory.find((k) => k.kind === spec.resourceKind)?.items ?? [])
      .filter((it) => !namespace || !it.namespace || it.namespace === namespace);
    const listId = `inv-${spec.resourceKind}-${spec.name}`;
    control = (
      <div className="mt-1">
        <input
          value={value}
          onChange={(e) => onChange(e.target.value)}
          list={listId}
          spellCheck={false}
          placeholder={items.length ? `${items.length} in cluster…` : undefined}
          className="rp-focus h-9 w-full rounded-skin-sm border border-line bg-inset px-2.5 font-mono text-[12px] text-ink placeholder:text-faint"
        />
        <datalist id={listId}>
          {items.slice(0, 200).map((it) => (
            <option key={`${it.namespace ?? ''}/${it.name}`} value={it.name}>
              {it.namespace ? `${it.namespace}/${it.name}` : it.name}
            </option>
          ))}
        </datalist>
      </div>
    );
  } else
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
