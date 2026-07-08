// approval.ts — Guardrails: Risk-Level je Action (vom Backend klassifiziert) und
// der pro Level EINSTELLBARE Approval-Modus. So denkt ein Admin: nicht jede
// Action ist gleich gefährlich, also verlangt nicht jede dieselbe Freigabe.
//
//   read        → nur beobachten, mutiert nichts
//   reversible  → sauberer Auto-Rollback (scale-up, restart, config edits …)
//   disruptive  → unterbricht laufende Pods, heilt sich selbst (evict, undo …)
//   destructive → echter Blast-Radius (drain, scale-to-0, NoExecute-taint …)
//
//   auto    → läuft ohne Nachfrage
//   click   → ein Button-Klick
//   confirm → Ziel-Namen eintippen (arm-to-fire)
//   off     → diese Stufe wird nicht angeboten (dismissed)
//
// WICHTIG — Sicherheitsmodell: der Approval-MODUS ist eine CLIENT-seitige UX-
// Policy darüber, wie viel Reibung der Mensch pro Stufe sieht. Die harte,
// SERVER-seitige Grenze ist eine andere und liegt tiefer: das LLM kann nichts
// selbst ausführen (die eigentliche Ausführung braucht eine authentifizierte
// Freigabe über den Decision-Endpoint), und jede Mutation läuft durch die
// Whitelist + Param-Validierung + das Namespace-Scope-Gate der Control-Plane.
// „off"/„confirm" begrenzen also die Autonomie des Assistenten in der UI, sind
// aber keine Autorisierungskontrolle gegen einen bereits berechtigten Nutzer.

export type RiskLevel = 'read' | 'reversible' | 'disruptive' | 'destructive';
export type ApprovalMode = 'auto' | 'click' | 'confirm' | 'off';
export type ApprovalPolicy = Record<RiskLevel, ApprovalMode>;

export const RISK_LEVELS: RiskLevel[] = ['read', 'reversible', 'disruptive', 'destructive'];
export const APPROVAL_MODES: ApprovalMode[] = ['auto', 'click', 'confirm', 'off'];

export const DEFAULT_POLICY: ApprovalPolicy = {
  read: 'auto',
  reversible: 'click',
  disruptive: 'click',
  destructive: 'confirm',
};

export const LEVEL_META: Record<RiskLevel, { label: string; glyph: string; hint: string }> = {
  read: { label: 'read-only', glyph: '◎', hint: 'observes only, changes nothing' },
  reversible: { label: 'reversible', glyph: '↺', hint: 'clean auto-rollback (scale, restart, config)' },
  disruptive: { label: 'disruptive', glyph: '◇', hint: 'interrupts pods, self-heals (evict, undo, cleanup)' },
  destructive: { label: 'destructive', glyph: '△', hint: 'real blast radius (drain, scale-to-0, NoExecute)' },
};

const KEY = (cluster: string) => `rp-approval-${cluster}`;

export function loadApprovalPolicy(clusterId: string): ApprovalPolicy {
  if (typeof window === 'undefined') return DEFAULT_POLICY;
  try {
    const raw = window.localStorage.getItem(KEY(clusterId));
    if (!raw) return DEFAULT_POLICY;
    const p = JSON.parse(raw) as Partial<ApprovalPolicy>;
    return { ...DEFAULT_POLICY, ...p };
  } catch {
    return DEFAULT_POLICY;
  }
}

export function saveApprovalPolicy(clusterId: string, p: ApprovalPolicy): void {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(KEY(clusterId), JSON.stringify(p));
  } catch {
    /* quota */
  }
}

// actionLevelOf spiegelt die Backend-Klassifizierung (copilot_policy.go) für die
// UI — inkl. der param-abhängigen Fälle (scale-to-0, node_taint NoExecute).
export function actionLevelOf(kind: string, params?: Record<string, unknown>): RiskLevel {
  switch (kind) {
    case 'debug_bundle':
    case 'pod_events':
    case 'rollout_history':
    case 'drain_preview':
    case 'get_resource':
    case 'describe_resource':
    case 'get_secret':
    case 'helm_releases':
      return 'read';
    case 'scale': {
      // fail-closed wie das Backend: nicht parsebar oder 0 → destructive
      const n = Number(params?.replicas ?? NaN);
      return Number.isFinite(n) && n > 0 ? 'reversible' : 'destructive';
    }
    case 'node_taint':
      return params?.effect === 'NoExecute' ? 'destructive' : 'reversible';
    case 'drain':
    case 'expand_pvc':
    case 'patch_resource':
    case 'create_configmap':
    case 'delete_configmap':
    case 'pvc_expand':
    case 'restore_resource':
    case 'script':
      return 'destructive';
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
      return 'reversible';
  }
}

// actionCategoryOf spiegelt copilot_policy.go:actionCategory — die EINE
// Kategorie-Taxonomie für Katalog, Runs-Filter und Copilot.
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
