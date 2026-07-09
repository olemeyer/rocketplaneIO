// Package model defines the domain types shared across the Control-Plane.
// The JSON tags are part of the public API contract (see docs/architecture.md §5)
// and use camelCase.
package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// User is an authenticated principal (Google SSO or dev-login).
type User struct {
	ID              uuid.UUID  `json:"id"`
	Email           string     `json:"email"`
	Name            string     `json:"name"`
	AvatarURL       string     `json:"avatarUrl"`
	IsPlatformAdmin bool       `json:"isPlatformAdmin"`
	SuspendedAt     *time.Time `json:"suspendedAt,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
}

// Member is a user together with their role in a specific org (Settings > Members).
type Member struct {
	UserID    uuid.UUID `json:"userId"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	AvatarURL string    `json:"avatarUrl"`
	Role      string    `json:"role"` // owner | admin | member
	JoinedAt  time.Time `json:"joinedAt"`
	IsYou     bool      `json:"isYou,omitempty"`
}

// Invitation is a pending (or accepted) invite of an email to an org with a role.
type Invitation struct {
	ID          uuid.UUID  `json:"id"`
	OrgID       uuid.UUID  `json:"orgId"`
	Email       string     `json:"email"`
	Role        string     `json:"role"`
	InvitedBy   string     `json:"invitedBy"` // email of the inviter (display)
	CreatedAt   time.Time  `json:"createdAt"`
	ExpiresAt   time.Time  `json:"expiresAt"`
	AcceptedAt  *time.Time `json:"acceptedAt,omitempty"`
	OrgName     string     `json:"orgName,omitempty"`     // populated on public preview
	Token       string     `json:"token,omitempty"`       // only returned once, at creation
	AcceptURL   string     `json:"acceptUrl,omitempty"`   // convenience for the inviter
}

// AuditEntry is one recorded mutating action (Settings > Audit / admin console).
type AuditEntry struct {
	ID         uuid.UUID       `json:"id"`
	OrgID      *uuid.UUID      `json:"orgId,omitempty"`
	ActorEmail string          `json:"actorEmail"`
	Action     string          `json:"action"`
	TargetType string          `json:"targetType"`
	TargetID   string          `json:"targetId,omitempty"`
	TargetName string          `json:"targetName,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
	CreatedAt  time.Time       `json:"createdAt"`
}

// AdminUser is a user row for the platform-admin console, with org count.
type AdminUser struct {
	User
	OrgCount int `json:"orgCount"`
}

// AdminOrg is an org row for the platform-admin console, with member/cluster counts.
type AdminOrg struct {
	Org
	MemberCount  int `json:"memberCount"`
	ClusterCount int `json:"clusterCount"`
}

// Org is the tenant boundary. Role is only populated when the org is loaded in
// the context of a specific member (e.g. /api/me).
type Org struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	Slug       string    `json:"slug"`
	IsPersonal bool      `json:"isPersonal"`
	Role       string    `json:"role,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Membership links a User to an Org with a role.
type Membership struct {
	ID     uuid.UUID `json:"id"`
	OrgID  uuid.UUID `json:"orgId"`
	UserID uuid.UUID `json:"userId"`
	Role   string    `json:"role"`
}

// Cluster belongs to an Org. Its identity is the UID of the kube-system
// namespace, set once the agent enrolls.
type Cluster struct {
	ID           uuid.UUID  `json:"id"`
	OrgID        uuid.UUID  `json:"orgId"`
	Name         string     `json:"name"`
	K8sUID       string     `json:"k8sUid"`
	Status       string     `json:"status"`
	AgentVersion string     `json:"agentVersion"`
	LastSeenAt   *time.Time `json:"lastSeenAt"`
	CreatedAt    time.Time  `json:"createdAt"`
}

// Namespace belongs to a Cluster and is synced by the agent.
type Namespace struct {
	ID          uuid.UUID         `json:"id"`
	Name        string            `json:"name"`
	K8sUID      string            `json:"k8sUid"`
	Phase       string            `json:"phase"`
	Labels      map[string]string `json:"labels"`
	FirstSeenAt time.Time         `json:"firstSeenAt"`
	LastSeenAt  time.Time         `json:"lastSeenAt"`
}

// ── Topology (Service-Map) ─────────────────────────────────────────────────
// These types are the Agent→Control-Plane sync contract for the service map.

// Pod is a pod snapshot synced by the agent (incl. owner derivation).
type Pod struct {
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	IP           string `json:"ip"`
	Image        string `json:"image"`
	NodeName     string `json:"nodeName"`
	Phase        string `json:"phase"`
	Ready        bool   `json:"ready"`
	Restarts     int    `json:"restarts"`
	WorkloadKind string `json:"workloadKind"`
	WorkloadName string `json:"workloadName"`
}

// K8sService is a synced Kubernetes service.
type K8sService struct {
	Namespace string            `json:"namespace"`
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	ClusterIP string            `json:"clusterIp"`
	Selector  map[string]string `json:"selector"`
}

// FlowEdge is a from → to connection observed by the agent (conntrack) and
// aggregated at the workload level.
type FlowEdge struct {
	FromNamespace string `json:"fromNamespace"`
	FromKind      string `json:"fromKind"`
	FromName      string `json:"fromName"`
	ToNamespace   string `json:"toNamespace"`
	ToKind        string `json:"toKind"`
	ToName        string `json:"toName"`
	ToPort        int    `json:"toPort"`
	ConnCount     int64  `json:"connCount"`
}

// WorkloadSync is a node read directly from the workload object —
// the source of truth for desired/ready, even when scaled-to-zero (no pods).
type WorkloadSync struct {
	Namespace       string `json:"namespace"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	ReplicasDesired int    `json:"replicasDesired"`
	ReplicasReady   int    `json:"replicasReady"`
}

// TopologySync is the agent payload for POST /api/agent/topology.
type TopologySync struct {
	Pods      []Pod          `json:"pods"`
	Services  []K8sService   `json:"services"`
	Edges     []FlowEdge     `json:"edges"`
	Workloads []WorkloadSync `json:"workloads"`
	Nodes     []NodeSync     `json:"nodes"`
	PVCs      []PVCSync      `json:"pvcs"`
}

// MapNode is a node in the service map (a workload).
type MapNode struct {
	ID        string `json:"id"` // namespace/kind/name
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Health    string `json:"health"` // healthy|degraded|critical|unknown
	PodsReady int    `json:"podsReady"`
	PodsTotal int    `json:"podsTotal"`
	Restarts  int    `json:"restarts"`
	// Image = container image (auto tech detection); Icon = manual override.
	Image string `json:"image"`
	Icon  string `json:"icon"`
}

// MapEdge is a directed, aggregated edge between two workloads.
type MapEdge struct {
	From      string `json:"from"` // MapNode.ID
	To        string `json:"to"`
	ConnCount int64  `json:"connCount"`
	// Source distinguishes the edge origin: "trace" = from Beyla eBPF spans
	// (primary, with RED), "flow" = Beyla L4 network flows (protocols without
	// L7 parsing: NATS, ClickHouse-native, …), "conntrack" = last-resort fallback
	// from the kernel conntrack table. Empty for older clients.
	Source string `json:"source,omitempty"`
	// L7 enrichment — only set when Source=="trace".
	Protocol string  `json:"protocol,omitempty"` // http | grpc | <db.system> (postgresql, redis, …)
	ReqRate  float64 `json:"reqRate,omitempty"`  // requests/second over the window
	ErrRate  float64 `json:"errRate,omitempty"`  // error fraction 0..1
	P95Ms    float64 `json:"p95Ms,omitempty"`    // p95 latency in ms
	// L4 enrichment — only set when Source=="flow" (bytes/s over the window).
	BytesRate float64 `json:"bytesRate,omitempty"`
}

// RawFlowEdge is an edge aggregated from Beyla's L4 network flow metric that has
// not yet been validated against the topology. Both sides are already
// kube-decorated by Beyla (owner + namespace) — all that remains is matching
// against known workloads (store.ResolveFlowEdges).
type RawFlowEdge struct {
	SrcNs   string
	SrcName string
	DstNs   string
	DstName string
	Bytes   float64
}

// RawTraceEdge is an edge aggregated from Beyla spans that has NOT YET been
// resolved to workloads. One side is cleanly known (the reporting workload from
// ResourceAttributes), the other is just an address (server.address on
// client spans, client.address on server spans) and gets resolved against the
// topology (store.ResolveTraceEdges).
type RawTraceEdge struct {
	// KnownNs/KnownName: the clean workload side (the span-reporting process).
	KnownNs   string
	KnownName string
	// Peer: the counterpart still to be resolved — service name, FQDN, IP or (on
	// kube-proxy) a node name; the latter resolves to nothing and is dropped.
	Peer string
	// KnownIsClient: true → edge Known→Peer (from a CLIENT span); false →
	// Peer→Known (from a SERVER span, where the counterpart is the caller).
	KnownIsClient bool
	Protocol      string
	Reqs          int64
	Errs          int64
	P95Ms         float64
}

// ServiceMap is the response of GET …/clusters/{id}/service-map.
type ServiceMap struct {
	Namespaces []string  `json:"namespaces"`
	Nodes      []MapNode `json:"nodes"`
	Edges      []MapEdge `json:"edges"`
}

// ── Safe-Actions ───────────────────────────────────────────────────────────
// Kubernetes actions requested by the user; the agent polls and executes them
// (outbound-only — the Control-Plane never has cluster access).

// Action is a Kubernetes action on a workload/pod.
type Action struct {
	ID              uuid.UUID       `json:"id"`
	ClusterID       uuid.UUID       `json:"clusterId"`
	RequestedBy     string          `json:"requestedBy"` // display name/email (resolved)
	Kind            string          `json:"kind"`        // rollout_restart | scale | delete_pod
	TargetNamespace string          `json:"targetNamespace"`
	TargetKind      string          `json:"targetKind"` // Deployment | StatefulSet | DaemonSet | Pod
	TargetName      string          `json:"targetName"`
	Params          json.RawMessage `json:"params"`
	Status          string          `json:"status"` // pending|running|succeeded|failed
	Result          string          `json:"result"`
	// Progress is the live line of the running step ("rollout: 1/3 available"),
	// Steps the state of the whole flow ([{name,status,detail}]).
	Progress        string          `json:"progress"`
	Steps           json.RawMessage `json:"steps"`
	// Revert: the inverse catalog action (filled by the agent with a before
	// snapshot, only when succeeded) — basis for the "Revert" button on the Runs page.
	Revert json.RawMessage `json:"revert,omitempty"`
	// Snapshot: the stripped target object BEFORE the mutation (generic
	// before snapshot) — audit trail + restore_resource basis.
	Snapshot        json.RawMessage `json:"snapshot,omitempty"`
	CancelRequested bool            `json:"cancelRequested"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
}

// WorkloadPod is a pod row for the workload panel (incl. node).
// Phase additionally knows "Terminating" (pod is shutting down); FirstSeenAt lets
// the UI mark fresh pods ("new").
type WorkloadPod struct {
	Namespace   string    `json:"namespace"`
	Name        string    `json:"name"`
	NodeName    string    `json:"nodeName"`
	Phase       string    `json:"phase"`
	Ready       bool      `json:"ready"`
	Restarts    int       `json:"restarts"`
	IP          string    `json:"ip"`
	FirstSeenAt time.Time `json:"firstSeenAt"`
}

// ActionDefinition is an org-wide, reusable Starlark workflow.
type ActionDefinition struct {
	ID             uuid.UUID       `json:"id"`
	OrgID          uuid.UUID       `json:"orgId"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Params         json.RawMessage `json:"params"` // [{name,label,type,default,min,max,options,...}]
	Source         string          `json:"source"`
	TimeoutSeconds int             `json:"timeoutSeconds"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

// Dashboard is an org-wide "dashboard as code": the Perses YAML spec (open
// CNCF standard) is stored raw → 1:1 portable within the Perses ecosystem.
type Dashboard struct {
	ID          uuid.UUID `json:"id"`
	OrgID       uuid.UUID `json:"orgId"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Spec        string    `json:"spec"` // Perses YAML
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// CopilotChat is a saved Copilot session (conversation). Data holds
// the full render state (chat history + tool activities) as JSON.
type CopilotChat struct {
	ID        uuid.UUID       `json:"id"`
	ClusterID uuid.UUID       `json:"clusterId"`
	UserID    uuid.UUID       `json:"userId"`
	Title     string          `json:"title"`
	Summary   string          `json:"summary"`
	Data      json.RawMessage `json:"data,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

// InvestigationNode is a node in the Copilot orchestrator's investigation
// graph: a hypothesis (task JSON for the investigator) and its
// verdict. Branching = parent_id points to an older node.
type InvestigationNode struct {
	ID              uuid.UUID       `json:"id"`
	InvestigationID uuid.UUID       `json:"investigationId"`
	ParentID        *uuid.UUID      `json:"parentId,omitempty"`
	Seq             int             `json:"seq"`
	Kind            string          `json:"kind"` // hypothesis | action | question | conclusion
	Hypothesis      string          `json:"hypothesis"`
	Task            json.RawMessage `json:"task,omitempty"`
	Verdict         json.RawMessage `json:"verdict,omitempty"`
	Status          string          `json:"status"` // pending|running|done|failed|abandoned
	Confidence      *float32        `json:"confidence,omitempty"`
	TokensIn        int             `json:"tokensIn"`
	TokensOut       int             `json:"tokensOut"`
	CreatedAt       time.Time       `json:"createdAt"`
	FinishedAt      *time.Time      `json:"finishedAt,omitempty"`
}

// ── Infrastructure (Nodes + PVCs) ──────────────────────────────────────────

// NodeSync is a cluster node synced by the agent, incl. kubelet stats.
type NodeSync struct {
	Name            string `json:"name"`
	Role            string `json:"role"`
	KubeletVersion  string `json:"kubeletVersion"`
	OSImage         string `json:"osImage"`
	Arch            string `json:"arch"`
	InternalIP      string `json:"internalIp"`
	Ready           bool   `json:"ready"`
	Unschedulable   bool   `json:"unschedulable"`
	Pressure        string `json:"pressure"`
	CPUCapacityM    int64  `json:"cpuCapacityM"`
	CPUAllocatableM int64  `json:"cpuAllocatableM"`
	MemCapacity     int64  `json:"memCapacity"`
	MemAllocatable  int64  `json:"memAllocatable"`
	PodCapacity     int64  `json:"podCapacity"`
	CPUUsageM       int64  `json:"cpuUsageM"`
	MemUsage        int64  `json:"memUsage"`
	FsUsed          int64  `json:"fsUsed"`
	FsCapacity      int64  `json:"fsCapacity"`
	ImageFsUsed     int64  `json:"imageFsUsed"`
	// PodCount is joined CP-side from the pods table (not from the agent).
	PodCount int `json:"podCount"`
}

// PVCSync is a synced PersistentVolumeClaim incl. utilization.
type PVCSync struct {
	Namespace      string   `json:"namespace"`
	Name           string   `json:"name"`
	Phase          string   `json:"phase"`
	StorageClass   string   `json:"storageClass"`
	AccessModes    []string `json:"accessModes"`
	VolumeName     string   `json:"volumeName"`
	RequestedBytes int64    `json:"requestedBytes"`
	CapacityBytes  int64    `json:"capacityBytes"`
	UsedBytes      int64    `json:"usedBytes"`
	MountedBy      []string `json:"mountedBy"`
}

// ── Alerts ─────────────────────────────────────────────────────────────────

// AlertProvider is a delivery channel (org-wide): webhook | slack | email.
type AlertProvider struct {
	ID        uuid.UUID       `json:"id"`
	OrgID     uuid.UUID       `json:"orgId"`
	Name      string          `json:"name"`
	Type      string          `json:"type"`
	Config    json.RawMessage `json:"config"`
	CreatedAt time.Time       `json:"createdAt"`
}

// AlertRule is a typed check (Dash0 pattern: condition + threshold + for).
type AlertRule struct {
	ID            uuid.UUID       `json:"id"`
	ClusterID     uuid.UUID       `json:"clusterId"`
	Name          string          `json:"name"`
	Kind          string          `json:"kind"`
	Params        json.RawMessage `json:"params"`
	Op            string          `json:"op"`
	Threshold     float64         `json:"threshold"`
	WindowSeconds int             `json:"windowSeconds"`
	ForSeconds    int             `json:"forSeconds"`
	Severity      string          `json:"severity"`
	ProviderIDs   []uuid.UUID     `json:"providerIds"`
	Enabled       bool            `json:"enabled"`
	// Query: PromQL condition (kind='promql'); snooze + auto-remediation.
	Query              string          `json:"query"`
	MutedUntil         *time.Time      `json:"mutedUntil"`
	ActionDefinitionID *uuid.UUID      `json:"actionDefinitionId"`
	ActionArgs         json.RawMessage `json:"actionArgs"`
	State         string          `json:"state"`
	StateSince    time.Time       `json:"stateSince"`
	LastValue     float64         `json:"lastValue"`
	LastEvalAt    *time.Time      `json:"lastEvalAt"`
	LastError     string          `json:"lastError"`
	CreatedAt     time.Time       `json:"createdAt"`
}

// AlertEvent is a state transition (ok→pending→firing→ok) in the feed.
type AlertEvent struct {
	ID        uuid.UUID `json:"id"`
	RuleID    uuid.UUID `json:"ruleId"`
	RuleName  string    `json:"ruleName"`
	At        time.Time `json:"at"`
	FromState string    `json:"fromState"`
	ToState   string    `json:"toState"`
	Value     float64   `json:"value"`
	Message   string    `json:"message"`
}

// ── Incidents ──────────────────────────────────────────────────────────────

// Incident is the umbrella over a single event: it ties together alerts, Copilot
// investigations and actions across a lifecycle (open→acknowledged→
// mitigated→resolved). MTTA/MTTR are derivable from the timestamps.
type Incident struct {
	ID             uuid.UUID  `json:"id"`
	OrgID          uuid.UUID  `json:"orgId"`
	ClusterID      uuid.UUID  `json:"clusterId"`
	Number         int        `json:"number"`
	Title          string     `json:"title"`
	Summary        string     `json:"summary"`
	Severity       string     `json:"severity"`
	Status         string     `json:"status"`
	Source         string     `json:"source"`
	DedupKey       *string    `json:"-"`
	AssigneeID     *uuid.UUID `json:"assigneeId,omitempty"`
	AssigneeName   string     `json:"assigneeName,omitempty"`
	AssigneeEmail  string     `json:"assigneeEmail,omitempty"`
	CreatedBy      *uuid.UUID `json:"createdBy,omitempty"`
	CreatedByName  string     `json:"createdByName,omitempty"`
	AcknowledgedAt *time.Time `json:"acknowledgedAt,omitempty"`
	MitigatedAt    *time.Time `json:"mitigatedAt,omitempty"`
	ResolvedAt     *time.Time `json:"resolvedAt,omitempty"`
	Postmortem     string     `json:"postmortem"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	// Escalation (Round 3): assigned policy + next step due.
	EscalationPolicyID *uuid.UUID `json:"escalationPolicyId,omitempty"`
	EscalationStep     int        `json:"escalationStep"`
	NextEscalationAt   *time.Time `json:"nextEscalationAt,omitempty"`
	// Aggregate for the list view (not in the detail query):
	EventCount int `json:"eventCount,omitempty"`
}

// APIToken is programmatic access (service account) to an org.
// Secret is only returned at creation (afterwards only the prefix).
type APIToken struct {
	ID            uuid.UUID  `json:"id"`
	OrgID         uuid.UUID  `json:"orgId"`
	Name          string     `json:"name"`
	Role          string     `json:"role"`
	Prefix        string     `json:"prefix"`
	CreatedBy     *uuid.UUID `json:"createdBy,omitempty"`
	CreatedByName string     `json:"createdByName,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	LastUsedAt    *time.Time `json:"lastUsedAt,omitempty"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
	RevokedAt     *time.Time `json:"revokedAt,omitempty"`
	Status        string     `json:"status"` // active | expired | revoked
	Secret        string     `json:"secret,omitempty"`
}

// EscalationPolicy is an ordered notification chain (org-wide).
type EscalationPolicy struct {
	ID        uuid.UUID        `json:"id"`
	OrgID     uuid.UUID        `json:"orgId"`
	Name      string           `json:"name"`
	Steps     []EscalationStep `json:"steps"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
}

// EscalationStep fires after AfterMinutes via the named providers.
type EscalationStep struct {
	AfterMinutes int         `json:"afterMinutes"`
	ProviderIDs  []uuid.UUID `json:"providerIds"`
}

// IncidentEvent is an entry in the incident timeline.
type IncidentEvent struct {
	ID         uuid.UUID       `json:"id"`
	IncidentID uuid.UUID       `json:"incidentId"`
	At         time.Time       `json:"at"`
	Kind       string          `json:"kind"`
	ActorID    *uuid.UUID      `json:"actorId,omitempty"`
	ActorEmail string          `json:"actorEmail,omitempty"`
	Message    string          `json:"message"`
	RefType    string          `json:"refType,omitempty"`
	RefID      *uuid.UUID      `json:"refId,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

// MetricDefinition is a derived metric: logs/spans → named time series.
type MetricDefinition struct {
	ID          uuid.UUID `json:"id"`
	ClusterID   uuid.UUID `json:"clusterId"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Source      string    `json:"source"`
	Namespace   string    `json:"namespace"`
	Workload    string    `json:"workload"`
	Search      string    `json:"search"`
	ValueMode   string    `json:"valueMode"`
	Pattern     string    `json:"pattern"`
	Agg         string    `json:"agg"`
	Unit        string    `json:"unit"`
	// Query: PromQL expression (source='promql') — recording-rule pattern.
	Query     string    `json:"query"`
	CreatedAt time.Time `json:"createdAt"`
}
