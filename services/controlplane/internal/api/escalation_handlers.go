package api

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

// escalation_handlers.go — Eskalations-Policies (org-weit) + Cluster-Default.
// Lesen erfordert Org-Mitgliedschaft, Mutationen die Admin-Rolle (wie Alert-
// Provider, da org-weite Config). Der Cluster-Default gate't auf Cluster-Admin.

type escalationPolicyReq struct {
	Name  string                 `json:"name"`
	Steps []model.EscalationStep `json:"steps"`
}

// validatePolicySteps stellt sicher, dass alle referenzierten Provider zum Org
// gehören (kein Cross-Org-Leak) und die Schritte plausibel sind.
func (s *Server) validatePolicySteps(r *http.Request, orgID uuid.UUID, steps []model.EscalationStep) (bool, string) {
	if len(steps) > 20 {
		return false, "too many steps (max 20)"
	}
	provs, err := s.store.ListAlertProviders(r.Context(), orgID)
	if err != nil {
		return false, "failed to load providers"
	}
	valid := map[uuid.UUID]bool{}
	for _, p := range provs {
		valid[p.ID] = true
	}
	for _, st := range steps {
		for _, pid := range st.ProviderIDs {
			if !valid[pid] {
				return false, "unknown provider in step"
			}
		}
	}
	return true, ""
}

func (s *Server) handleListEscalationPolicies(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	policies, err := s.store.ListEscalationPolicies(r.Context(), orgID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list policies")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policies": policies})
}

func (s *Server) handleCreateEscalationPolicy(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := s.requireRole(w, r, "admin")
	if !ok {
		return
	}
	var req escalationPolicyReq
	if !decode(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if ok, msg := s.validatePolicySteps(r, orgID, req.Steps); !ok {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	p, err := s.store.CreateEscalationPolicy(r.Context(), orgID, req.Name, req.Steps)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create policy")
		return
	}
	s.audit(r, &orgID, "escalation.create", "escalation_policy", p.ID.String(), p.Name, nil)
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleUpdateEscalationPolicy(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := s.requireRole(w, r, "admin")
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("policy"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid policy id")
		return
	}
	var req escalationPolicyReq
	if !decode(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if ok, msg := s.validatePolicySteps(r, orgID, req.Steps); !ok {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	p, err := s.store.UpdateEscalationPolicy(r.Context(), orgID, id, req.Name, req.Steps)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "policy not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to update policy")
		return
	}
	s.audit(r, &orgID, "escalation.update", "escalation_policy", p.ID.String(), p.Name, nil)
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleDeleteEscalationPolicy(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := s.requireRole(w, r, "admin")
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("policy"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid policy id")
		return
	}
	if err := s.store.DeleteEscalationPolicy(r.Context(), orgID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "policy not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to delete policy")
		return
	}
	s.audit(r, &orgID, "escalation.delete", "escalation_policy", id.String(), "", nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleGetClusterEscalation — GET …/clusters/{cluster}/escalation {policyId}
func (s *Server) handleGetClusterEscalation(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.resolveClusterScope(w, r)
	if !ok {
		return
	}
	pid, err := s.store.GetClusterEscalationPolicy(r.Context(), clusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load setting")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policyId": pid})
}

// handleSetClusterEscalation — PUT …/clusters/{cluster}/escalation {policyId|null}
func (s *Server) handleSetClusterEscalation(w http.ResponseWriter, r *http.Request) {
	orgID, clusterID, ok := s.requireClusterRole(w, r, "admin")
	if !ok {
		return
	}
	var req struct {
		PolicyID *string `json:"policyId"`
	}
	if !decode(w, r, &req) {
		return
	}
	var pid *uuid.UUID
	if req.PolicyID != nil && *req.PolicyID != "" {
		id, err := uuid.Parse(*req.PolicyID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid policyId")
			return
		}
		// Policy muss zum selben Org gehören.
		if _, err := s.store.GetEscalationPolicy(r.Context(), orgID, id); err != nil {
			writeErr(w, http.StatusBadRequest, "policy not found in this org")
			return
		}
		pid = &id
	}
	if err := s.store.SetClusterEscalationPolicy(r.Context(), clusterID, pid); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to save setting")
		return
	}
	s.broker.Publish(clusterID, "incidents", 0)
	writeJSON(w, http.StatusOK, map[string]any{"policyId": pid})
}
