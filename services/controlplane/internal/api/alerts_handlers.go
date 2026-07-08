package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/alerts"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/promqlx"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

// alerts_handlers.go — Provider-CRUD (org), Rule-CRUD (cluster), Events und
// die Metrik-Serien für die Charts.

var ruleKinds = map[string]bool{
	"log_errors": true, "trace_error_ratio": true, "trace_p95_ms": true,
	"node_cpu_pct": true, "node_mem_pct": true, "node_disk_pct": true,
	"workload_unready": true, "derived": true, "promql": true,
}
var providerTypes = map[string]bool{"webhook": true, "slack": true, "email": true}

/* ── Provider ───────────────────────────────────────────────────────────── */

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	provs, err := s.store.ListAlertProviders(r.Context(), orgID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list providers")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": provs})
}

func (s *Server) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := s.requireRole(w, r, "admin")
	if !ok {
		return
	}
	var req struct {
		Name   string          `json:"name"`
		Type   string          `json:"type"`
		Config json.RawMessage `json:"config"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Name == "" || !providerTypes[req.Type] {
		writeErr(w, http.StatusBadRequest, "name and valid type (webhook|slack|email) required")
		return
	}
	if len(req.Config) == 0 {
		req.Config = json.RawMessage(`{}`)
	}
	p, err := s.store.CreateAlertProvider(r.Context(), orgID, req.Name, req.Type, req.Config)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create provider (name taken?)")
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := s.requireRole(w, r, "admin")
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("provider"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid provider id")
		return
	}
	if err := s.store.DeleteAlertProvider(r.Context(), orgID, id); err != nil {
		writeErr(w, http.StatusNotFound, "provider not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleTestProvider — POST …/alert-providers/{provider}/test: Probeversand.
func (s *Server) handleTestProvider(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("provider"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid provider id")
		return
	}
	provs, err := s.store.ListAlertProviders(r.Context(), orgID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load providers")
		return
	}
	for i := range provs {
		if provs[i].ID == id {
			p := alerts.Payload{
				Rule: "test notification", State: "test", Severity: "info",
				At: time.Now().UTC(), Message: "rocketplane test notification — channel is working",
			}
			if err := alerts.SendNotification(r.Context(), &http.Client{Timeout: 10 * time.Second}, &provs[i], &p); err != nil {
				writeErr(w, http.StatusBadGateway, "send failed: "+err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
			return
		}
	}
	writeErr(w, http.StatusNotFound, "provider not found")
}

/* ── Rules ──────────────────────────────────────────────────────────────── */

type ruleReq struct {
	Name               string          `json:"name"`
	Kind               string          `json:"kind"`
	Params             json.RawMessage `json:"params"`
	Op                 string          `json:"op"`
	Threshold          float64         `json:"threshold"`
	WindowSeconds      int             `json:"windowSeconds"`
	ForSeconds         int             `json:"forSeconds"`
	Severity           string          `json:"severity"`
	ProviderIDs        []uuid.UUID     `json:"providerIds"`
	Enabled            *bool           `json:"enabled"`
	Query              string          `json:"query"`
	ActionDefinitionID *uuid.UUID      `json:"actionDefinitionId"`
	ActionArgs         json.RawMessage `json:"actionArgs"`
}

func (s *Server) validateRule(w http.ResponseWriter, r *http.Request, clusterID uuid.UUID, req *ruleReq) bool {
	if req.Name == "" || !ruleKinds[req.Kind] {
		writeErr(w, http.StatusBadRequest, "name and valid kind required")
		return false
	}
	if req.Kind == "promql" {
		// PromQL-Bedingung: parse + Probe — kaputte Rules kann man nicht anlegen.
		if err := promqlx.Check(req.Query); err != nil {
			writeErr(w, http.StatusBadRequest, "query does not parse: "+err.Error())
			return false
		}
		if _, closeFn, err := s.promql.QueryInstant(r.Context(), clusterID, req.Query, time.Now().UTC()); err != nil {
			writeErr(w, http.StatusBadRequest, "query does not evaluate: "+err.Error())
			return false
		} else {
			closeFn()
		}
	}
	if req.Op != "gt" && req.Op != "lt" {
		req.Op = "gt"
	}
	// clamp auf die Grenzen statt still auf einen Default zurückzusetzen —
	// „window 10s" wird 30s, nicht kommentarlos 300s.
	if req.WindowSeconds == 0 {
		req.WindowSeconds = 300
	} else if req.WindowSeconds < 30 {
		req.WindowSeconds = 30
	} else if req.WindowSeconds > 3600 {
		req.WindowSeconds = 3600
	}
	if req.ForSeconds < 0 {
		req.ForSeconds = 0
	} else if req.ForSeconds > 3600 {
		req.ForSeconds = 3600
	}
	if req.Severity != "critical" {
		req.Severity = "warning"
	}
	if len(req.Params) == 0 {
		req.Params = json.RawMessage(`{}`)
	}
	if req.ActionDefinitionID != nil {
		// Auto-Remediation: Workflow muss existieren und alle required-Args
		// müssen (als Arg oder Default) belegt sein — sonst crasht der
		// Starlark-Run erst beim firing. Nichts Kaputtes anlegbar.
		def, err := s.store.GetActionDefinitionForCluster(r.Context(), clusterID, *req.ActionDefinitionID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "remediation workflow not found")
			return false
		}
		var dparams []struct {
			Name     string `json:"name"`
			Default  string `json:"default"`
			Required bool   `json:"required"`
		}
		_ = json.Unmarshal(def.Params, &dparams)
		args := map[string]string{}
		if len(req.ActionArgs) > 0 {
			_ = json.Unmarshal(req.ActionArgs, &args)
		}
		for _, p := range dparams {
			if p.Required && args[p.Name] == "" && p.Default == "" {
				writeErr(w, http.StatusBadRequest, "remediation workflow needs arg "+p.Name)
				return false
			}
		}
	}
	return true
}

func ruleFromReq(clusterID uuid.UUID, req *ruleReq) *model.AlertRule {
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if req.ProviderIDs == nil {
		req.ProviderIDs = []uuid.UUID{}
	}
	if len(req.ActionArgs) == 0 {
		req.ActionArgs = json.RawMessage(`{}`)
	}
	return &model.AlertRule{
		ClusterID: clusterID, Name: req.Name, Kind: req.Kind, Params: req.Params,
		Op: req.Op, Threshold: req.Threshold, WindowSeconds: req.WindowSeconds,
		ForSeconds: req.ForSeconds, Severity: req.Severity, ProviderIDs: req.ProviderIDs, Enabled: enabled,
		Query: req.Query, ActionDefinitionID: req.ActionDefinitionID, ActionArgs: req.ActionArgs,
	}
}

func (s *Server) handleListRules(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.resolveClusterScope(w, r)
	if !ok {
		return
	}
	rules, err := s.store.ListAlertRules(r.Context(), clusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list rules")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

func (s *Server) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.requireClusterRole(w, r, "admin")
	if !ok {
		return
	}
	var req ruleReq
	if !decode(w, r, &req) || !s.validateRule(w, r, clusterID, &req) {
		return
	}
	rule, err := s.store.CreateAlertRule(r.Context(), ruleFromReq(clusterID, &req))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create rule (name taken?)")
		return
	}
	s.broker.Publish(clusterID, "alerts", 0)
	writeJSON(w, http.StatusCreated, rule)
}

func (s *Server) handleUpdateRule(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.requireClusterRole(w, r, "admin")
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("rule"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid rule id")
		return
	}
	var req ruleReq
	if !decode(w, r, &req) || !s.validateRule(w, r, clusterID, &req) {
		return
	}
	rule, err := s.store.UpdateAlertRule(r.Context(), clusterID, id, ruleFromReq(clusterID, &req))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "rule not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to update rule")
		return
	}
	s.broker.Publish(clusterID, "alerts", 0)
	writeJSON(w, http.StatusOK, rule)
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.requireClusterRole(w, r, "admin")
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("rule"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid rule id")
		return
	}
	if err := s.store.DeleteAlertRule(r.Context(), clusterID, id); err != nil {
		writeErr(w, http.StatusNotFound, "rule not found")
		return
	}
	s.broker.Publish(clusterID, "alerts", 0)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleListAlertEvents(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.resolveClusterScope(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	evs, err := s.store.ListAlertEvents(r.Context(), clusterID, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list events")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": evs})
}

/* ── Metrik-Serien ──────────────────────────────────────────────────────── */

// handleMetricSeries — GET …/metrics/series?kind=red|logs|infra&…
func (s *Server) handleMetricSeries(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.resolveClusterScope(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	until := time.Now().UTC()
	since := until.Add(-15 * time.Minute)
	if v := q.Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = t
		}
	}
	if v := q.Get("until"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			until = t
		}
	}
	var out any
	var err error
	switch q.Get("kind") {
	case "red":
		out, err = s.tele.QueryREDSeries(r.Context(), clusterID, q.Get("metric"), q.Get("service"), q.Get("namespace"), since, until)
	case "logs":
		out, err = s.tele.QueryLogRateSeries(r.Context(), clusterID, q.Get("namespace"), q.Get("workload"), since, until)
	case "infra":
		out, err = s.tele.QueryInfraSeries(r.Context(), clusterID, q.Get("scope"), q.Get("metric"), q.Get("name"), since, until)
	default:
		writeErr(w, http.StatusBadRequest, "kind must be red, logs or infra")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "series query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"series": out})
}

// resolveClusterScope: Org-Mitgliedschaft + Cluster-Zugehörigkeit in einem.
func (s *Server) resolveClusterScope(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	if _, _, err := s.store.GetClusterWithNamespaces(r.Context(), orgID, clusterID); err != nil {
		writeErr(w, http.StatusNotFound, "cluster not found")
		return uuid.Nil, uuid.Nil, false
	}
	return orgID, clusterID, true
}

// handleMuteRule — POST …/alert-rules/{rule}/mute {minutes} (0 = unmute).
func (s *Server) handleMuteRule(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.resolveClusterScope(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("rule"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid rule id")
		return
	}
	var req struct {
		Minutes int `json:"minutes"`
	}
	if !decode(w, r, &req) {
		return
	}
	var until *time.Time
	if req.Minutes > 0 {
		t := time.Now().UTC().Add(time.Duration(req.Minutes) * time.Minute)
		until = &t
	}
	if err := s.store.SetRuleMute(r.Context(), clusterID, id, until); err != nil {
		writeErr(w, http.StatusNotFound, "rule not found")
		return
	}
	s.broker.Publish(clusterID, "alerts", 0)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// handleRuleSeries — GET …/alert-rules/{rule}/series: die vom Evaluator
// gesehene Wert-Historie (Sparkline-Datenbasis der Rule-Karten).
func (s *Server) handleRuleSeries(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.resolveClusterScope(w, r)
	if !ok {
		return
	}
	id := r.PathValue("rule")
	until := time.Now().UTC()
	since := until.Add(-30 * time.Minute)
	series, err := s.tele.QueryInfraSeries(r.Context(), clusterID, "alert", "value", id, since, until)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "series failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"series": series})
}
