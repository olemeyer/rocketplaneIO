package api

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/auth"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/telemetry"
)

// handleTopology nimmt einen Full-Sync von Pods + Services vom Agent entgegen
// (Bearer-Agent-Auth) und leitet daraus die Workloads (Map-Knoten) ab.
func (s *Server) handleTopology(w http.ResponseWriter, r *http.Request) {
	clusterID, _ := auth.ClusterIDFrom(r.Context())
	var body model.TopologySync
	if !decode(w, r, &body) {
		return
	}
	if err := s.store.SyncTopology(r.Context(), clusterID, body); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to sync topology")
		return
	}
	if err := s.store.TouchCluster(r.Context(), clusterID, ""); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to update cluster")
		return
	}
	// Infra-Zeitreihe nach ClickHouse (Nodes: cpu/mem/disk %, Workloads:
	// ready/desired/unready) — die Datenbasis für Metrics-Charts + Alerts.
	pts := []telemetry.InfraPoint{}
	for _, n := range body.Nodes {
		if n.CPUUsageM >= 0 && n.CPUAllocatableM > 0 {
			pts = append(pts, telemetry.InfraPoint{Scope: "node", Name: n.Name, Metric: "cpu_pct", Value: float64(n.CPUUsageM) * 100 / float64(n.CPUAllocatableM)})
		}
		if n.MemUsage >= 0 && n.MemAllocatable > 0 {
			pts = append(pts, telemetry.InfraPoint{Scope: "node", Name: n.Name, Metric: "mem_pct", Value: float64(n.MemUsage) * 100 / float64(n.MemAllocatable)})
		}
		if n.FsUsed >= 0 && n.FsCapacity > 0 {
			pts = append(pts, telemetry.InfraPoint{Scope: "node", Name: n.Name, Metric: "disk_pct", Value: float64(n.FsUsed) * 100 / float64(n.FsCapacity)})
		}
	}
	for _, wl := range body.Workloads {
		pts = append(pts,
			telemetry.InfraPoint{Scope: "workload", Name: wl.Namespace + "/" + wl.Name, Metric: "ready", Value: float64(wl.ReplicasReady)},
			telemetry.InfraPoint{Scope: "workload", Name: wl.Namespace + "/" + wl.Name, Metric: "desired", Value: float64(wl.ReplicasDesired)},
			telemetry.InfraPoint{Scope: "workload", Name: wl.Namespace + "/" + wl.Name, Metric: "unready", Value: float64(wl.ReplicasDesired - wl.ReplicasReady)},
		)
	}
	if err := s.tele.InsertInfraPoints(r.Context(), clusterID, pts); err != nil {
		log.Printf("infra metrics insert failed: %v", err)
	}

	// Topologie hat sich (potenziell) geändert → Browser-Streams anstoßen.
	s.broker.Publish(clusterID, "topology", 800*time.Millisecond)
	writeJSON(w, http.StatusOK, map[string]any{
		"pods":     len(body.Pods),
		"services": len(body.Services),
	})
}

// handleServiceMap liefert die aggregierte Service-Map (Workload-Knoten + Flow-
// Kanten) eines Clusters (Session-Auth, org-scoped).
func (s *Server) handleServiceMap(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	// Mitgliedschaft + Cluster-Zugehörigkeit über den bestehenden Read absichern.
	if _, _, err := s.store.GetClusterWithNamespaces(r.Context(), orgID, clusterID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "cluster not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load cluster")
		return
	}
	m, err := s.store.ServiceMap(r.Context(), clusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to build service map")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// handleSetWorkloadIcon — PUT /api/orgs/{org}/clusters/{cluster}/workload-icon
// Manueller Icon-Override je Workload (leerer icon = zurück zur Auto-Erkennung).
func (s *Server) handleSetWorkloadIcon(w http.ResponseWriter, r *http.Request) {
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
	var req struct {
		Namespace string `json:"namespace"`
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Icon      string `json:"icon"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Namespace == "" || req.Kind == "" || req.Name == "" || len(req.Icon) > 80 {
		writeErr(w, http.StatusBadRequest, "namespace, kind, name required; icon max 80 chars")
		return
	}
	if err := s.store.SetWorkloadIcon(r.Context(), clusterID, req.Namespace, req.Kind, req.Name, req.Icon); err != nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	s.broker.Publish(clusterID, "topology", 0)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
