// Typed contract functions (docs/architecture.md §5). Thin wrapper around apiFetch.
import { apiFetch } from './client';
import type {
  AdminOrg,
  AdminUser,
  AuditEntry,
  Cluster,
  ClusterDetail,
  ConnectClusterResponse,
  Invitation,
  LogsParams,
  LogsResponse,
  Me,
  Member,
  OrgRole,
  OrgSummary,
  ReconnectResponse,
  ServiceMap,
  SpanStats,
  TraceDetail,
  TracesResponse,
} from './types';

const enc = encodeURIComponent;

/* ── Auth / Session ─────────────────────────────────────────────────────── */

export function getMe(): Promise<Me> {
  return apiFetch<Me>('/api/me');
}

export function devLogin(email: string): Promise<unknown> {
  return apiFetch('/auth/dev/login', {
    method: 'POST',
    body: JSON.stringify({ email }),
  });
}

export function logout(): Promise<unknown> {
  return apiFetch('/auth/logout', { method: 'POST' });
}

/** Change the signed-in user's local password (currentPassword blank if none set yet). */
export function changePassword(currentPassword: string, newPassword: string): Promise<unknown> {
  return apiFetch('/api/me/password', {
    method: 'POST',
    body: JSON.stringify({ currentPassword, newPassword }),
  });
}

/* ── Orgs ───────────────────────────────────────────────────────────────── */

export function createOrg(name: string): Promise<OrgSummary> {
  return apiFetch<OrgSummary>('/api/orgs', {
    method: 'POST',
    body: JSON.stringify({ name }),
  });
}

export function switchOrg(orgId: string): Promise<unknown> {
  return apiFetch('/api/session/org', {
    method: 'POST',
    body: JSON.stringify({ orgId }),
  });
}

export function renameOrg(orgId: string, name: string): Promise<unknown> {
  return apiFetch(`/api/orgs/${enc(orgId)}`, { method: 'PATCH', body: JSON.stringify({ name }) });
}
export function deleteOrg(orgId: string): Promise<unknown> {
  return apiFetch(`/api/orgs/${enc(orgId)}`, { method: 'DELETE' });
}
export function transferOwnership(orgId: string, userId: string): Promise<unknown> {
  return apiFetch(`/api/orgs/${enc(orgId)}/transfer`, { method: 'POST', body: JSON.stringify({ userId }) });
}

/* ── API tokens / service accounts ──────────────────────────────────────── */

export function listAPITokens(orgId: string): Promise<{ tokens: import('./types').APIToken[] }> {
  return apiFetch(`/api/orgs/${enc(orgId)}/api-tokens`);
}
export function createAPIToken(
  orgId: string,
  body: { name: string; role: OrgRole; expiresInDays?: number },
): Promise<import('./types').APIToken> {
  return apiFetch(`/api/orgs/${enc(orgId)}/api-tokens`, { method: 'POST', body: JSON.stringify(body) });
}
export function revokeAPIToken(orgId: string, tokenId: string): Promise<unknown> {
  return apiFetch(`/api/orgs/${enc(orgId)}/api-tokens/${enc(tokenId)}/revoke`, { method: 'POST', body: '{}' });
}
export function deleteAPIToken(orgId: string, tokenId: string): Promise<unknown> {
  return apiFetch(`/api/orgs/${enc(orgId)}/api-tokens/${enc(tokenId)}`, { method: 'DELETE' });
}

/* ── Members & invitations ──────────────────────────────────────────────── */

export function listMembers(orgId: string): Promise<{ members: Member[] }> {
  return apiFetch(`/api/orgs/${enc(orgId)}/members`);
}
export function updateMemberRole(orgId: string, userId: string, role: OrgRole): Promise<unknown> {
  return apiFetch(`/api/orgs/${enc(orgId)}/members/${enc(userId)}`, { method: 'PATCH', body: JSON.stringify({ role }) });
}
export function removeMember(orgId: string, userId: string): Promise<unknown> {
  return apiFetch(`/api/orgs/${enc(orgId)}/members/${enc(userId)}`, { method: 'DELETE' });
}
export function listInvitations(orgId: string): Promise<{ invitations: Invitation[] }> {
  return apiFetch(`/api/orgs/${enc(orgId)}/invitations`);
}
export function createInvitation(orgId: string, email: string, role: OrgRole): Promise<{ invitation: Invitation }> {
  return apiFetch(`/api/orgs/${enc(orgId)}/invitations`, { method: 'POST', body: JSON.stringify({ email, role }) });
}
export function revokeInvitation(orgId: string, inviteId: string): Promise<unknown> {
  return apiFetch(`/api/orgs/${enc(orgId)}/invitations/${enc(inviteId)}`, { method: 'DELETE' });
}
export function previewInvitation(token: string): Promise<{ orgName: string; email: string; role: OrgRole; expiresAt: string }> {
  return apiFetch(`/api/invitations/${enc(token)}`);
}
export function acceptInvitation(token: string): Promise<{ orgId: string }> {
  return apiFetch(`/api/invitations/${enc(token)}/accept`, { method: 'POST' });
}
export function listAudit(orgId: string, limit = 100): Promise<{ entries: AuditEntry[] }> {
  return apiFetch(`/api/orgs/${enc(orgId)}/audit?limit=${limit}`);
}

/* ── Platform admin (super admin) ───────────────────────────────────────── */

export function adminStats(): Promise<{ stats: Record<string, number> }> {
  return apiFetch(`/api/admin/stats`);
}
export function adminListUsers(search = '', limit = 200): Promise<{ users: AdminUser[] }> {
  return apiFetch(`/api/admin/users?search=${enc(search)}&limit=${limit}`);
}
export function adminListOrgs(search = '', limit = 200): Promise<{ orgs: AdminOrg[] }> {
  return apiFetch(`/api/admin/orgs?search=${enc(search)}&limit=${limit}`);
}
export function adminSetUserFlags(userId: string, flags: { suspended?: boolean; platformAdmin?: boolean }): Promise<unknown> {
  return apiFetch(`/api/admin/users/${enc(userId)}`, { method: 'PATCH', body: JSON.stringify(flags) });
}

/* ── Clusters ───────────────────────────────────────────────────────────── */

/** List tolerates both `[...]` and `{ clusters:[...] }`. */
export async function listClusters(orgId: string): Promise<Cluster[]> {
  const data = await apiFetch<Cluster[] | { clusters: Cluster[] }>(
    `/api/orgs/${enc(orgId)}/clusters`,
  );
  return Array.isArray(data) ? data : (data.clusters ?? []);
}

export function getCluster(orgId: string, clusterId: string): Promise<ClusterDetail> {
  return apiFetch<ClusterDetail>(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}`);
}

export function connectCluster(orgId: string, name: string): Promise<ConnectClusterResponse> {
  return apiFetch<ConnectClusterResponse>(`/api/orgs/${enc(orgId)}/clusters`, {
    method: 'POST',
    body: JSON.stringify({ name }),
  });
}

export function reconnectCluster(orgId: string, clusterId: string): Promise<ReconnectResponse> {
  return apiFetch<ReconnectResponse>(
    `/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/reconnect`,
    { method: 'POST' },
  );
}

/* ── Service-Map ────────────────────────────────────────────────────────────── */

export function getServiceMap(orgId: string, clusterId: string): Promise<ServiceMap> {
  return apiFetch<ServiceMap>(
    `/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/service-map`,
  );
}

/* ── Logs ───────────────────────────────────────────────────────────────────── */

export function getTraces(
  orgId: string,
  clusterId: string,
  params: { since?: string; until?: string; namespace?: string; service?: string; onlyError?: boolean } = {},
): Promise<TracesResponse> {
  const q = new URLSearchParams();
  if (params.since) q.set('since', params.since);
  if (params.until) q.set('until', params.until);
  if (params.namespace) q.set('namespace', params.namespace);
  if (params.service) q.set('service', params.service);
  if (params.onlyError) q.set('onlyError', 'true');
  const qs = q.toString();
  return apiFetch<TracesResponse>(
    `/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/traces${qs ? `?${qs}` : ''}`,
  );
}

export function getTraceDetail(
  orgId: string,
  clusterId: string,
  traceId: string,
): Promise<TraceDetail> {
  return apiFetch<TraceDetail>(
    `/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/traces/${enc(traceId)}`,
  );
}

export function getSpanStats(
  orgId: string,
  clusterId: string,
  service: string,
  span: string,
  since = '1h',
): Promise<SpanStats> {
  const q = new URLSearchParams({ service, span, since });
  return apiFetch<SpanStats>(
    `/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/span-stats?${q}`,
  );
}

export function getLogs(
  orgId: string,
  clusterId: string,
  params: LogsParams = {},
): Promise<LogsResponse> {
  const q = new URLSearchParams();
  if (params.since) q.set('since', params.since);
  if (params.until) q.set('until', params.until);
  if (params.namespace) q.set('namespace', params.namespace);
  if (params.workload) q.set('workload', params.workload);
  if (params.workloads?.length) q.set('workloads', params.workloads.join(','));
  if (params.pod) q.set('pod', params.pod);
  if (params.minSeverity) q.set('minSeverity', String(params.minSeverity));
  if (params.search) q.set('search', params.search);
  for (const rx of params.regexes ?? []) q.append('regex', rx);
  if (params.regexMode) q.set('regexMode', params.regexMode);
  for (const ex of params.exclude ?? []) q.append('exclude', ex);
  if (params.fuzzy) q.set('fuzzy', params.fuzzy);
  if (params.limit) q.set('limit', String(params.limit));
  const qs = q.toString();
  return apiFetch<LogsResponse>(
    `/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/logs${qs ? `?${qs}` : ''}`,
  );
}

/* ── Safe-Actions ─────────────────────────────────────────────────────────── */

export function getWorkloadPods(
  orgId: string,
  clusterId: string,
  namespace: string,
  kind: string,
  name: string,
): Promise<{ pods: import('./types').WorkloadPod[] }> {
  const q = new URLSearchParams({ namespace, kind, name });
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/workload-pods?${q}`);
}

export function getActions(
  orgId: string,
  clusterId: string,
  namespace = '',
  target = '',
  limit = 20,
): Promise<{ actions: import('./types').ClusterAction[] }> {
  const q = new URLSearchParams({ namespace, target, limit: String(limit) });
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/actions?${q}`);
}

export function createAction(
  orgId: string,
  clusterId: string,
  body: {
    kind: import('./types').ActionKind;
    targetNamespace: string;
    targetKind: string;
    targetName: string;
    params?: Record<string, unknown>;
  },
): Promise<import('./types').ClusterAction> {
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/actions`, {
    method: 'POST',
    body: JSON.stringify(body),
  });
}

/* ── Action definitions ───────────────────────────────────────────────────── */

export function getActionDefinitions(
  orgId: string,
): Promise<{ definitions: import('./types').ActionDefinition[] }> {
  return apiFetch(`/api/orgs/${enc(orgId)}/action-definitions`);
}

export function createActionDefinition(
  orgId: string,
  body: { name: string; description: string; params: import('./types').ActionDefParam[]; source: string; timeoutSeconds?: number },
): Promise<import('./types').ActionDefinition> {
  return apiFetch(`/api/orgs/${enc(orgId)}/action-definitions`, {
    method: 'POST',
    body: JSON.stringify(body),
  });
}

export function updateActionDefinition(
  orgId: string,
  defId: string,
  body: { name: string; description: string; params: import('./types').ActionDefParam[]; source: string; timeoutSeconds?: number },
): Promise<import('./types').ActionDefinition> {
  return apiFetch(`/api/orgs/${enc(orgId)}/action-definitions/${enc(defId)}`, {
    method: 'PUT',
    body: JSON.stringify(body),
  });
}

export function deleteActionDefinition(orgId: string, defId: string): Promise<unknown> {
  return apiFetch(`/api/orgs/${enc(orgId)}/action-definitions/${enc(defId)}`, {
    method: 'DELETE',
  });
}

export function runScriptAction(
  orgId: string,
  clusterId: string,
  definitionId: string,
  args: Record<string, string>,
): Promise<import('./types').ClusterAction> {
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/actions`, {
    method: 'POST',
    body: JSON.stringify({ kind: 'script', definitionId, args }),
  });
}

export function cancelAction(
  orgId: string,
  clusterId: string,
  actionId: string,
  force = false,
): Promise<{ status: string }> {
  const suffix = force ? '?force=true' : '';
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/actions/${enc(actionId)}/cancel${suffix}`, {
    method: 'POST',
    body: '{}',
  });
}

// revertAction undoes a succeeded snapshot-substrate run: the control plane
// enqueues a snapshot_restore action from the run's durable capture list.
export function revertAction(
  orgId: string,
  clusterId: string,
  actionId: string,
): Promise<import('./types').ClusterAction> {
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/actions/${enc(actionId)}/revert`, {
    method: 'POST',
    body: '{}',
  });
}

// checkWorkflow compiles a Starlark workflow (parse + resolve, no execution)
// against the agent surface — live editor validation. Returns {ok, error?}.
export function checkWorkflow(
  orgId: string,
  source: string,
): Promise<{ ok: boolean; error?: string }> {
  return apiFetch(`/api/orgs/${enc(orgId)}/action-definitions/check`, {
    method: 'POST',
    body: JSON.stringify({ source }),
  });
}

export function getInfra(
  orgId: string,
  clusterId: string,
): Promise<import('./types').InfraResponse> {
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/infra`);
}

export function setWorkloadIcon(
  orgId: string,
  clusterId: string,
  body: { namespace: string; kind: string; name: string; icon: string },
): Promise<unknown> {
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/workload-icon`, {
    method: 'PUT',
    body: JSON.stringify(body),
  });
}

/* ── Metrics + Alerts ─────────────────────────────────────────────────────── */

export function getMetricSeries(
  orgId: string,
  clusterId: string,
  params: Record<string, string>,
): Promise<{ series: import('./types').MetricSeries[] }> {
  const q = new URLSearchParams(params);
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/metrics/series?${q}`);
}

export function getAlertProviders(orgId: string): Promise<{ providers: import('./types').AlertProvider[] }> {
  return apiFetch(`/api/orgs/${enc(orgId)}/alert-providers`);
}
export function createAlertProvider(
  orgId: string,
  body: { name: string; type: import('./types').ProviderType; config: Record<string, string> },
): Promise<import('./types').AlertProvider> {
  return apiFetch(`/api/orgs/${enc(orgId)}/alert-providers`, { method: 'POST', body: JSON.stringify(body) });
}
export function deleteAlertProvider(orgId: string, id: string): Promise<unknown> {
  return apiFetch(`/api/orgs/${enc(orgId)}/alert-providers/${enc(id)}`, { method: 'DELETE' });
}
export function testAlertProvider(orgId: string, id: string): Promise<unknown> {
  return apiFetch(`/api/orgs/${enc(orgId)}/alert-providers/${enc(id)}/test`, { method: 'POST', body: '{}' });
}

export function getAlertRules(orgId: string, clusterId: string): Promise<{ rules: import('./types').AlertRule[] }> {
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/alert-rules`);
}
export function createAlertRule(
  orgId: string,
  clusterId: string,
  body: Partial<import('./types').AlertRule>,
): Promise<import('./types').AlertRule> {
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/alert-rules`, {
    method: 'POST',
    body: JSON.stringify(body),
  });
}
export function updateAlertRule(
  orgId: string,
  clusterId: string,
  ruleId: string,
  body: Partial<import('./types').AlertRule>,
): Promise<import('./types').AlertRule> {
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/alert-rules/${enc(ruleId)}`, {
    method: 'PUT',
    body: JSON.stringify(body),
  });
}
export function deleteAlertRule(orgId: string, clusterId: string, ruleId: string): Promise<unknown> {
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/alert-rules/${enc(ruleId)}`, {
    method: 'DELETE',
  });
}
/** Snooze: minutes > 0 = mute for n minutes, 0 = unmute. */
export function muteAlertRule(orgId: string, clusterId: string, ruleId: string, minutes: number): Promise<unknown> {
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/alert-rules/${enc(ruleId)}/mute`, {
    method: 'POST',
    body: JSON.stringify({ minutes }),
  });
}
/** Value history of the evaluator (sparkline on the rule card). */
export function getAlertRuleSeries(
  orgId: string,
  clusterId: string,
  ruleId: string,
): Promise<{ series: import('./types').MetricSeries[] }> {
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/alert-rules/${enc(ruleId)}/series`);
}
export function getAlertEvents(
  orgId: string,
  clusterId: string,
  limit = 50,
): Promise<{ events: import('./types').AlertEvent[] }> {
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/alert-events?limit=${limit}`);
}

/* ── Incidents ─────────────────────────────────────────────────────────── */

type IncidentT = import('./types').Incident;
type IncidentEventT = import('./types').IncidentEvent;

function incBase(orgId: string, clusterId: string): string {
  return `/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/incidents`;
}

export function getIncidents(
  orgId: string,
  clusterId: string,
  status = '',
  limit = 100,
): Promise<{ incidents: IncidentT[] }> {
  const q = new URLSearchParams();
  if (status) q.set('status', status);
  q.set('limit', String(limit));
  return apiFetch(`${incBase(orgId, clusterId)}?${q.toString()}`);
}

export function getIncidentStats(
  orgId: string,
  clusterId: string,
): Promise<{ stats: import('./types').IncidentStats }> {
  return apiFetch(`${incBase(orgId, clusterId)}/stats`);
}

export function getIncident(
  orgId: string,
  clusterId: string,
  id: string,
): Promise<{ incident: IncidentT; timeline: IncidentEventT[] }> {
  return apiFetch(`${incBase(orgId, clusterId)}/${enc(id)}`);
}

export function createIncident(
  orgId: string,
  clusterId: string,
  body: { title: string; summary?: string; severity?: string },
): Promise<IncidentT> {
  return apiFetch(incBase(orgId, clusterId), { method: 'POST', body: JSON.stringify(body) });
}

export function updateIncident(
  orgId: string,
  clusterId: string,
  id: string,
  body: { title?: string; summary?: string; severity?: string },
): Promise<IncidentT> {
  return apiFetch(`${incBase(orgId, clusterId)}/${enc(id)}`, { method: 'PATCH', body: JSON.stringify(body) });
}

export function setIncidentStatus(
  orgId: string,
  clusterId: string,
  id: string,
  status: string,
): Promise<IncidentT> {
  return apiFetch(`${incBase(orgId, clusterId)}/${enc(id)}/status`, {
    method: 'POST',
    body: JSON.stringify({ status }),
  });
}

export function assignIncident(
  orgId: string,
  clusterId: string,
  id: string,
  userId: string | null,
): Promise<IncidentT> {
  return apiFetch(`${incBase(orgId, clusterId)}/${enc(id)}/assign`, {
    method: 'POST',
    body: JSON.stringify({ userId }),
  });
}

export function addIncidentNote(
  orgId: string,
  clusterId: string,
  id: string,
  note: string,
): Promise<{ timeline: IncidentEventT[] }> {
  return apiFetch(`${incBase(orgId, clusterId)}/${enc(id)}/notes`, {
    method: 'POST',
    body: JSON.stringify({ note }),
  });
}

export function setIncidentPostmortem(
  orgId: string,
  clusterId: string,
  id: string,
  text: string,
): Promise<IncidentT> {
  return apiFetch(`${incBase(orgId, clusterId)}/${enc(id)}/postmortem`, {
    method: 'PUT',
    body: JSON.stringify({ text }),
  });
}

export function linkInvestigation(
  orgId: string,
  clusterId: string,
  id: string,
  chatId: string,
): Promise<{ timeline: IncidentEventT[] }> {
  return apiFetch(`${incBase(orgId, clusterId)}/${enc(id)}/link-investigation`, {
    method: 'POST',
    body: JSON.stringify({ chatId }),
  });
}

/* ── Maintenance windows ───────────────────────────────────────────────── */

export function getMaintenanceWindows(
  orgId: string,
  clusterId: string,
): Promise<{ windows: import('./types').MaintenanceWindow[] }> {
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/maintenance`);
}
export function createMaintenanceWindow(
  orgId: string,
  clusterId: string,
  body: { title: string; scopeNamespace: string; startsAt: string; endsAt: string },
): Promise<import('./types').MaintenanceWindow> {
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/maintenance`, {
    method: 'POST',
    body: JSON.stringify(body),
  });
}
export function deleteMaintenanceWindow(orgId: string, clusterId: string, id: string): Promise<unknown> {
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/maintenance/${enc(id)}`, { method: 'DELETE' });
}

/* ── Escalation policies ───────────────────────────────────────────────── */

type EscalationPolicyT = import('./types').EscalationPolicy;
type EscalationStepT = import('./types').EscalationStep;

export function getEscalationPolicies(orgId: string): Promise<{ policies: EscalationPolicyT[] }> {
  return apiFetch(`/api/orgs/${enc(orgId)}/escalation-policies`);
}
export function createEscalationPolicy(
  orgId: string,
  body: { name: string; steps: EscalationStepT[] },
): Promise<EscalationPolicyT> {
  return apiFetch(`/api/orgs/${enc(orgId)}/escalation-policies`, { method: 'POST', body: JSON.stringify(body) });
}
export function updateEscalationPolicy(
  orgId: string,
  policyId: string,
  body: { name: string; steps: EscalationStepT[] },
): Promise<EscalationPolicyT> {
  return apiFetch(`/api/orgs/${enc(orgId)}/escalation-policies/${enc(policyId)}`, {
    method: 'PUT',
    body: JSON.stringify(body),
  });
}
export function deleteEscalationPolicy(orgId: string, policyId: string): Promise<unknown> {
  return apiFetch(`/api/orgs/${enc(orgId)}/escalation-policies/${enc(policyId)}`, { method: 'DELETE' });
}
export function getClusterEscalation(
  orgId: string,
  clusterId: string,
): Promise<{ policyId: string | null }> {
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/escalation`);
}
export function setClusterEscalation(
  orgId: string,
  clusterId: string,
  policyId: string | null,
): Promise<{ policyId: string | null }> {
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/escalation`, {
    method: 'PUT',
    body: JSON.stringify({ policyId }),
  });
}

/* ── Derived Metrics ──────────────────────────────────────────────────────── */

export function getMetricDefinitions(
  orgId: string,
  clusterId: string,
): Promise<{ definitions: import('./types').MetricDefinition[] }> {
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/metrics/definitions`);
}
export function createMetricDefinition(
  orgId: string,
  clusterId: string,
  body: Partial<import('./types').MetricDefinition>,
): Promise<import('./types').MetricDefinition> {
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/metrics/definitions`, {
    method: 'POST',
    body: JSON.stringify(body),
  });
}
export function updateMetricDefinition(
  orgId: string,
  clusterId: string,
  defId: string,
  body: Partial<import('./types').MetricDefinition>,
): Promise<import('./types').MetricDefinition> {
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/metrics/definitions/${enc(defId)}`, {
    method: 'PUT',
    body: JSON.stringify(body),
  });
}
export function deleteMetricDefinition(orgId: string, clusterId: string, defId: string): Promise<unknown> {
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/metrics/definitions/${enc(defId)}`, {
    method: 'DELETE',
  });
}
export function previewMetricDefinition(
  orgId: string,
  clusterId: string,
  body: Partial<import('./types').MetricDefinition>,
): Promise<{ series: import('./types').MetricSeries[]; samples: { body: string; value: string }[] | null }> {
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/metrics/preview`, {
    method: 'POST',
    body: JSON.stringify(body),
  });
}
export function getDerivedSeries(
  orgId: string,
  clusterId: string,
  defId: string,
  since?: string,
): Promise<{ series: import('./types').MetricSeries[] }> {
  const q = new URLSearchParams({ id: defId });
  if (since) q.set('since', since);
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/metrics/derived?${q}`);
}

export interface InventoryKind {
  kind: string;
  items: { namespace?: string; name: string; createdAt: string; info: Record<string, string> }[];
  updatedAt: string;
}

export function getInventory(orgId: string, clusterId: string, kind = ''): Promise<{ kinds: InventoryKind[] }> {
  const q = kind ? `?kind=${enc(kind)}` : '';
  return apiFetch(`/api/orgs/${enc(orgId)}/clusters/${enc(clusterId)}/inventory${q}`);
}
