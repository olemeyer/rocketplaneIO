// Contract-Typen (docs/architecture.md §5). JSON der Control-Plane ist camelCase
// (siehe /api/me: { user, orgs:[{id,name,slug,role,isPersonal}], currentOrgId }).

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

/** Install-Wege für den Agenten — die UI lässt zwischen ihnen wählen. */
export interface InstallCommands {
  kubectl: string;
  helm: string;
}

/** Antwort von POST /api/orgs/{org}/clusters — Klartext-Token genau einmal. */
export interface ConnectClusterResponse {
  cluster: Cluster;
  enrollToken: string;
  installCommand: string;
  installCommands?: InstallCommands;
}

/** Antwort von POST …/{cluster}/reconnect — neuer Enroll-Token + Command. */
export interface ReconnectResponse {
  cluster?: Cluster;
  enrollToken: string;
  installCommand: string;
  installCommands?: InstallCommands;
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

/** Eine gerichtete, aggregierte Kante zwischen zwei Workloads. */
export interface MapEdge {
  from: string; // MapNode.id
  to: string;
  connCount: number;
  /** Kantenherkunft: "trace" = eBPF-L7-Spans (mit RED) · "flow" = eBPF-L4-
   *  Netzwerk-Flows (nur Bytes; Protokolle ohne L7-Parsing wie NATS) ·
   *  "conntrack" = Kernel-Fallback. Leer bei alten Control-Planes. */
  source?: 'trace' | 'flow' | 'conntrack';
  protocol?: string; // http | grpc | postgresql | … | tcp (flow)
  reqRate?: number; // req/s über das Fenster (nur trace)
  errRate?: number; // Fehleranteil 0..1 (nur trace)
  p95Ms?: number; // p95-Latenz in ms (nur trace)
  bytesRate?: number; // Bytes/s (nur flow)
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
  regexes?: string[]; // RE2-Pattern (max 5); regexMode kombiniert sie
  regexMode?: 'any' | 'all';
  exclude?: string[]; // NOT-Substrings (max 5)
  fuzzy?: string; // tippfehlertolerante ngram-Suche
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
  /** Inverse Katalog-Action mit Before-Snapshot (nur bei succeeded) — Revert-Button. */
  revert?: { kind: string; targetNamespace: string; targetKind: string; targetName: string; params: Record<string, unknown> };
  /** Gestripptes Zielobjekt VOR der Mutation (generischer Before-Snapshot). */
  snapshot?: Record<string, unknown>;
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

/* ── Incidents ─────────────────────────────────────────────────────────── */

export type IncidentSeverity = 'critical' | 'high' | 'medium' | 'low';
export type IncidentStatus = 'open' | 'acknowledged' | 'mitigated' | 'resolved';
export type IncidentSource = 'manual' | 'alert' | 'copilot';

/** Incident — die Klammer über einen Vorfall (Alerts + Investigations + Actions). */
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

/** Eskalations-Policy: geordnete Notification-Kette (org-weit). */
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

/** Ein Eintrag der Incident-Timeline. */
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
