// Contract-Typen (docs/architecture.md §5). JSON der Control-Plane ist camelCase
// (siehe /api/me: { user, orgs:[{id,name,slug,role,isPersonal}], currentOrgId }).

export type OrgRole = 'owner' | 'admin' | 'member';

export interface User {
  id: string;
  email: string;
  name: string;
  avatarUrl?: string;
}

/** Org wie sie in /api/me zurückkommt (inkl. Rolle des aktuellen Users). */
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
  /** UID des kube-system-Namespace = Cluster-Identität. NULL solange pending. */
  k8sUid?: string | null;
  status: ClusterStatus;
  agentVersion?: string;
  lastSeenAt?: string | null;
  /** Anzahl gesyncter Namespaces (falls die Liste sie mitliefert). */
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

/** Antwort von GET /api/orgs/{org}/clusters/{cluster}. */
export interface ClusterDetail {
  cluster: Cluster;
  namespaces: Namespace[];
}

/** Antwort von POST /api/orgs/{org}/clusters — Klartext-Token genau einmal. */
export interface ConnectClusterResponse {
  cluster: Cluster;
  enrollToken: string;
  installCommand: string;
}

/** Antwort von POST …/{cluster}/reconnect — neuer Enroll-Token + Command. */
export interface ReconnectResponse {
  cluster?: Cluster;
  enrollToken: string;
  installCommand: string;
}

/* ── Service-Map ───────────────────────────────────────────────────────────── */

export type WorkloadHealth = 'healthy' | 'degraded' | 'critical' | 'unknown';

/** Ein Knoten der Service-Map (ein Workload). */
export interface MapNode {
  id: string; // namespace/kind/name
  namespace: string;
  name: string;
  kind: string;
  health: WorkloadHealth;
  podsReady: number;
  podsTotal: number;
  restarts: number;
  /** Container-Image (Quelle der Tech-Auto-Erkennung) */
  image: string;
  /** manueller Icon-Override (simple-icons slug), '' = auto */
  icon: string;
}

/** Eine gerichtete, aggregierte Flow-Kante zwischen zwei Workloads. */
export interface MapEdge {
  from: string; // MapNode.id
  to: string;
  connCount: number;
}

/** Antwort von GET …/clusters/{id}/service-map. */
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
  since?: string; // Go-Duration ("15m") oder RFC3339
  until?: string; // RFC3339 (Brush-Fenster)
  namespace?: string;
  workload?: string;
  workloads?: string[];
  pod?: string;
  minSeverity?: number;
  search?: string;
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
  | 'script';
export type ActionStatus = 'pending' | 'running' | 'succeeded' | 'failed' | 'cancelled';

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
  /** Live-Zeile des laufenden Schritts, z.B. "rollout: 1/3 available". */
  progress: string;
  /** Ablauf-Schritte (trigger → observe → verify), live vom Agenten. */
  steps: ActionStep[];
  /** User hat Abbruch angefordert; Engine rollt zurück → cancelled. */
  cancelRequested: boolean;
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
  /** inkl. "Terminating" — der Pod fährt gerade raus. */
  phase: string;
  ready: boolean;
  restarts: number;
  ip: string;
  firstSeenAt: string;
}

/* ── Action-Definitionen (Custom-Workflows, Starlark) ─────────────────────── */

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

/* ── Infrastruktur (Nodes + PVCs) ─────────────────────────────────────────── */

export interface InfraNode {
  name: string;
  role: string;
  kubeletVersion: string;
  osImage: string;
  arch: string;
  internalIp: string;
  ready: boolean;
  /** cordoned — Node nimmt keine neuen Pods an */
  unschedulable: boolean;
  /** "", "memory", "disk", "pid" (kommasepariert) */
  pressure: string;
  cpuCapacityM: number;
  cpuAllocatableM: number;
  memCapacity: number;
  memAllocatable: number;
  podCapacity: number;
  /** -1 = unbekannt (kubelet-Stats nicht erreichbar) */
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
  /** -1 = unbekannt (nicht gemountet / keine Stats) */
  usedBytes: number;
  mountedBy: string[];
}

export interface InfraResponse {
  nodes: InfraNode[];
  pvcs: InfraPVC[];
}

/* ── Metriken + Alerts ────────────────────────────────────────────────────── */

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
  /** PromQL-Bedingung (kind='promql') */
  query: string;
  /** Snooze: Benachrichtigungen stumm bis zu diesem Zeitpunkt */
  mutedUntil: string | null;
  /** Auto-Remediation: Workflow, der bei firing dispatcht wird */
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

/** Derived Metric: Logs/Spans → benannte Zeitreihe (Better-Stack-Muster). */
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
  /** PromQL-Ausdruck (source='promql') */
  query: string;
  createdAt: string;
}
