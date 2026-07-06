package api

import (
	"net/http"
)

// infra_handlers.go — der Infrastructure-Bereich: Nodes (Hardware) + PVCs
// (Storage). Live über das topology-SSE-Signal (der Agent pusht beides im
// selben Sync).

func (s *Server) handleInfra(w http.ResponseWriter, r *http.Request) {
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
	nodes, err := s.store.InfraNodes(r.Context(), clusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load nodes")
		return
	}
	pvcs, err := s.store.InfraPVCs(r.Context(), clusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load pvcs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes, "pvcs": pvcs})
}
