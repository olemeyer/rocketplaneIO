package api

import (
	"net/http"
	"regexp"
	"time"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/telemetry"
)

// traces_handlers.go — Traces-Explorer-API: Liste der jüngsten Spans + RED-
// Aggregation je Service (aus Beyla-eBPF-Spans via OTel-Collector in ClickHouse).

// handleQueryTraces bedient GET /api/orgs/{org}/clusters/{cluster}/traces.
// Query-Params: since ("15m"), namespace, service, onlyError, limit.
func (s *Server) handleQueryTraces(w http.ResponseWriter, r *http.Request) {
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
	if _, _, err := s.store.GetClusterWithNamespaces(r.Context(), orgID, clusterID); err != nil {
		writeErr(w, http.StatusNotFound, "cluster not found")
		return
	}

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
	q := telemetry.TracesQuery{
		Namespace: v.Get("namespace"),
		Service:   v.Get("service"),
		OnlyError: v.Get("onlyError") == "true",
		Since:     since,
		Until:     until,
	}

	traces, err := s.tele.QueryTraces(r.Context(), q)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "trace query failed")
		return
	}
	red, err := s.tele.QueryRED(r.Context(), q)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "red query failed")
		return
	}
	// Histogramm immer ungefiltert nach onlyError: Gesamtvolumen + roter Anteil.
	histo, err := s.tele.QueryTraceHistogram(r.Context(), q, 60)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "trace histogram failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"traces": traces, "red": red, "histogram": histo})
}

// traceIDPattern: 32 Hex-Zeichen (OTel-Trace-ID).
var traceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// handleTraceDetail liefert alle Spans eines Traces (Waterfall im Side-Panel):
// GET /api/orgs/{org}/clusters/{cluster}/traces/{traceId}.
func (s *Server) handleTraceDetail(w http.ResponseWriter, r *http.Request) {
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
	if _, _, err := s.store.GetClusterWithNamespaces(r.Context(), orgID, clusterID); err != nil {
		writeErr(w, http.StatusNotFound, "cluster not found")
		return
	}
	traceID := r.PathValue("traceId")
	if !traceIDPattern.MatchString(traceID) {
		writeErr(w, http.StatusBadRequest, "invalid trace id")
		return
	}
	spans, err := s.tele.QueryTraceSpans(r.Context(), traceID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "trace detail query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"traceId": traceID, "spans": spans})
}

// handleSpanStats vergleicht eine Operation über das Zeitfenster:
// GET /api/orgs/{org}/clusters/{cluster}/span-stats?service=X&span=Y&since=1h
func (s *Server) handleSpanStats(w http.ResponseWriter, r *http.Request) {
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
	if _, _, err := s.store.GetClusterWithNamespaces(r.Context(), orgID, clusterID); err != nil {
		writeErr(w, http.StatusNotFound, "cluster not found")
		return
	}
	v := r.URL.Query()
	service, span := v.Get("service"), v.Get("span")
	if service == "" || span == "" {
		writeErr(w, http.StatusBadRequest, "service and span required")
		return
	}
	now := time.Now()
	since := now.Add(-1 * time.Hour)
	if raw := v.Get("since"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			since = now.Add(-d)
		}
	}
	stats, err := s.tele.QuerySpanStats(r.Context(), service, span, since, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "span stats query failed")
		return
	}
	writeJSON(w, http.StatusOK, stats)
}
