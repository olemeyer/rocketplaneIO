package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/prometheus/promql"
)

// promql_handlers.go — echtes PromQL auf ClickHouse: die eingebettete
// Prometheus-Engine beantwortet query_range/query, dazu das API-Subset, das
// der offizielle CodeMirror-PromQL-Editor für Autocomplete braucht.

func parseTS(v string, def time.Time) time.Time {
	if v == "" {
		return def
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return time.UnixMilli(int64(f * 1000)).UTC()
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t
	}
	return def
}

// handlePromQLRange — GET …/promql/api/v1/query_range (Prometheus-Format).
func (s *Server) handlePromQLRange(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.resolveClusterScope(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	now := time.Now().UTC()
	start := parseTS(q.Get("start"), now.Add(-15*time.Minute))
	end := parseTS(q.Get("end"), now)
	step := 15 * time.Second
	if v := q.Get("step"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			step = time.Duration(f * float64(time.Second))
		} else if d, err := time.ParseDuration(v); err == nil {
			step = d
		}
	}
	res, closeFn, err := s.promql.QueryRange(r.Context(), clusterID, q.Get("query"), start, end, step)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "errorType": "bad_data", "error": err.Error()})
		return
	}
	defer closeFn()
	writePromResult(w, res)
}

// handlePromQLInstant — GET …/promql/api/v1/query.
func (s *Server) handlePromQLInstant(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.resolveClusterScope(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	ts := parseTS(q.Get("time"), time.Now().UTC())
	res, closeFn, err := s.promql.QueryInstant(r.Context(), clusterID, q.Get("query"), ts)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "errorType": "bad_data", "error": err.Error()})
		return
	}
	defer closeFn()
	writePromResult(w, res)
}

// writePromResult serialisiert promql.Result ins Prometheus-HTTP-Format.
func writePromResult(w http.ResponseWriter, res *promql.Result) {
	var data any
	switch v := res.Value.(type) {
	case promql.Matrix:
		out := make([]map[string]any, 0, len(v))
		for _, s := range v {
			vals := make([][2]any, 0, len(s.Floats))
			for _, p := range s.Floats {
				vals = append(vals, [2]any{float64(p.T) / 1000, fmt.Sprintf("%g", p.F)})
			}
			out = append(out, map[string]any{"metric": s.Metric.Map(), "values": vals})
		}
		data = map[string]any{"resultType": "matrix", "result": out}
	case promql.Vector:
		out := make([]map[string]any, 0, len(v))
		for _, s := range v {
			out = append(out, map[string]any{
				"metric": s.Metric.Map(),
				"value":  [2]any{float64(s.T) / 1000, fmt.Sprintf("%g", s.F)},
			})
		}
		data = map[string]any{"resultType": "vector", "result": out}
	case promql.Scalar:
		data = map[string]any{"resultType": "scalar", "result": [2]any{float64(v.T) / 1000, fmt.Sprintf("%g", v.V)}}
	default:
		data = map[string]any{"resultType": "string", "result": ""}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "data": data})
}

/* ── Editor-Support (Prometheus-API-Subset für codemirror-promql) ───────── */

// handlePromQLLabelValues — …/promql/api/v1/label/{label}/values
// __name__ → alle Metriknamen; sonst leere Liste (MVP).
func (s *Server) handlePromQLLabelValues(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.resolveClusterScope(w, r)
	if !ok {
		return
	}
	if r.PathValue("label") == "__name__" {
		names, err := s.promql.MetricNames(r.Context(), clusterID)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"status": "success", "data": []string{}})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "success", "data": names})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "data": []string{
		// die häufigsten Label — echte Values kämen aus einem Attributes-Scan
	}})
}

func (s *Server) handlePromQLLabels(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.resolveClusterScope(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "data": []string{
		"__name__", "service_name", "service_namespace", "http_route", "http_request_method", "http_response_status_code", "le", "node", "workload",
	}})
}

func (s *Server) handlePromQLMetadata(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.resolveClusterScope(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "data": map[string]any{}})
}

func (s *Server) handlePromQLSeries(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.resolveClusterScope(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "data": []any{}})
}
