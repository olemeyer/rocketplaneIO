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

// ── Topologie (Service-Map) ────────────────────────────────────────────────
// Diese Typen sind der Agent→Control-Plane-Sync-Contract für die Service-Map.

// Pod ist eine vom Agent gesyncte Pod-Momentaufnahme (inkl. Owner-Ableitung).
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

// K8sService ist ein gesyncter Kubernetes-Service.
type K8sService struct {
	Namespace string            `json:"namespace"`
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	ClusterIP string            `json:"clusterIp"`
	Selector  map[string]string `json:"selector"`
}

// FlowEdge ist eine vom Agent (conntrack) beobachtete, auf Workload-Ebene
// aggregierte Verbindung from → to.
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

// WorkloadSync ist ein direkt vom Workload-Objekt gelesener Knoten —
// die Wahrheit für desired/ready, auch bei scaled-to-zero (keine Pods).
type WorkloadSync struct {
	Namespace       string `json:"namespace"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	ReplicasDesired int    `json:"replicasDesired"`
	ReplicasReady   int    `json:"replicasReady"`
}

// TopologySync ist der Agent-Payload für POST /api/agent/topology.
type TopologySync struct {
	Pods      []Pod          `json:"pods"`
	Services  []K8sService   `json:"services"`
	Edges     []FlowEdge     `json:"edges"`
	Workloads []WorkloadSync `json:"workloads"`
	Nodes     []NodeSync     `json:"nodes"`
	PVCs      []PVCSync      `json:"pvcs"`
}

// MapNode ist ein Knoten der Service-Map (ein Workload).
type MapNode struct {
	ID        string `json:"id"` // namespace/kind/name
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Health    string `json:"health"` // healthy|degraded|critical|unknown
	PodsReady int    `json:"podsReady"`
	PodsTotal int    `json:"podsTotal"`
	Restarts  int    `json:"restarts"`
	// Image = Container-Image (Auto-Tech-Erkennung); Icon = manueller Override.
	Image string `json:"image"`
	Icon  string `json:"icon"`
}

// MapEdge ist eine gerichtete, aggregierte Kante zwischen zwei Workloads.
type MapEdge struct {
	From      string `json:"from"` // MapNode.ID
	To        string `json:"to"`
	ConnCount int64  `json:"connCount"`
	// Source unterscheidet die Kantenherkunft: "trace" = aus Beyla-eBPF-Spans
	// (primär, mit RED), "flow" = Beyla-L4-Netzwerk-Flows (Protokolle ohne
	// L7-Parsing: NATS, ClickHouse-native, …), "conntrack" = letzter Fallback
	// aus der Kernel-Conntrack-Tabelle. Leer bei Alt-Clients.
	Source string `json:"source,omitempty"`
	// L7-Anreicherung — nur bei Source=="trace" gesetzt.
	Protocol string  `json:"protocol,omitempty"` // http | grpc | <db.system> (postgresql, redis, …)
	ReqRate  float64 `json:"reqRate,omitempty"`  // Requests/Sekunde über das Fenster
	ErrRate  float64 `json:"errRate,omitempty"`  // Fehleranteil 0..1
	P95Ms    float64 `json:"p95Ms,omitempty"`    // p95-Latenz in ms
	// L4-Anreicherung — nur bei Source=="flow" gesetzt (Bytes/s über das Fenster).
	BytesRate float64 `json:"bytesRate,omitempty"`
}

// RawFlowEdge ist eine aus Beylas L4-Netzwerk-Flow-Metrik aggregierte, noch
// nicht gegen die Topologie validierte Kante. Beide Seiten sind bereits von
// Beyla kube-dekoriert (owner + namespace) — es bleibt nur der Abgleich gegen
// bekannte Workloads (store.ResolveFlowEdges).
type RawFlowEdge struct {
	SrcNs   string
	SrcName string
	DstNs   string
	DstName string
	Bytes   float64
}

// RawTraceEdge ist eine aus Beyla-Spans aggregierte, NOCH NICHT auf Workloads
// aufgelöste Kante. Eine Seite ist sauber bekannt (der berichtende Workload aus
// ResourceAttributes), die andere ist nur eine Adresse (server.address bei
// Client-Spans, client.address bei Server-Spans) und wird gegen die Topologie
// aufgelöst (store.ResolveTraceEdges).
type RawTraceEdge struct {
	// KnownNs/KnownName: die saubere Workload-Seite (der Span-berichtende Prozess).
	KnownNs   string
	KnownName string
	// Peer: die noch aufzulösende Gegenseite — Service-Name, FQDN, IP oder (auf
	// kube-proxy) ein Node-Name; Letzteres löst auf nichts auf und fällt weg.
	Peer string
	// KnownIsClient: true → Kante Known→Peer (aus einem CLIENT-Span); false →
	// Peer→Known (aus einem SERVER-Span, die Gegenseite ist der Aufrufer).
	KnownIsClient bool
	Protocol      string
	Reqs          int64
	Errs          int64
	P95Ms         float64
}

// ServiceMap ist die Antwort von GET …/clusters/{id}/service-map.
type ServiceMap struct {
	Namespaces []string  `json:"namespaces"`
	Nodes      []MapNode `json:"nodes"`
	Edges      []MapEdge `json:"edges"`
}

// ── Safe-Actions ───────────────────────────────────────────────────────────
// Vom User angeforderte Kubernetes-Aktionen; der Agent pollt und führt aus
// (outbound-only — die Control-Plane hat nie Cluster-Zugriff).

// Action ist eine Kubernetes-Aktion auf einem Workload/Pod.
type Action struct {
	ID              uuid.UUID       `json:"id"`
	ClusterID       uuid.UUID       `json:"clusterId"`
	RequestedBy     string          `json:"requestedBy"` // Anzeigename/E-Mail (aufgelöst)
	Kind            string          `json:"kind"`        // rollout_restart | scale | delete_pod
	TargetNamespace string          `json:"targetNamespace"`
	TargetKind      string          `json:"targetKind"` // Deployment | StatefulSet | DaemonSet | Pod
	TargetName      string          `json:"targetName"`
	Params          json.RawMessage `json:"params"`
	Status          string          `json:"status"` // pending|running|succeeded|failed
	Result          string          `json:"result"`
	// Progress ist die Live-Zeile des laufenden Schritts („rollout: 1/3 available"),
	// Steps der Zustand des gesamten Ablaufs ([{name,status,detail}]).
	Progress        string          `json:"progress"`
	Steps           json.RawMessage `json:"steps"`
	// Revert: die inverse Katalog-Action (vom Agenten mit Before-Snapshot
	// befüllt, nur bei succeeded) — Grundlage des "Revert"-Buttons der Runs-Seite.
	Revert json.RawMessage `json:"revert,omitempty"`
	// Snapshot: das gestrippte Zielobjekt VOR der Mutation (generischer
	// Before-Snapshot) — Audit-Trail + restore_resource-Grundlage.
	Snapshot        json.RawMessage `json:"snapshot,omitempty"`
	CancelRequested bool            `json:"cancelRequested"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
}

// WorkloadPod ist eine Pod-Zeile für das Workload-Panel (inkl. Node).
// Phase kennt zusätzlich "Terminating" (Pod fährt raus); FirstSeenAt lässt
// die UI frische Pods („new") markieren.
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

// ActionDefinition ist ein org-weiter, wiederverwendbarer Starlark-Workflow.
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

// Dashboard ist ein org-weites „dashboard as code": die Perses-YAML-Spec (offener
// CNCF-Standard) wird roh gespeichert → 1:1 portabel im Perses-Ökosystem.
type Dashboard struct {
	ID          uuid.UUID `json:"id"`
	OrgID       uuid.UUID `json:"orgId"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Spec        string    `json:"spec"` // Perses-YAML
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// CopilotChat ist ein gespeicherter Copilot-Vorgang (Konversation). Data hält
// den kompletten Render-Zustand (Chat-Verlauf + Tool-Aktivitäten) als JSON.
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

// InvestigationNode ist ein Knoten im Investigation-Graph des Copilot-
// Orchestrators: eine Hypothese (Task-JSON für den Investigator) und ihr
// Verdict. Branching = parent_id zeigt auf einen älteren Knoten.
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

// ── Infrastruktur (Nodes + PVCs) ───────────────────────────────────────────

// NodeSync ist ein vom Agent gesyncter Cluster-Node inkl. kubelet-Stats.
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
	// PodCount wird CP-seitig aus der pods-Tabelle gejoint (nicht vom Agent).
	PodCount int `json:"podCount"`
}

// PVCSync ist ein gesyncter PersistentVolumeClaim inkl. Belegung.
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

// AlertProvider ist ein Versandkanal (org-weit): webhook | slack | email.
type AlertProvider struct {
	ID        uuid.UUID       `json:"id"`
	OrgID     uuid.UUID       `json:"orgId"`
	Name      string          `json:"name"`
	Type      string          `json:"type"`
	Config    json.RawMessage `json:"config"`
	CreatedAt time.Time       `json:"createdAt"`
}

// AlertRule ist ein typed Check (Dash0-Muster: Bedingung + Threshold + for).
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
	// Query: PromQL-Bedingung (kind='promql'); Snooze + Auto-Remediation.
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

// AlertEvent ist ein State-Übergang (ok→pending→firing→ok) im Feed.
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

// Incident ist die Klammer über einen Vorfall: verbindet Alerts, Copilot-
// Investigations und Actions über einen Lebenszyklus (open→acknowledged→
// mitigated→resolved). MTTA/MTTR sind aus den Timestamps ableitbar.
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
	// Eskalation (Round 3): zugeordnete Policy + nächster fälliger Schritt.
	EscalationPolicyID *uuid.UUID `json:"escalationPolicyId,omitempty"`
	EscalationStep     int        `json:"escalationStep"`
	NextEscalationAt   *time.Time `json:"nextEscalationAt,omitempty"`
	// Aggregat für die Listenansicht (nicht in der Detail-Query):
	EventCount int `json:"eventCount,omitempty"`
}

// APIToken ist ein programmatischer Zugang (Service-Account) zu einem Org.
// Secret wird nur bei Erstellung zurückgegeben (danach nur der Prefix).
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

// EscalationPolicy ist eine geordnete Notification-Kette (org-weit).
type EscalationPolicy struct {
	ID        uuid.UUID        `json:"id"`
	OrgID     uuid.UUID        `json:"orgId"`
	Name      string           `json:"name"`
	Steps     []EscalationStep `json:"steps"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
}

// EscalationStep feuert nach AfterMinutes über die genannten Provider.
type EscalationStep struct {
	AfterMinutes int         `json:"afterMinutes"`
	ProviderIDs  []uuid.UUID `json:"providerIds"`
}

// IncidentEvent ist ein Eintrag in der Incident-Timeline.
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

// MetricDefinition ist eine Derived Metric: Logs/Spans → benannte Zeitreihe.
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
	// Query: PromQL-Ausdruck (source='promql') — Recording-Rule-Muster.
	Query     string    `json:"query"`
	CreatedAt time.Time `json:"createdAt"`
}
