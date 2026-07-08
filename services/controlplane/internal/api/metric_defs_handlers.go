package api

import (
	"errors"
	"net/http"
	"regexp"
	"time"

	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/prometheus/prometheus/promql"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/promqlx"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/telemetry"
)

// metric_defs_handlers.go — Derived Metrics: CRUD mit SAVE-ZEIT-VERIFIKATION
// (Regex kompiliert + Probe-Query läuft), Live-Preview für den Editor und die
// Serien-Auslieferung für Charts.

var mdefNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)

func derivedOf(d *model.MetricDefinition) telemetry.DerivedDef {
	return telemetry.DerivedDef{
		Source: d.Source, Namespace: d.Namespace, Workload: d.Workload,
		Search: d.Search, ValueMode: d.ValueMode, Pattern: d.Pattern, Agg: d.Agg,
	}
}

// validateMDef prüft Struktur + KOMPILIERT die Definition gegen ClickHouse —
// kaputte Metriken kann man nicht speichern.
func (s *Server) validateMDef(w http.ResponseWriter, r *http.Request, clusterID uuid.UUID, d *model.MetricDefinition) bool {
	if !mdefNamePattern.MatchString(d.Name) {
		writeErr(w, http.StatusBadRequest, "name must be kebab-case")
		return false
	}
	if d.Source == "promql" {
		// PromQL-Metrik: Ausdruck parsen + Probe-Ausführung — kaputte Queries
		// kann man nicht speichern.
		if err := promqlx.Check(d.Query); err != nil {
			writeErr(w, http.StatusBadRequest, "query does not parse: "+err.Error())
			return false
		}
		until := time.Now().UTC()
		res, closeFn, err := s.promql.QueryRange(r.Context(), clusterID, d.Query, until.Add(-5*time.Minute), until, 30*time.Second)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "query does not evaluate: "+err.Error())
			return false
		}
		_ = res
		closeFn()
		return true
	}
	if d.Source != "logs" && d.Source != "spans" {
		writeErr(w, http.StatusBadRequest, "source must be logs, spans or promql")
		return false
	}
	validModes := map[string]map[string]bool{"logs": {"count": true, "regex": true}, "spans": {"count": true, "duration": true}}
	if !validModes[d.Source][d.ValueMode] {
		writeErr(w, http.StatusBadRequest, "invalid value mode for source")
		return false
	}
	if d.ValueMode == "count" {
		if d.Agg != "rate" && d.Agg != "sum" {
			d.Agg = "rate"
		}
	} else {
		valid := map[string]bool{"avg": true, "sum": true, "max": true, "p50": true, "p95": true, "p99": true}
		if !valid[d.Agg] {
			writeErr(w, http.StatusBadRequest, "agg must be avg|sum|max|p50|p95|p99 for value metrics")
			return false
		}
	}
	if d.ValueMode == "regex" {
		re, err := regexp.Compile(d.Pattern)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "pattern does not compile: "+err.Error())
			return false
		}
		if re.NumSubexp() < 1 {
			writeErr(w, http.StatusBadRequest, "pattern needs one capture group for the numeric value, e.g. (\\d+)")
			return false
		}
	}
	// Probe-Query: die Definition MUSS ausführbar sein (ClickHouse-Regex-Dialekt).
	until := time.Now().UTC()
	if _, err := s.tele.QueryDerivedSeries(r.Context(), clusterID, derivedOf(d), until.Add(-5*time.Minute), until); err != nil {
		writeErr(w, http.StatusBadRequest, "definition does not evaluate: "+err.Error())
		return false
	}
	return true
}

func (s *Server) handleListMetricDefs(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.resolveClusterScope(w, r)
	if !ok {
		return
	}
	defs, err := s.store.ListMetricDefinitions(r.Context(), clusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list definitions")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"definitions": defs})
}

func (s *Server) handleCreateMetricDef(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.requireClusterRole(w, r, "admin")
	if !ok {
		return
	}
	var d model.MetricDefinition
	if !decode(w, r, &d) {
		return
	}
	d.ClusterID = clusterID
	if !s.validateMDef(w, r, clusterID, &d) {
		return
	}
	out, err := s.store.CreateMetricDefinition(r.Context(), &d)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create (name taken?)")
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleUpdateMetricDef(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.requireClusterRole(w, r, "admin")
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("def"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var d model.MetricDefinition
	if !decode(w, r, &d) {
		return
	}
	if !s.validateMDef(w, r, clusterID, &d) {
		return
	}
	out, err := s.store.UpdateMetricDefinition(r.Context(), clusterID, id, &d)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "definition not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to update")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeleteMetricDef(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.requireClusterRole(w, r, "admin")
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("def"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.store.DeleteMetricDefinition(r.Context(), clusterID, id); err != nil {
		writeErr(w, http.StatusNotFound, "definition not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleMetricPreview — POST …/metrics/preview: Live-Vorschau im Editor
// (Serie der letzten 15 Minuten + Beispiel-Treffer mit extrahiertem Wert).
func (s *Server) handleMetricPreview(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.resolveClusterScope(w, r)
	if !ok {
		return
	}
	var d model.MetricDefinition
	if !decode(w, r, &d) {
		return
	}
	if d.ValueMode == "regex" {
		if re, err := regexp.Compile(d.Pattern); err != nil || re.NumSubexp() < 1 {
			writeErr(w, http.StatusBadRequest, "pattern invalid (needs one capture group)")
			return
		}
	}
	until := time.Now().UTC()
	if d.Source == "promql" {
		series, err := s.promqlToSeries(r, clusterID, d.Query, until.Add(-15*time.Minute), until)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "does not evaluate: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"series": series, "samples": nil})
		return
	}
	series, err := s.tele.QueryDerivedSeries(r.Context(), clusterID, derivedOf(&d), until.Add(-15*time.Minute), until)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "does not evaluate: "+err.Error())
		return
	}
	samples, _ := s.tele.DerivedSamples(r.Context(), clusterID, derivedOf(&d), 3)
	writeJSON(w, http.StatusOK, map[string]any{"series": series, "samples": samples})
}

// handleDerivedSeries — GET …/metrics/derived?id=&since=&until=
func (s *Server) handleDerivedSeries(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.resolveClusterScope(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.URL.Query().Get("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	def, err := s.store.GetMetricDefinition(r.Context(), clusterID, id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "definition not found")
		return
	}
	until := time.Now().UTC()
	since := until.Add(-15 * time.Minute)
	if v := r.URL.Query().Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = t
		}
	}
	if def.Source == "promql" {
		series, err := s.promqlToSeries(r, clusterID, def.Query, since, until)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "series failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"series": series})
		return
	}
	series, err := s.tele.QueryDerivedSeries(r.Context(), clusterID, derivedOf(def), since, until)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "series failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"series": series})
}

// promqlToSeries: PromQL-Matrix → telemetry.Series (Chart-Format).
func (s *Server) promqlToSeries(r *http.Request, clusterID uuid.UUID, query string, since, until time.Time) ([]telemetry.Series, error) {
	step := time.Duration(max64(5, int64(until.Sub(since).Seconds())/120)) * time.Second
	res, closeFn, err := s.promql.QueryRange(r.Context(), clusterID, query, since, until, step)
	if err != nil {
		return nil, err
	}
	defer closeFn()
	out := []telemetry.Series{}
	if m, ok := res.Value.(promql.Matrix); ok {
		for _, series := range m {
			sr := telemetry.Series{Name: seriesLabel(series.Metric.Map())}
			for _, pt := range series.Floats {
				sr.Points = append(sr.Points, telemetry.SeriesPoint{TsMs: pt.T, Value: pt.F})
			}
			out = append(out, sr)
		}
	}
	return out, nil
}

func seriesLabel(m map[string]string) string {
	if v, ok := m["service_name"]; ok {
		return v
	}
	parts := []string{}
	for k, v := range m {
		if k != "__name__" {
			parts = append(parts, k+"="+v)
		}
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		return "value"
	}
	return strings.Join(parts, " ")
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
