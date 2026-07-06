package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/auth"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/telemetry"
)

// logs_handlers.go — die Log-Pipeline der Control-Plane: Agent-Ingest (Bearer →
// ClusterId serverseitig gestempelt) und der org-gescopte Logs-Explorer-Query.

// handleAgentLogs nimmt einen Log-Batch des Agents an und schreibt ihn nach
// ClickHouse. Der Agent puffert/batcht; hier wird nur validiert + gestempelt.
func (s *Server) handleAgentLogs(w http.ResponseWriter, r *http.Request) {
	if !s.tele.Enabled() {
		writeErr(w, http.StatusServiceUnavailable, "telemetry store not configured")
		return
	}
	clusterID, _ := auth.ClusterIDFrom(r.Context())
	var body struct {
		Logs []telemetry.LogRecord `json:"logs"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Logs) == 0 {
		s.broker.Publish(clusterID, "logs", 2*time.Second)
		writeJSON(w, http.StatusOK, map[string]int{"accepted": 0})
		return
	}
	if len(body.Logs) > 10000 {
		writeErr(w, http.StatusRequestEntityTooLarge, "batch too large (max 10000)")
		return
	}
	if err := s.tele.InsertLogs(r.Context(), clusterID, body.Logs); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to store logs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"accepted": len(body.Logs)})
}

// handleQueryLogs bedient den Logs-Explorer: Zeitfenster + Filter + Suche.
// Query-Params: since/until (RFC3339 oder relative "15m"), namespace, workload,
// minSeverity (Zahl), search, limit.
func (s *Server) handleQueryLogs(w http.ResponseWriter, r *http.Request) {
	if !s.tele.Enabled() {
		writeErr(w, http.StatusServiceUnavailable, "telemetry store not configured")
		return
	}
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	// Tenancy: Cluster muss zur Org gehören.
	if _, _, err := s.store.GetClusterWithNamespaces(r.Context(), orgID, clusterID); err != nil {
		writeErr(w, http.StatusNotFound, "cluster not found")
		return
	}

	q := parseLogsQuery(r)
	q.ClusterID = clusterID

	lines, err := s.tele.QueryLogs(r.Context(), q)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "log query failed")
		return
	}
	histo, err := s.tele.QueryHistogram(r.Context(), q, 60)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "histogram query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines, "histogram": histo})
}

func parseLogsQuery(r *http.Request) telemetry.LogsQuery {
	v := r.URL.Query()
	now := time.Now()
	since := now.Add(-15 * time.Minute)
	if raw := v.Get("since"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			since = now.Add(-d)
		} else if t, err := time.Parse(time.RFC3339, raw); err == nil {
			since = t
		}
	}
	until := now
	if raw := v.Get("until"); raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			until = t
		}
	}
	minSev := 0
	if raw := v.Get("minSeverity"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 && n <= 24 {
			minSev = n
		}
	}
	limit := 200
	if raw := v.Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	var workloads []string
	if raw := v.Get("workloads"); raw != "" {
		for _, w := range strings.Split(raw, ",") {
			if w = strings.TrimSpace(w); w != "" && len(workloads) < 20 {
				workloads = append(workloads, w)
			}
		}
	}
	return telemetry.LogsQuery{
		Namespace:   v.Get("namespace"),
		Workload:    v.Get("workload"),
		Workloads:   workloads,
		Pod:         v.Get("pod"),
		MinSeverity: uint8(minSev),
		Search:      v.Get("search"),
		Since:       since,
		Until:       until,
		Limit:       limit,
	}
}
