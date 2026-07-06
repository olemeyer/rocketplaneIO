package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/auth"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

// actions_handlers.go — Safe-Actions über beide Kanäle:
//   Browser (Session): Aktion anlegen + Feed lesen, Workload-Pods listen.
//   Agent (Bearer):    pending claimen, Ergebnis melden.
// Die Whitelist ist bewusst eng — jede Aktion ist ein konkreter, benannter
// kubectl-Handgriff, nichts Generisches.

// actionRules: erlaubte Aktionen je Target-Kind + Params-Validierung.
var actionTargetKinds = map[string]map[string]bool{
	"rollout_restart": {"Deployment": true, "StatefulSet": true, "DaemonSet": true},
	"rollout_undo":    {"Deployment": true},
	"scale":           {"Deployment": true, "StatefulSet": true},
	"delete_pod":      {"Pod": true},
	// Release-Handgriffe des Alltags
	"set_image":      {"Deployment": true, "StatefulSet": true, "DaemonSet": true},
	"rollout_pause":  {"Deployment": true},
	"rollout_resume": {"Deployment": true},
	// Autoscaling (die ehrliche Skalierung in HPA-Clustern)
	"hpa_set": {"HorizontalPodAutoscaler": true},
	// Batch
	"cronjob_trigger": {"CronJob": true},
	"cronjob_suspend": {"CronJob": true},
	"cronjob_resume":  {"CronJob": true},
	// Housekeeping (namespace-scoped: TargetName = Namespace)
	"cleanup_pods": {"Namespace": true},
	// Read-only Investigation-Bündel (kein Mutieren)
	"pod_events":   {"Deployment": true, "StatefulSet": true, "DaemonSet": true, "Pod": true},
	"debug_bundle": {"Deployment": true, "StatefulSet": true, "DaemonSet": true, "Pod": true},
	// Node-Wartung (cluster-scoped — namespace ist "-")
	"cordon":       {"Node": true},
	"uncordon":     {"Node": true},
	"drain":        {"Node": true},
	"node_taint":   {"Node": true},
	"node_untaint": {"Node": true},
}

// validateActionParams prüft die typisierten Params je Kind — nichts
// Unvalidiertes erreicht den Cluster. Kinds ohne Params (rollout_*, cordon,
// cronjob_*, cleanup_pods …) fallen durch auf true.
func validateActionParams(w http.ResponseWriter, kind string, raw json.RawMessage) bool {
	switch kind {
	case "scale":
		var p struct {
			Replicas *int `json:"replicas"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || p.Replicas == nil || *p.Replicas < 0 || *p.Replicas > 50 {
			writeErr(w, http.StatusBadRequest, "scale requires params.replicas in 0..50")
			return false
		}
	case "set_image":
		var p struct {
			Image string `json:"image"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || p.Image == "" || len(p.Image) > 512 {
			writeErr(w, http.StatusBadRequest, "set_image requires params.image")
			return false
		}
	case "hpa_set":
		var p struct {
			MinReplicas *int `json:"minReplicas"`
			MaxReplicas *int `json:"maxReplicas"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || p.MaxReplicas == nil {
			writeErr(w, http.StatusBadRequest, "hpa_set requires params.maxReplicas")
			return false
		}
		min := 1
		if p.MinReplicas != nil {
			min = *p.MinReplicas
		}
		if min < 1 || *p.MaxReplicas < min || *p.MaxReplicas > 200 {
			writeErr(w, http.StatusBadRequest, "hpa_set bounds must satisfy 1 <= min <= max <= 200")
			return false
		}
	case "node_taint":
		var p struct {
			Key    string `json:"key"`
			Effect string `json:"effect"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || p.Key == "" {
			writeErr(w, http.StatusBadRequest, "node_taint requires params.key")
			return false
		}
		switch p.Effect {
		case "NoSchedule", "PreferNoSchedule", "NoExecute":
		default:
			writeErr(w, http.StatusBadRequest, "node_taint effect must be NoSchedule, PreferNoSchedule or NoExecute")
			return false
		}
	case "node_untaint":
		var p struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || p.Key == "" {
			writeErr(w, http.StatusBadRequest, "node_untaint requires params.key")
			return false
		}
	}
	return true
}

type createActionReq struct {
	Kind            string          `json:"kind"`
	TargetNamespace string          `json:"targetNamespace"`
	TargetKind      string          `json:"targetKind"`
	TargetName      string          `json:"targetName"`
	Params          json.RawMessage `json:"params"`
	// Script-Actions: Definition + benannte Argumente. Die Source wird beim
	// Dispatch GESNAPSHOTTET — der Audit-Trail zeigt exakt, was lief.
	DefinitionID string            `json:"definitionId"`
	Args         map[string]string `json:"args"`
}

// handleCreateAction — POST /api/orgs/{org}/clusters/{cluster}/actions
func (s *Server) handleCreateAction(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	if _, _, err := s.store.GetClusterWithNamespaces(r.Context(), orgID, clusterID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "cluster not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load cluster")
		return
	}

	var req createActionReq
	if !decode(w, r, &req) {
		return
	}
	if req.Kind == "script" {
		defID, err := uuid.Parse(req.DefinitionID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "script actions require a definitionId")
			return
		}
		def, err := s.store.GetActionDefinition(r.Context(), orgID, defID)
		if err != nil {
			writeErr(w, http.StatusNotFound, "action definition not found")
			return
		}
		if req.Args == nil {
			req.Args = map[string]string{}
		}
		// Typed-Args-Validierung: nichts Untypisiertes erreicht den Cluster.
		specs, err := parseParamSpecs(def.Params)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "definition has invalid params")
			return
		}
		if err := validateArgs(specs, req.Args); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		params, _ := json.Marshal(map[string]any{
			"definitionId":   def.ID,
			"source":         def.Source,
			"args":           req.Args,
			"timeoutSeconds": def.TimeoutSeconds,
		})
		req.Params = params
		req.TargetKind = "Script"
		req.TargetName = def.Name
		if req.TargetNamespace == "" {
			req.TargetNamespace = "-"
		}
	} else {
		kinds, okKind := actionTargetKinds[req.Kind]
		if !okKind || !kinds[req.TargetKind] {
			writeErr(w, http.StatusBadRequest, "unsupported action/target combination")
			return
		}
		if (req.TargetKind == "Node" || req.TargetKind == "Namespace") && req.TargetNamespace == "" {
			req.TargetNamespace = "-" // cluster- bzw. namespace-scoped Ziele
		}
		if req.TargetNamespace == "" || req.TargetName == "" {
			writeErr(w, http.StatusBadRequest, "targetNamespace and targetName are required")
			return
		}
		if !validateActionParams(w, req.Kind, req.Params) {
			return
		}
	}

	user, _ := auth.UserFrom(r.Context())
	a, err := s.store.CreateAction(r.Context(), clusterID, user.ID, req.Kind, req.TargetNamespace, req.TargetKind, req.TargetName, req.Params)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create action")
		return
	}
	a.RequestedBy = user.Email
	s.broker.Publish(clusterID, "actions", 0)
	writeJSON(w, http.StatusCreated, a)
}

// handleListActions — GET /api/orgs/{org}/clusters/{cluster}/actions?namespace=&target=&limit=
func (s *Server) handleListActions(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	if _, _, err := s.store.GetClusterWithNamespaces(r.Context(), orgID, clusterID); err != nil {
		writeErr(w, http.StatusNotFound, "cluster not found")
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	actions, err := s.store.ListActions(r.Context(), clusterID, q.Get("namespace"), q.Get("target"), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list actions")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": actions})
}

// handleWorkloadPods — GET /api/orgs/{org}/clusters/{cluster}/workload-pods?namespace=&kind=&name=
func (s *Server) handleWorkloadPods(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	if _, _, err := s.store.GetClusterWithNamespaces(r.Context(), orgID, clusterID); err != nil {
		writeErr(w, http.StatusNotFound, "cluster not found")
		return
	}
	q := r.URL.Query()
	pods, err := s.store.ListWorkloadPods(r.Context(), clusterID, q.Get("namespace"), q.Get("kind"), q.Get("name"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list pods")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pods": pods})
}

// handleAgentActions — GET /api/agent/actions (Agent-Poll; claimt pending → running).
func (s *Server) handleAgentActions(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := auth.ClusterIDFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no cluster in context")
		return
	}
	actions, err := s.store.ClaimPendingActions(r.Context(), clusterID, 10)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to claim actions")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": actions})
}

// handleAgentActionResult — POST /api/agent/actions/{action}/result
func (s *Server) handleAgentActionResult(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := auth.ClusterIDFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no cluster in context")
		return
	}
	actionID, err := uuid.Parse(r.PathValue("action"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid action id")
		return
	}
	var req struct {
		Status   string          `json:"status"`
		Result   string          `json:"result"`
		Progress string          `json:"progress"`
		Steps    json.RawMessage `json:"steps"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Result) > 4000 {
		req.Result = req.Result[:4000]
	}
	if len(req.Progress) > 500 {
		req.Progress = req.Progress[:500]
	}
	if len(req.Steps) > 16_000 {
		writeErr(w, http.StatusBadRequest, "steps too large")
		return
	}
	var err2 error
	cancel := false
	switch req.Status {
	case "running":
		// Zwischenstand — die Antwort trägt den Cancel-Wunsch zurück zum
		// Agenten (outbound-only-Rückkanal).
		cancel, err2 = s.store.UpdateActionProgress(r.Context(), clusterID, actionID, req.Progress, req.Steps)
	case "succeeded", "failed", "cancelled":
		err2 = s.store.CompleteAction(r.Context(), clusterID, actionID, req.Status, req.Result, req.Steps)
	default:
		writeErr(w, http.StatusBadRequest, "status must be running, succeeded, failed or cancelled")
		return
	}
	if err2 != nil {
		if errors.Is(err2, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "action not found or not running")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to update action")
		return
	}
	// Progress/Endstatus → Streams; leicht gedrosselt (Reports kommen ~1.2s).
	s.broker.Publish(clusterID, "actions", 400*time.Millisecond)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "cancel": cancel})
}

// handleCancelAction — POST /api/orgs/{org}/clusters/{cluster}/actions/{action}/cancel
func (s *Server) handleCancelAction(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	if _, _, err := s.store.GetClusterWithNamespaces(r.Context(), orgID, clusterID); err != nil {
		writeErr(w, http.StatusNotFound, "cluster not found")
		return
	}
	actionID, err := uuid.Parse(r.PathValue("action"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid action id")
		return
	}
	status, err := s.store.RequestCancel(r.Context(), clusterID, actionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "action not found or already finished")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to cancel")
		return
	}
	s.broker.Publish(clusterID, "actions", 0)
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}
