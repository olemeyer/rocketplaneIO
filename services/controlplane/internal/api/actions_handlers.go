package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/auth"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

// actions_handlers.go — the generic operation set over both channels:
//   API (session/token): create an operation run + read the feed.
//   Agent (bearer):      claim pending runs, report results.
// The catalog is gone: operations are kubectl-shaped primitives, classified by
// actions_policy.go and validated below.

// validateOperation checks the generic operation set: target shape + typed
// payload per kind. Nothing unvalidated reaches the cluster. Sizes are capped
// so a runaway agent cannot stuff megabytes into params.
func validateOperation(kind, targetNamespace, targetKind, targetName string, raw json.RawMessage) error {
	if !operationKinds[kind] {
		return errors.New("unsupported operation kind " + kind)
	}
	// Generic for ALL kinds: optional params.timeoutSeconds — callers (UI, MCP)
	// tune the execution timeout per situation; the bounds are hard.
	var p struct {
		TimeoutSeconds *float64        `json:"timeoutSeconds"`
		APIVersion     string          `json:"apiVersion"`
		Resource       string          `json:"resource"`
		Patch          json.RawMessage `json:"patch"`
		Manifest       json.RawMessage `json:"manifest"`
		Selector       string          `json:"selector"`
		Command        []string        `json:"command"`
		Container      string          `json:"container"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return errors.New("params must be a JSON object")
		}
	}
	if p.TimeoutSeconds != nil && (*p.TimeoutSeconds < 10 || *p.TimeoutSeconds > 1800) {
		return errors.New("params.timeoutSeconds must be 10..1800 seconds")
	}
	if len(p.APIVersion) > 253 || len(p.Resource) > 253 || len(p.Selector) > 1024 {
		return errors.New("apiVersion/resource/selector too long")
	}
	needTarget := func() error {
		if targetKind == "" || targetName == "" {
			return errors.New("targetKind and targetName are required")
		}
		return nil
	}
	switch kind {
	case "k8s_get", "k8s_delete":
		return needTarget()
	case "k8s_list":
		if targetKind == "" {
			return errors.New("targetKind is required")
		}
		return nil
	case "k8s_patch":
		if err := needTarget(); err != nil {
			return err
		}
		if len(p.Patch) == 0 {
			return errors.New("k8s_patch requires params.patch (JSON object)")
		}
		if len(p.Patch) > 256*1024 {
			return errors.New("patch too large (max 256KiB)")
		}
		var probe map[string]any
		if err := json.Unmarshal(p.Patch, &probe); err != nil {
			return errors.New("params.patch is not a JSON object")
		}
		return nil
	case "k8s_apply":
		if len(p.Manifest) == 0 {
			return errors.New("k8s_apply requires params.manifest (full object)")
		}
		if len(p.Manifest) > 512*1024 {
			return errors.New("manifest too large (max 512KiB)")
		}
		var m struct {
			APIVersion string `json:"apiVersion"`
			Kind       string `json:"kind"`
			Metadata   struct {
				Name string `json:"name"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(p.Manifest, &m); err != nil || m.APIVersion == "" || m.Kind == "" || m.Metadata.Name == "" {
			return errors.New("params.manifest must be a full object with apiVersion, kind and metadata.name")
		}
		return nil
	case "k8s_exec":
		if targetKind != "Pod" || targetName == "" || targetNamespace == "" || targetNamespace == "-" {
			return errors.New("k8s_exec targets a Pod (targetKind=Pod, namespace + name required)")
		}
		if len(p.Command) == 0 {
			return errors.New("k8s_exec requires params.command (argv array)")
		}
		if len(p.Command) > 64 {
			return errors.New("command too long (max 64 argv entries)")
		}
		for _, a := range p.Command {
			if len(a) > 8*1024 {
				return errors.New("command argument too long")
			}
		}
		return nil
	case "script":
		return nil // validated by the script paths in createActionCore
	}
	return errors.New("unsupported operation kind " + kind)
}

type createActionReq struct {
	Kind            string          `json:"kind"`
	TargetNamespace string          `json:"targetNamespace"`
	TargetKind      string          `json:"targetKind"`
	TargetName      string          `json:"targetName"`
	Params          json.RawMessage `json:"params"`
	// Script runs: a stored definition + named args. The source is SNAPSHOTTED
	// at dispatch — the audit trail shows exactly what ran.
	DefinitionID string            `json:"definitionId"`
	Args         map[string]string `json:"args"`
	// Ad-hoc script: source passed directly instead of a definition. Compiled
	// like in the editor (checkScriptSource) — nothing broken ever runs.
	Source         string            `json:"source"`
	Title          string            `json:"title"`
	TimeoutSeconds int               `json:"timeoutSeconds"`
	ScriptArgs     map[string]string `json:"scriptArgs"`
	// Safe Actions v2 grouping (optional): an explicit groupId appends to that
	// group (manual batch). When unset, the action gets its own group-of-one.
	GroupID string `json:"groupId"`
}

func (s *Server) handleCreateAction(w http.ResponseWriter, r *http.Request) {
	orgID, role, ok := s.resolveOrgRole(w, r)
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
	user, _ := auth.UserFrom(r.Context())
	a, _, status, msg := s.createActionCore(r.Context(), orgID, clusterID, role, user.ID, user.Email, req, nil, nil, clientIP(r))
	if status != 0 {
		writeErr(w, status, msg)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// createActionCore is the ONE path that creates an action run — shared by the
// browser API (handleCreateAction) and the MCP run_action/run_script tools.
// It validates, classifies, RBAC-gates, resolves the group, optionally links
// the run into an MCP transaction (txnID) and parks it as awaiting_approval
// when its level is in gateLevels. Returns (action, level) on success or
// (0-status ⇒ ok; else HTTP-status + message) on failure.
func (s *Server) createActionCore(ctx context.Context, orgID, clusterID uuid.UUID, role string,
	userID uuid.UUID, userEmail string, req createActionReq, txnID *uuid.UUID, gateLevels []string,
	sourceIP string) (*model.Action, string, int, string) {

	if req.Kind == "script" && req.DefinitionID == "" && req.Source != "" {
		// Ad-hoc script: compile like in the editor, then enqueue as a regular
		// script action (destructive, arm-to-fire).
		if len(req.Source) > 64*1024 {
			return nil, "", http.StatusBadRequest, "script source too large (max 64KiB)"
		}
		title := req.Title
		if title == "" {
			title = "adhoc-script"
		}
		if err := checkScriptSource(title, req.Source); err != nil {
			return nil, "", http.StatusBadRequest, "script does not compile: " + err.Error()
		}
		timeout := req.TimeoutSeconds
		if timeout < 30 || timeout > 1800 {
			timeout = 600
		}
		if req.ScriptArgs == nil {
			req.ScriptArgs = map[string]string{}
		}
		params, _ := json.Marshal(map[string]any{
			"source":         req.Source,
			"args":           req.ScriptArgs,
			"timeoutSeconds": timeout,
		})
		req.Params = params
		req.TargetKind = "Script"
		req.TargetName = title
		if req.TargetNamespace == "" {
			req.TargetNamespace = "-"
		}
	} else if req.Kind == "script" {
		defID, err := uuid.Parse(req.DefinitionID)
		if err != nil {
			return nil, "", http.StatusBadRequest, "script actions require a definitionId"
		}
		def, err := s.store.GetActionDefinition(ctx, orgID, defID)
		if err != nil {
			return nil, "", http.StatusNotFound, "action definition not found"
		}
		if req.Args == nil {
			req.Args = map[string]string{}
		}
		// Typed-args validation: nothing untyped reaches the cluster.
		specs, err := parseParamSpecs(def.Params)
		if err != nil {
			return nil, "", http.StatusInternalServerError, "definition has invalid params"
		}
		if err := validateArgs(specs, req.Args); err != nil {
			return nil, "", http.StatusBadRequest, err.Error()
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
		// Generic operations: derive apply targets from the manifest so the run
		// row displays what it touches, then default namespace for
		// cluster-scoped targets.
		if req.Kind == "k8s_apply" && req.TargetName == "" {
			var p struct {
				Manifest struct {
					Kind     string `json:"kind"`
					Metadata struct {
						Name      string `json:"name"`
						Namespace string `json:"namespace"`
					} `json:"metadata"`
				} `json:"manifest"`
			}
			_ = json.Unmarshal(req.Params, &p)
			req.TargetKind, req.TargetName = p.Manifest.Kind, p.Manifest.Metadata.Name
			if req.TargetNamespace == "" {
				req.TargetNamespace = p.Manifest.Metadata.Namespace
			}
		}
		if req.TargetNamespace == "" {
			req.TargetNamespace = "-" // cluster-scoped target (or all-namespaces list)
		}
		if req.Kind == "k8s_list" && req.TargetName == "" {
			req.TargetName = "*"
		}
		if err := validateOperation(req.Kind, req.TargetNamespace, req.TargetKind, req.TargetName, req.Params); err != nil {
			return nil, "", http.StatusBadRequest, err.Error()
		}
	}

	// RBAC on mutation blast-radius: a plain member may run read-level
	// operations (k8s_get/k8s_list) but any cluster-mutating operation
	// (reversible/disruptive/destructive, incl. scripts) requires admin.
	var lvlParams map[string]any
	_ = json.Unmarshal(req.Params, &lvlParams)
	level := actionLevel(req.Kind, req.TargetKind, lvlParams)
	if level != "read" && roleRank(role) < roleRank("admin") {
		return nil, "", http.StatusForbidden, "running " + level + " actions requires admin role — a member can only run read-only diagnostics"
	}

	// Approval gate (MCP transactions): levels in gateLevels are parked as
	// awaiting_approval — never claimable by the agent until a human approves.
	gated := false
	if level != "read" {
		for _, g := range gateLevels {
			if g == level {
				gated = true
				break
			}
		}
	}

	// Safe Actions v2: resolve the group this run belongs to.
	//   transaction (MCP) → fresh group linked to the transaction,
	//   explicit groupId  → append to that group,
	//   otherwise         → group-of-one (via CreateAction).
	sourceKind := "builtin"
	if req.Kind == "script" {
		sourceKind = "script"
	}
	var a *model.Action
	var err error
	if txnID != nil {
		title := req.Kind
		if req.TargetName != "" {
			title = req.Kind + " " + req.TargetName
		}
		g, gerr := s.store.CreateGroup(ctx, clusterID, userID, "mcp", title, nil, "", "")
		if gerr != nil {
			return nil, "", http.StatusInternalServerError, "failed to open action group"
		}
		if lerr := s.store.LinkGroupToTransaction(ctx, g.ID, *txnID); lerr != nil {
			return nil, "", http.StatusInternalServerError, "failed to link action group"
		}
		a, err = s.store.AppendAction(ctx, g.ID, clusterID, userID, req.Kind, req.TargetNamespace, req.TargetKind, req.TargetName, sourceKind, req.Params)
	} else if groupID, perr := uuid.Parse(req.GroupID); perr == nil {
		a, err = s.store.AppendAction(ctx, groupID, clusterID, userID, req.Kind, req.TargetNamespace, req.TargetKind, req.TargetName, sourceKind, req.Params)
	} else {
		a, err = s.store.CreateAction(ctx, clusterID, userID, req.Kind, req.TargetNamespace, req.TargetKind, req.TargetName, req.Params)
	}
	if err != nil {
		return nil, "", http.StatusInternalServerError, "failed to create action"
	}
	if gated {
		// Park BEFORE the dispatch publish below — ClaimPendingActions only
		// takes status='pending', so a parked run is never claimable.
		if err := s.store.ParkActionForApproval(ctx, clusterID, a.ID); err != nil {
			return nil, "", http.StatusInternalServerError, "failed to gate action for approval"
		}
		a.Status = "awaiting_approval"
		go s.notifyApprovalRequested(orgID, clusterID, a, level)
	}
	a.RequestedBy = userEmail
	// Audit every non-read action (the mutation trail SREs and auditors want).
	if level != "read" {
		actorID := userID
		_ = s.store.WriteAudit(ctx, &orgID, &actorID, userEmail, "action.created", "action",
			a.ID.String(), req.Kind+" "+req.TargetKind+"/"+req.TargetName,
			map[string]any{"level": level, "kind": req.Kind, "gated": gated}, sourceIP)
	}
	s.broker.Publish(clusterID, "actions", 0)
	if !gated {
		// dispatch wakes the agent stream: claim at push latency, not poll cadence.
		s.broker.Publish(clusterID, "dispatch", 0)
	}
	return a, level, 0, ""
}

// handleRevertAction — POST /api/orgs/{org}/clusters/{cluster}/actions/{action}/revert
// Undo a succeeded snapshot-substrate run by enqueueing a snapshot_restore action
// with the run's durable capture list — the ONE generic rollback, now user-driven.
func (s *Server) handleRevertAction(w http.ResponseWriter, r *http.Request) {
	orgID, role, ok := s.resolveOrgRole(w, r)
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
	if roleRank(role) < roleRank("admin") {
		writeErr(w, http.StatusForbidden, "reverting a change requires admin role")
		return
	}
	snaps, err := s.store.GetActionSnapshots(r.Context(), actionID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "action not found")
		return
	}
	if len(snaps) == 0 || string(snaps) == "[]" || string(snaps) == "null" {
		writeErr(w, http.StatusBadRequest, "this run captured no snapshot to revert")
		return
	}
	params, _ := json.Marshal(map[string]any{"snapshots": json.RawMessage(snaps)})
	user, _ := auth.UserFrom(r.Context())
	a, err := s.store.CreateAction(r.Context(), clusterID, user.ID, "snapshot_restore", "-", "Revert", "revert", params)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to enqueue revert")
		return
	}
	a.RequestedBy = user.Email
	s.audit(r, &orgID, "action.reverted", "action", actionID.String(), "revert via snapshot list", map[string]any{"revertOf": actionID.String()})
	s.broker.Publish(clusterID, "actions", 0)
	s.broker.Publish(clusterID, "dispatch", 0)
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

// handleAgentActions — GET /api/agent/actions (claimt pending → running; der
// Agent ruft das auf ein dispatch-Signal hin auf, plus seltener Fallback-Poll).
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
		// Revert: inverse Katalog-Action mit Before-Snapshot (nur bei succeeded).
		Revert json.RawMessage `json:"revert"`
		// Snapshot: das VOR der Mutation gestrippte Zielobjekt (generisch).
		Snapshot json.RawMessage `json:"snapshot"`
		// Snapshots: snapshot substrate — a batch of capture entries to APPEND to
		// the run's ordered durable snapshot list (reported the instant each
		// mutation commits, so a crash mid-run leaves a fully-restorable list).
		Snapshots json.RawMessage `json:"snapshots"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Result) > 32_000 {
		req.Result = req.Result[:32_000]
	}
	if len(req.Progress) > 500 {
		req.Progress = req.Progress[:500]
	}
	if len(req.Steps) > 64_000 {
		writeErr(w, http.StatusBadRequest, "steps too large")
		return
	}
	if len(req.Revert) > 300_000 {
		req.Revert = nil // Revert ist optional — zu groß heisst schlicht: keiner
	}
	if len(req.Snapshot) > 300_000 {
		req.Snapshot = nil
	}
	var err2 error
	cancel := false
	switch req.Status {
	case "running":
		// Zwischenstand — die Antwort trägt den Cancel-Wunsch zurück zum
		// Agenten (outbound-only-Rückkanal).
		// v4: a running tick may carry the durable compensation (reported the
		// instant a mutation commits), so a mid-action crash leaves a revertible
		// row for ReapCrashedAgents.
		cancel, err2 = s.store.UpdateActionProgress(r.Context(), clusterID, actionID, req.Progress, req.Steps, req.Revert)
		// snapshot substrate: append any newly-committed captures to the durable
		// ordered list so a crash mid-run is fully restorable.
		if err2 == nil && len(req.Snapshots) > 0 {
			_ = s.store.AppendActionSnapshots(r.Context(), clusterID, actionID, req.Snapshots)
		}
	case "succeeded", "failed", "cancelled":
		err2 = s.store.CompleteAction(r.Context(), clusterID, actionID, req.Status, req.Result, req.Steps, req.Revert)
		if err2 == nil && len(req.Snapshot) > 0 {
			_ = s.store.SetActionSnapshot(r.Context(), clusterID, actionID, req.Snapshot)
		}
		if err2 == nil {
			// Push-latency transaction convergence: a completed run may be the
			// restore (or last member) a cancelling transaction waits for — one
			// cheap drive pass flips it to rolled_back NOW instead of on the next
			// 30s reaper tick.
			go func() {
				ctx, cancelFn := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancelFn()
				s.driveTxnRollbacksNow(ctx)
			}()
		}
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
	if r.URL.Query().Get("force") == "true" {
		// Notausgang: sofort finalisieren, ohne auf den Agenten zu warten —
		// nichts bleibt jemals dauerhaft hängen.
		if err := s.store.ForceCancel(r.Context(), clusterID, actionID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeErr(w, http.StatusNotFound, "action not found or already finished")
				return
			}
			writeErr(w, http.StatusInternalServerError, "failed to force-cancel")
			return
		}
		s.broker.Publish(clusterID, "actions", 0)
		// Dem (evtl. noch lebenden) Agenten trotzdem den Abbruch signalisieren.
		s.broker.PublishData(clusterID, "cancel", fmt.Sprintf(`{"actionId":%q}`, actionID))
		writeJSON(w, http.StatusOK, map[string]any{"status": "cancelled", "forced": true})
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
	if status == "running" {
		// Der Agent führt bereits aus → cancel-Event mit actionId, damit der
		// Rollback SOFORT startet (der Progress-Report-Rückkanal bleibt als
		// Fallback, falls der Stream gerade nicht steht).
		s.broker.PublishData(clusterID, "cancel", fmt.Sprintf(`{"actionId":%q}`, actionID))
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}
