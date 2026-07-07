package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"

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
	// Trace-Kanten (Beyla-eBPF) sind die PRIMÄRE Quelle; die conntrack-Kanten aus
	// der ServiceMap sind der Fallback. Best-effort: fehlt/klemmt ClickHouse, bleibt
	// es bei der conntrack-Map — die Map darf daran nie scheitern.
	m.Edges = s.enrichWithTraceEdges(r.Context(), clusterID, m.Edges)
	writeJSON(w, http.StatusOK, m)
}

// serviceMapWindow ist das Fenster, über das Trace-Kanten und ihre RED-Werte
// aggregiert werden. Bewusst großzügig (15 min), damit die Map auf Clustern mit
// stoßweisem/sparsamem Traffic nicht flackert; die RED-Rate ist ein Mittel darüber.
const serviceMapWindow = 15 * time.Minute

// enrichWithTraceEdges baut die Kanten dreistufig: L7-Trace-Kanten (RED) gewinnen,
// Beyla-L4-Flow-Kanten füllen Paare ohne L7-Sicht (nicht parsebare Protokolle wie
// NATS/ClickHouse-native), conntrack bleibt letzter Fallback. Jede Stufe ist
// best-effort: fehlt/klemmt ClickHouse, bleibt die vorige Stufe — die Map darf
// daran nie scheitern.
func (s *Server) enrichWithTraceEdges(ctx context.Context, clusterID uuid.UUID, conntrack []model.MapEdge) []model.MapEdge {
	if s.cfg.ClickHouseURL == "" {
		return conntrack
	}
	since := time.Now().Add(-serviceMapWindow)
	winSecs := serviceMapWindow.Seconds()

	var traceEdges []model.MapEdge
	if raw, err := s.tele.ServiceGraphEdges(ctx, since); err != nil {
		log.Printf("service-map: trace edges unavailable: %v", err)
	} else if traceEdges, err = s.store.ResolveTraceEdges(ctx, clusterID, raw, winSecs); err != nil {
		log.Printf("service-map: resolve trace edges failed: %v", err)
		traceEdges = nil
	}

	var flowEdges []model.MapEdge
	if raw, err := s.tele.FlowEdges(ctx, since); err != nil {
		log.Printf("service-map: flow edges unavailable: %v", err)
	} else if flowEdges, err = s.store.ResolveFlowEdges(ctx, clusterID, raw, winSecs); err != nil {
		log.Printf("service-map: resolve flow edges failed: %v", err)
		flowEdges = nil
	}

	if len(traceEdges) == 0 && len(flowEdges) == 0 {
		return conntrack
	}

	out := make([]model.MapEdge, 0, len(traceEdges)+len(flowEdges)+len(conntrack))
	seen := make(map[string]bool, len(traceEdges)+len(flowEdges))
	for _, e := range traceEdges {
		out = append(out, e)
		seen[e.From+"\x00"+e.To] = true
	}
	for _, e := range flowEdges {
		if seen[e.From+"\x00"+e.To] {
			continue // L7-Kante gewinnt (hat RED statt nur Bytes)
		}
		out = append(out, e)
		seen[e.From+"\x00"+e.To] = true
	}
	for _, e := range conntrack {
		if seen[e.From+"\x00"+e.To] {
			continue
		}
		e.Source = "conntrack"
		out = append(out, e)
	}
	return out
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
