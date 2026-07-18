// approval.ts — guardrail risk levels: each action carries a risk level
// (classified by the backend), so not every action demands the same amount
// of human friction.
//
//   read        → observes only, mutates nothing
//   reversible  → clean auto-rollback (scale-up, restart, config edits …)
//   disruptive  → interrupts running pods, self-heals (evict, undo …)
//   destructive → real blast radius (drain, scale-to-0, NoExecute taint …)
//
// IMPORTANT — security model: these levels are a CLIENT-side UX policy for
// how much friction a human sees per level. The hard, SERVER-side boundary
// is different and sits deeper: every mutation goes through the whitelist +
// param validation + the namespace-scope gate of the control plane.

export type RiskLevel = 'read' | 'reversible' | 'disruptive' | 'destructive';

export const RISK_LEVELS: RiskLevel[] = ['read', 'reversible', 'disruptive', 'destructive'];

export const LEVEL_META: Record<RiskLevel, { label: string; glyph: string; hint: string }> = {
  read: { label: 'read-only', glyph: '◎', hint: 'observes only, changes nothing' },
  reversible: { label: 'reversible', glyph: '↺', hint: 'clean auto-rollback (scale, restart, config)' },
  disruptive: { label: 'disruptive', glyph: '◇', hint: 'interrupts pods, self-heals (evict, undo, cleanup)' },
  destructive: { label: 'destructive', glyph: '△', hint: 'real blast radius (drain, scale-to-0, NoExecute)' },
};

// actionLevelOf mirrors the backend classification for the UI — including the
// param-dependent cases (scale-to-0, node_taint NoExecute) and the new MCP
// kubectl-shaped primitives (k8s_get/list/patch/apply/delete/exec, script).
export function actionLevelOf(kind: string, params?: Record<string, unknown>): RiskLevel {
  switch (kind) {
    // MCP kubectl primitives — read tier
    case 'k8s_get':
    case 'k8s_list':
    // Legacy catalog read kinds
    case 'debug_bundle':
    case 'pod_events':
    case 'rollout_history':
    case 'drain_preview':
    case 'get_resource':
    case 'describe_resource':
    case 'get_secret':
    case 'helm_releases':
    case 'pod_logs':
    case 'list_events':
    case 'net_probe':
      return 'read';

    // MCP kubectl primitives — reversible tier
    case 'k8s_patch':
    case 'k8s_apply':
      return 'reversible';

    // MCP kubectl primitives — depends on target kind
    case 'k8s_delete': {
      // Pod/Job deletes are disruptive (self-heal); anything else is destructive
      const target = String(params?.targetKind ?? params?.kind ?? '');
      return target === 'Pod' || target === 'Job' ? 'disruptive' : 'destructive';
    }

    // MCP kubectl primitives — destructive tier
    case 'k8s_exec':
    case 'script':
      return 'destructive';

    // Legacy catalog — param-dependent cases
    case 'scale': {
      // fail-closed like the backend: unparseable or 0 → destructive
      const n = Number(params?.replicas ?? NaN);
      return Number.isFinite(n) && n > 0 ? 'reversible' : 'destructive';
    }
    case 'node_taint':
      return params?.effect === 'NoExecute' ? 'destructive' : 'reversible';

    // Legacy catalog — destructive tier
    case 'drain':
    case 'expand_pvc':
    case 'patch_resource':
    case 'create_configmap':
    case 'delete_configmap':
    case 'pvc_expand':
    case 'restore_resource':
    case 'snapshot_restore':
    case 'run_debug_pod':
      return 'destructive';

    // Legacy catalog — disruptive tier
    case 'delete_pod':
    case 'evict_pod':
    case 'rollout_undo':
    case 'cleanup_pods':
    case 'cleanup_jobs':
    case 'cronjob_trigger':
    case 'exec_readonly':
    case 'delete_job':
      return 'disruptive';

    default:
      // Unknown kinds default to destructive (fail-closed)
      return 'destructive';
  }
}

// actionCategoryOf mirrors the backend's action-category taxonomy — the ONE
// category taxonomy for the catalog and the runs filter.
export type ActionCategory = 'diagnose' | 'workloads' | 'scaling' | 'config' | 'network' | 'storage' | 'nodes' | 'batch' | 'cleanup' | 'workflows' | 'other';

export const CATEGORY_LABEL: Record<ActionCategory, string> = {
  diagnose: 'diagnose',
  workloads: 'workloads',
  scaling: 'scaling',
  config: 'config & secrets',
  network: 'network',
  storage: 'storage',
  nodes: 'nodes',
  batch: 'batch',
  cleanup: 'cleanup',
  workflows: 'workflows',
  other: 'other',
};

export function actionCategoryOf(kind: string): ActionCategory {
  switch (kind) {
    case 'debug_bundle': case 'pod_events': case 'rollout_history': case 'drain_preview':
    case 'get_resource': case 'describe_resource': case 'get_secret': case 'helm_releases': case 'exec_readonly':
    case 'pod_logs': case 'list_events': case 'run_debug_pod': case 'net_probe':
      return 'diagnose';
    case 'rollout_restart': case 'rollout_undo': case 'rollout_pause': case 'rollout_resume':
    case 'rollout_to_revision': case 'set_image': case 'statefulset_partition': case 'delete_pod': case 'evict_pod':
      return 'workloads';
    case 'scale': case 'hpa_set': case 'hpa_toggle':
      return 'scaling';
    case 'patch_configmap': case 'patch_secret': case 'create_configmap': case 'delete_configmap':
    case 'set_env': case 'set_resources': case 'annotate': case 'set_label':
      return 'config';
    case 'pdb_set': case 'patch_resource':
      return 'network';
    case 'pvc_expand':
      return 'storage';
    case 'cordon': case 'uncordon': case 'drain': case 'node_taint': case 'node_untaint':
      return 'nodes';
    case 'cronjob_trigger': case 'cronjob_suspend': case 'cronjob_resume': case 'delete_job':
      return 'batch';
    case 'cleanup_pods': case 'cleanup_jobs':
      return 'cleanup';
    case 'script': case 'restore_resource':
      return 'workflows';
    default:
      return 'other';
  }
}

export function levelColor(level?: string): string {
  switch (level) {
    case 'destructive':
      return 'var(--rp-node-crit)';
    case 'disruptive':
      return 'var(--rp-node-warn)';
    default:
      return 'var(--rp-ink-faint)';
  }
}
