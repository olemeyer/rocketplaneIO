package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/auth"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

// ── Browser API ────────────────────────────────────────────────────────────

// handleMe returns the current user, their orgs and the active org id.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	orgID, _ := auth.OrgIDFrom(r.Context())

	orgs, err := s.store.ListOrgsForUser(r.Context(), user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load orgs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user":         user,
		"orgs":         orgs,
		"currentOrgId": orgID,
	})
}

// handleCreateOrg creates a new org owned by the caller.
func (s *Server) handleCreateOrg(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name required")
		return
	}
	org, err := s.store.CreateOrg(r.Context(), body.Name, user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create org")
		return
	}
	writeJSON(w, http.StatusCreated, org)
}

// handleSetOrg switches the active org (updates the session cookie).
func (s *Server) handleSetOrg(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	var body struct {
		OrgID string `json:"orgId"`
	}
	if !decode(w, r, &body) {
		return
	}
	orgID, err := uuid.Parse(strings.TrimSpace(body.OrgID))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid orgId")
		return
	}
	member, err := s.store.IsMember(r.Context(), user.ID, orgID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to check membership")
		return
	}
	if !member {
		writeErr(w, http.StatusForbidden, "not a member of this org")
		return
	}
	if err := s.auth.SetSession(w, user.ID, orgID); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to update session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"currentOrgId": orgID})
}

// handleListClusters lists the clusters of an org.
func (s *Server) handleListClusters(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	clusters, err := s.store.ListClusters(r.Context(), orgID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list clusters")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"clusters": clusters})
}

// handleCreateCluster creates a pending cluster + enroll token and returns the
// install command.
func (s *Server) handleCreateCluster(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeErr(w, http.StatusBadRequest, "name required")
		return
	}
	cluster, token, err := s.store.CreateClusterWithEnrollToken(r.Context(), orgID, name, user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create cluster")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"cluster":        cluster,
		"enrollToken":    token,
		"installCommand": s.installCommand(token, cluster.Name),
	})
}

// handleGetCluster returns a cluster and its namespaces.
func (s *Server) handleGetCluster(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	cluster, namespaces, err := s.store.GetClusterWithNamespaces(r.Context(), orgID, clusterID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "cluster not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load cluster")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cluster":    cluster,
		"namespaces": namespaces,
	})
}

// handleReconnect issues a fresh enroll token for an existing cluster.
func (s *Server) handleReconnect(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	cluster, _, err := s.store.GetClusterWithNamespaces(r.Context(), orgID, clusterID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "cluster not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load cluster")
		return
	}
	token, err := s.store.NewEnrollToken(r.Context(), orgID, clusterID, user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create enroll token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cluster":        cluster,
		"enrollToken":    token,
		"installCommand": s.installCommand(token, cluster.Name),
	})
}

// ── Agent API ──────────────────────────────────────────────────────────────

// handleEnroll redeems an enroll token and returns the long-lived agent token.
func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EnrollToken  string `json:"enrollToken"`
		K8sUID       string `json:"k8sUid"`
		ClusterName  string `json:"clusterName"`
		AgentVersion string `json:"agentVersion"`
	}
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.EnrollToken) == "" || strings.TrimSpace(body.K8sUID) == "" {
		writeErr(w, http.StatusBadRequest, "enrollToken and k8sUid required")
		return
	}
	clusterID, agentToken, err := s.store.RedeemEnrollToken(r.Context(), body.EnrollToken, body.K8sUID, body.ClusterName, body.AgentVersion)
	if err != nil {
		if errors.Is(err, store.ErrInvalidToken) {
			writeErr(w, http.StatusUnauthorized, "invalid or expired enroll token")
			return
		}
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"clusterId":  clusterID,
		"agentToken": agentToken,
	})
}

// handleHeartbeat updates the cluster's liveness.
func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	clusterID, _ := auth.ClusterIDFrom(r.Context())
	var body struct {
		AgentVersion string `json:"agentVersion"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.store.TouchCluster(r.Context(), clusterID, body.AgentVersion); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to record heartbeat")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleNamespaces performs a full namespace sync for the calling cluster.
func (s *Server) handleNamespaces(w http.ResponseWriter, r *http.Request) {
	clusterID, _ := auth.ClusterIDFrom(r.Context())
	var body struct {
		Namespaces []struct {
			Name   string            `json:"name"`
			UID    string            `json:"uid"`
			Phase  string            `json:"phase"`
			Labels map[string]string `json:"labels"`
		} `json:"namespaces"`
	}
	if !decode(w, r, &body) {
		return
	}
	namespaces := make([]model.Namespace, 0, len(body.Namespaces))
	for _, ns := range body.Namespaces {
		if strings.TrimSpace(ns.Name) == "" {
			continue
		}
		namespaces = append(namespaces, model.Namespace{
			Name:   ns.Name,
			K8sUID: ns.UID,
			Phase:  ns.Phase,
			Labels: ns.Labels,
		})
	}
	if err := s.store.SyncNamespaces(r.Context(), clusterID, namespaces); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to sync namespaces")
		return
	}
	// A namespace sync also proves liveness.
	if err := s.store.TouchCluster(r.Context(), clusterID, ""); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to update cluster")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"synced": len(namespaces)})
}

// ── Helpers ────────────────────────────────────────────────────────────────

// resolveOrg resolves the {org} path value (uuid or slug), verifies membership
// and returns the org id. On failure it writes the response and returns false.
func (s *Server) resolveOrg(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	user, _ := auth.UserFrom(r.Context())
	param := r.PathValue("org")

	var org *model.Org
	var err error
	if id, perr := uuid.Parse(param); perr == nil {
		org, err = s.store.GetOrgByID(r.Context(), id)
	} else {
		org, err = s.store.GetOrgBySlug(r.Context(), param)
	}
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "org not found")
			return uuid.Nil, false
		}
		writeErr(w, http.StatusInternalServerError, "failed to resolve org")
		return uuid.Nil, false
	}

	member, err := s.store.IsMember(r.Context(), user.ID, org.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to check membership")
		return uuid.Nil, false
	}
	if !member {
		writeErr(w, http.StatusForbidden, "not a member of this org")
		return uuid.Nil, false
	}
	return org.ID, true
}

// parseClusterID parses the {cluster} path value as a uuid.
func parseClusterID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("cluster"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid cluster id")
		return uuid.Nil, false
	}
	return id, true
}

// installCommand renders the install command shown in the UI. Both the method
// (helm vs. kubectl) and every parameter (image, chart, agent-facing URL) come
// from config, so the SAME UI command works in prod (Helm+ghcr) and locally
// (kubectl + a manifest this server renders itself + host.minikube.internal).
func (s *Server) installCommand(token, clusterName string) string {
	if s.cfg.AgentInstallMethod == "kubectl" {
		// Ein einziger Befehl: kubectl (auf dem Laptop) holt das fertig gerenderte
		// Manifest von der Control-Plane (PublicURL, für den Laptop erreichbar) und
		// wendet es auf den aktuellen kubectl-Context an. Token + Cluster-Name sind
		// bereits eingesetzt; das Manifest selbst zeigt auf AgentControlPlaneURL.
		q := url.Values{"token": {token}, "name": {clusterName}}
		return fmt.Sprintf("kubectl apply -f %q", s.cfg.PublicURL+"/api/agent/manifest?"+q.Encode())
	}
	return fmt.Sprintf(
		"helm install rocketplane-agent %s "+
			"--namespace rocketplane --create-namespace "+
			"--set controlplane.url=%s --set enrollToken=%s --set clusterName=%s",
		s.cfg.AgentChart, s.cfg.AgentControlPlaneURL, token, clusterName)
}
