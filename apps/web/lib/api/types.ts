// Contract types (docs/architecture.md §5). The Control-Plane JSON is camelCase
// (see /api/me: { user, orgs:[{id,name,slug,role,isPersonal}], currentOrgId }).

export type OrgRole = 'owner' | 'admin' | 'member';

export interface User {
  id: string;
  email: string;
  name: string;
  avatarUrl?: string;
  isPlatformAdmin?: boolean;
  suspendedAt?: string | null;
  createdAt?: string;
}

/** A member of an org (Settings > Members). */
export interface Member {
  userId: string;
  email: string;
  name: string;
  avatarUrl?: string;
  role: OrgRole;
  joinedAt: string;
  isYou?: boolean;
}

/** An API token / service account for programmatic access. */
export interface APIToken {
  id: string;
  orgId: string;
  name: string;
  role: OrgRole;
  prefix: string;
  createdBy?: string;
  createdByName?: string;
  createdAt: string;
  lastUsedAt?: string;
  expiresAt?: string;
  revokedAt?: string;
  status: 'active' | 'expired' | 'revoked';
  secret?: string; // returned ONCE on creation
}

/** A pending (or accepted) org invitation. */
export interface Invitation {
  id: string;
  orgId: string;
  email: string;
  role: OrgRole;
  invitedBy?: string;
  createdAt: string;
  expiresAt: string;
  acceptedAt?: string | null;
  orgName?: string;
  token?: string; // only returned once, at creation
  acceptUrl?: string;
}

/** One audit-log entry (Settings > Audit / admin console). */
export interface AuditEntry {
  id: string;
  orgId?: string | null;
  actorEmail: string;
  action: string;
  targetType: string;
  targetId?: string;
  targetName?: string;
  metadata?: Record<string, unknown>;
  createdAt: string;
}

/** Platform-admin user directory row. */
export interface AdminUser extends User {
  orgCount: number;
}

/** Platform-admin org directory row. */
export interface AdminOrg {
  id: string;
  name: string;
  slug: string;
  isPersonal: boolean;
  createdAt: string;
  memberCount: number;
  clusterCount: number;
}

/** Org as returned by /api/me (including the current user's role). */
export interface OrgSummary {
  id: string;
  name: string;
  slug: string;
  role: OrgRole;
  isPersonal: boolean;
}

export interface Me {
  user: User;
  orgs: OrgSummary[];
  currentOrgId: string;
}

export type ClusterStatus = 'pending' | 'connected' | 'stale';

export interface Cluster {
  id: string;
  orgId?: string;
  name: string;
  /** UID of the kube-system namespace = cluster identity. NULL while pending. */
  k8sUid?: string | null;
  status: ClusterStatus;
  agentVersion?: string;
  lastSeenAt?: string | null;
  /** Number of synced namespaces (if the list provides them). */
  namespaceCount?: number;
  createdAt?: string;
}

export interface Namespace {
  id: string;
  name: string;
  k8sUid?: string;
  phase: string;
  labels?: Record<string, string>;
  firstSeenAt?: string;
  lastSeenAt?: string;
}

/** Response from GET /api/orgs/{org}/clusters/{cluster}. */
export interface ClusterDetail {
  cluster: Cluster;
  namespaces: Namespace[];
}

/** Install methods for the agent — the UI lets you choose between them. */
export interface InstallCommands {
  kubectl: string;
  helm: string;
}

/** Response from POST /api/orgs/{org}/clusters — plaintext token exactly once. */
export interface ConnectClusterResponse {
  cluster: Cluster;
  enrollToken: string;
  installCommand: string;
  installCommands?: InstallCommands;
}

/** Response from POST …/{cluster}/reconnect — new enroll token + command. */
export interface ReconnectResponse {
  cluster?: Cluster;
  enrollToken: string;
  installCommand: string;
  installCommands?: InstallCommands;
}

/* ── Service-Map ───────────────────────────────────────────────────────────── */

export type WorkloadHealth = 'healthy' | 'degraded' | 'critical' | 'unknown';

/** A node in the service map (one workload). */
export interface MapNode {
  id: string; // namespace/kind/name
  namespace: string;
  name: string;
  kind: string;
  health: WorkloadHealth;
  podsReady: number;
  podsTotal: number;
  restarts: number;
  /** Container image (source of tech auto-detection) */
  image: string;
  /** manual icon override (simple-icons slug), '' = auto */
  icon: string;
}

/** A directed, aggregated edge between two workloads. */
export interface MapEdge {
  from: string; // MapNode.id
  to: string;
  connCount: number;
  /** Edge origin: "trace" = eBPF L7 spans (with RED) · "flow" = eBPF L4
   *  network flows (bytes only; protocols without L7 parsing such as NATS) ·
   *  "conntrack" = kernel fallback. Empty on older Control-Planes. */
  source?: 'trace' | 'flow' | 'conntrack';
  protocol?: string; // http | grpc | postgresql | … | tcp (flow)
  reqRate?: number; // req/s over the window (trace only)
  errRate?: number; // error ratio 0..1 (trace only)
  p95Ms?: number; // p95 latency in ms (trace only)
  bytesRate?: number; // bytes/s (flow only)
}

/** Response from GET …/clusters/{id}/service-map. */
export interface ServiceMap {
  namespaces: string[];
  nodes: MapNode[];
  edges: MapEdge[];
}

/* ── Logs ──────────────────────────────────────────────────────────────────── */

export interface LogLine {
  ts: string;
  namespace: string;
  workloadName: string;
  podName: string;
  containerName: string;
  stream: string;
  severityText: string;
  severityNumber: number;
  body: string;
}

export interface LogBucket {
  ts: string;
  count: number;
  errors: number;
  warns: number;
}

export interface LogsResponse {
  lines: LogLine[];
  histogram: LogBucket[];
}

export interface LogsParams {
  since?: string; // Go duration ("15m") or RFC3339
  until?: string; // RFC3339 (brush window)
  namespace?: string;
  workload?: string;
  workloads?: string[];
  pod?: string;
  minSeverity?: number;
  search?: string;
  regexes?: string[]; // RE2 patterns (max 5); regexMode combines them
  regexMode?: 'any' | 'all';
  exclude?: string[]; // NOT substrings (max 5)
  fuzzy?: string; // typo-tolerant ngram search
  limit?: number;
}

/* ── Traces & RED-Metriken ─────────────────────────────────────────────────── */

export interface TraceRow {
  ts: string;
  traceId: string;
  serviceName: string;
  spanName: string;
  durationMs: number;
  statusCode: string;
  httpStatus: string;
  namespace: string;
  spanCount: number;
  errorCount: number;
}

export interface TraceSpan {
  spanId: string;
  parentSpanId: string;
  serviceName: string;
  spanName: string;
  kind: string;
  startUnixNs: number;
  durationMs: number;
  statusCode: string;
  httpStatus: string;
  namespace: string;
  attributes: Record<string, string>;
  resource: Record<string, string>;
}

export interface SpanStats {
  count: number;
  p50: number;
  p75: number;
  p90: number;
  p95: number;
  p99: number;
  histogram: number[][]; // [lo, hi, height] in ms
}

export interface TraceDetail {
  traceId: string;
  spans: TraceSpan[];
}

export interface REDRow {
  serviceName: string;
  namespace: string;
  requests: number;
  ratePerMin: number;
  errorRatio: number;
  p50Ms: number;
  p95Ms: number;
  p99Ms: number;
}

export interface TracesResponse {
  traces: TraceRow[];
  red: REDRow[];
  histogram: LogBucket[];
}

/* ── Safe-Actions ─────────────────────────────────────────────────────────── */

export type ActionKind =
  | 'rollout_restart'
  | 'rollout_undo'
  | 'scale'
  | 'delete_pod'
  | 'set_image'
  | 'rollout_pause'
  | 'rollout_resume'
  | 'hpa_set'
  | 'cronjob_trigger'
  | 'cronjob_suspend'
  | 'cronjob_resume'
  | 'cleanup_pods'
  | 'pod_events'
  | 'debug_bundle'
  | 'cordon'
  | 'uncordon'
  | 'drain'
  | 'node_taint'
  | 'node_untaint'
  | 'set_resources'
  | 'set_env'
  | 'rollout_to_revision'
  | 'rollout_history'
  | 'statefulset_partition'
  | 'hpa_toggle'
  | 'annotate'
  | 'set_label'
  | 'patch_configmap'
  | 'evict_pod'
  | 'cleanup_jobs'
  | 'drain_preview'
  // Batch 2/3 — professional read + generic mutation coverage
  | 'get_resource'
  | 'describe_resource'
  | 'get_secret'
  | 'helm_releases'
  | 'exec_readonly'
  | 'patch_secret'
  | 'create_configmap'
  | 'delete_configmap'
  | 'pdb_set'
  | 'patch_resource'
  | 'restore_resource'
  | 'pvc_expand'
  | 'delete_job'
  | 'pod_logs'
  | 'list_events'
  | 'net_probe'
  | 'run_debug_pod'
  | 'script';
/** 'awaiting_approval' = parked by the MCP approval gate until a human decides. */
export type ActionStatus = 'pending' | 'awaiting_approval' | 'running' | 'succeeded' | 'failed' | 'cancelled';

export interface ClusterAction {
  id: string;
  clusterId: string;
  requestedBy: string;
  kind: ActionKind;
  targetNamespace: string;
  targetKind: string;
  targetName: string;
  params: Record<string, unknown>;
  status: ActionStatus;
  result: string;
  /** Live line of the running step, e.g. "rollout: 1/3 available". */
  progress: string;
  /** Execution steps (trigger → observe → verify), live from the agent. */
  steps: ActionStep[];
  /** Inverse catalog action with before-snapshot (only when succeeded) — revert button. */
  revert?: { kind: string; targetNamespace: string; targetKind: string; targetName: string; params: Record<string, unknown> };
  /** True when a succeeded run can be undone (legacy inverse OR a durable snapshot list). */
  revertible?: boolean;
  /** Stripped target object BEFORE the mutation (generic before-snapshot). */
  snapshot?: Record<string, unknown>;
  /** User requested cancellation; engine rolls back → cancelled. */
  cancelRequested: boolean;
  /** Safe Actions v2 grouping: the ActionGroup this run belongs to (a lone action
   *  is a group of one). groupSeq orders the run within the group's trace. */
  groupId?: string;
  groupSeq?: number;
  createdAt: string;
  updatedAt: string;
}

export interface ActionStep {
  name: string;
  status: 'pending' | 'running' | 'ok' | 'failed';
  detail: string;
}

export interface WorkloadPod {
  namespace: string;
  name: string;
  nodeName: string;
  /** incl. "Terminating" — the pod is currently shutting down. */
  phase: string;
  ready: boolean;
  restarts: number;
  ip: string;
  firstSeenAt: string;
}

/* ── Action definitions (custom workflows, Starlark) ─────────────────────── */

export interface ActionDefParam {
  name: string;
  label?: string;
  description?: string;
  type?: 'string' | 'int' | 'bool' | 'enum' | 'namespace' | 'workload' | 'node';
  default?: string;
  required?: boolean;
  min?: number;
  max?: number;
  options?: string[];
}

export interface ActionDefinition {
  id: string;
  orgId: string;
  name: string;
  description: string;
  params: ActionDefParam[];
  source: string;
  timeoutSeconds: number;
  createdAt: string;
  updatedAt: string;
}

/* ── Infrastructure (nodes + PVCs) ─────────────────────────────────────────── */

export interface InfraNode {
  name: string;
  role: string;
  kubeletVersion: string;
  osImage: string;
  arch: string;
  internalIp: string;
  ready: boolean;
  /** cordoned — node accepts no new pods */
  unschedulable: boolean;
  /** "", "memory", "disk", "pid" (comma-separated) */
  pressure: string;
  cpuCapacityM: number;
  cpuAllocatableM: number;
  memCapacity: number;
  memAllocatable: number;
  podCapacity: number;
  /** -1 = unknown (kubelet stats unreachable) */
  cpuUsageM: number;
  memUsage: number;
  fsUsed: number;
  fsCapacity: number;
  imageFsUsed: number;
  podCount: number;
}

export interface InfraPVC {
  namespace: string;
  name: string;
  phase: string;
  storageClass: string;
  accessModes: string[];
  volumeName: string;
  requestedBytes: number;
  capacityBytes: number;
  /** -1 = unknown (not mounted / no stats) */
  usedBytes: number;
  mountedBy: string[];
}

export interface InfraResponse {
  nodes: InfraNode[];
  pvcs: InfraPVC[];
}

/* ── Metrics + Alerts ────────────────────────────────────────────────────── */

export interface SeriesPoint {
  t: number; // Unix ms
  v: number;
}
export interface MetricSeries {
  name: string;
  points: SeriesPoint[];
}

export type ProviderType = 'webhook' | 'slack' | 'email';
export interface AlertProvider {
  id: string;
  orgId: string;
  name: string;
  type: ProviderType;
  config: Record<string, string>;
  createdAt: string;
}

export type RuleKind =
  | 'log_errors'
  | 'trace_error_ratio'
  | 'trace_p95_ms'
  | 'node_cpu_pct'
  | 'node_mem_pct'
  | 'node_disk_pct'
  | 'workload_unready'
  | 'derived'
  | 'promql';
export type RuleState = 'ok' | 'pending' | 'firing';

export interface AlertRule {
  id: string;
  clusterId: string;
  name: string;
  kind: RuleKind;
  params: Record<string, string>;
  op: 'gt' | 'lt';
  threshold: number;
  windowSeconds: number;
  forSeconds: number;
  severity: 'warning' | 'critical';
  providerIds: string[];
  enabled: boolean;
  /** PromQL condition (kind='promql') */
  query: string;
  /** Snooze: mute notifications until this timestamp */
  mutedUntil: string | null;
  /** Auto-remediation: workflow dispatched when firing */
  actionDefinitionId: string | null;
  actionArgs: Record<string, string>;
  state: RuleState;
  stateSince: string;
  lastValue: number;
  lastEvalAt: string | null;
  lastError: string;
  createdAt: string;
}

export interface AlertEvent {
  id: string;
  ruleId: string;
  ruleName: string;
  at: string;
  fromState: string;
  toState: string;
  value: number;
  message: string;
}

/* ── Incidents ─────────────────────────────────────────────────────────── */

export type IncidentSeverity = 'critical' | 'high' | 'medium' | 'low';
export type IncidentStatus = 'open' | 'acknowledged' | 'mitigated' | 'resolved';
/** 'copilot' only appears on historical rows (the feature was removed) — render it neutrally. */
export type IncidentSource = 'manual' | 'alert' | 'copilot';

/** Incident — the umbrella over an event (alerts + investigations + actions). */
export interface Incident {
  id: string;
  orgId: string;
  clusterId: string;
  number: number;
  title: string;
  summary: string;
  severity: IncidentSeverity;
  status: IncidentStatus;
  source: IncidentSource;
  assigneeId?: string;
  assigneeName?: string;
  assigneeEmail?: string;
  createdBy?: string;
  createdByName?: string;
  acknowledgedAt?: string;
  mitigatedAt?: string;
  resolvedAt?: string;
  postmortem: string;
  createdAt: string;
  updatedAt: string;
  escalationPolicyId?: string;
  escalationStep: number;
  nextEscalationAt?: string;
  eventCount?: number;
}

/** Escalation policy: ordered notification chain (org-wide). */
export interface EscalationStep {
  afterMinutes: number;
  providerIds: string[];
}
export interface EscalationPolicy {
  id: string;
  orgId: string;
  name: string;
  steps: EscalationStep[];
  createdAt: string;
  updatedAt: string;
}

/** One entry in the incident timeline. */
export interface IncidentEvent {
  id: string;
  incidentId: string;
  at: string;
  kind:
    | 'declared'
    | 'status'
    | 'severity'
    | 'assigned'
    | 'note'
    | 'alert'
    | 'alert_cleared'
    // 'investigation' only appears on historical timelines (feature removed).
    | 'investigation'
    | 'action'
    | 'escalated'
    | 'postmortem';
  actorId?: string;
  actorEmail?: string;
  message: string;
  refType?: string;
  refId?: string;
  metadata?: Record<string, unknown>;
}

export interface IncidentStats {
  open: number;
  unacknowledged: number;
  critical: number;
  mttaSeconds: number;
  mttrSeconds: number;
}

/** A planned maintenance window that suppresses alert notifications + auto-incidents. */
export interface MaintenanceWindow {
  id: string;
  orgId: string;
  clusterId: string;
  title: string;
  scopeNamespace: string;
  startsAt: string;
  endsAt: string;
  createdBy?: string;
  createdByName?: string;
  createdAt: string;
  status: 'active' | 'scheduled' | 'ended';
}

/* ── MCP transactions (external AI agents) ───────────────────────────────── */

/** open → committed | cancelling → rolled_back | rollback_failed */
export type MCPTransactionStatus = 'open' | 'committed' | 'cancelling' | 'rolled_back' | 'rollback_failed';

/**
 * MCPTransaction — the envelope an external AI agent (Claude Code etc.) opens
 * over the remote MCP endpoint before mutating anything. Every read/action is
 * logged under it; commit keeps the changes, cancel or deadline expiry rolls
 * everything back from snapshots.
 */
export interface MCPTransaction {
  id: string;
  orgId: string;
  clusterId: string;
  tokenId?: string;
  tokenName?: string;
  requestedBy: string;
  incidentId?: string;
  title: string;
  status: MCPTransactionStatus;
  closeReason?: 'commit' | 'cancel' | 'expired';
  deadline: string;
  rollbackActionId?: string;
  createdAt: string;
  closedAt?: string;
  updatedAt: string;
  /** Member runs — only populated on the detail endpoint. */
  actions?: ClusterAction[];
}

/**
 * One append-only timeline entry of a transaction. Types:
 * opened | read | action_created | action_result | approval_requested |
 * approval_decided | committed | cancel_requested | rollback_started |
 * rollback_done | rollback_failed | expired
 */
export interface MCPTransactionEvent {
  seq: number;
  txnId: string;
  type: string;
  tool?: string;
  actionId?: string;
  payload?: Record<string, unknown>;
  at: string;
}

/** Org-level MCP policy (defaults are fail-closed: disruptive+destructive gated). */
export interface MCPPolicy {
  approvalLevels: string[];
  defaultTtlSeconds: number;
  maxTtlSeconds: number;
  notifyProviderIds?: string[];
}

/** Derived metric: logs/spans → named time series (Better Stack pattern). */
export interface MetricDefinition {
  id: string;
  clusterId: string;
  name: string;
  description: string;
  source: 'logs' | 'spans' | 'promql';
  namespace: string;
  workload: string;
  search: string;
  valueMode: 'count' | 'regex' | 'duration';
  pattern: string;
  agg: 'rate' | 'avg' | 'sum' | 'max' | 'p50' | 'p95' | 'p99';
  unit: string;
  /** PromQL expression (source='promql') */
  query: string;
  createdAt: string;
}
