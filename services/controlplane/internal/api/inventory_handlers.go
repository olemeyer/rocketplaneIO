package api

import (
	"encoding/json"
	"net/http"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/auth"
)

// inventory_handlers.go — K8s-Inventar: Agent pusht (Bearer), Browser liest
// (Session). Generisches Item-Format; Secrets sind hier NUR Metadaten.

// handleAgentInventory — POST /api/agent/inventory {kinds:{Kind:[items]}}
func (s *Server) handleAgentInventory(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := auth.ClusterIDFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no cluster in context")
		return
	}
	var req struct {
		Kinds map[string]json.RawMessage `json:"kinds"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Kinds) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	for kind, items := range req.Kinds {
		if len(kind) > 64 || len(items) > 2<<20 {
			writeErr(w, http.StatusBadRequest, "inventory kind too large")
			return
		}
	}
	if err := s.store.UpsertInventory(r.Context(), clusterID, req.Kinds); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to store inventory")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleListInventory — GET /api/orgs/{org}/clusters/{cluster}/inventory?kind=
func (s *Server) handleListInventory(w http.ResponseWriter, r *http.Request) {
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
	kinds, err := s.store.ListInventory(r.Context(), clusterID, r.URL.Query().Get("kind"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list inventory")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"kinds": kinds})
}
