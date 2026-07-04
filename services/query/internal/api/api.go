// Package api stellt die HTTP-Schicht des query-Service bereit: ein Go-1.22-Mux
// mit Prometheus-kompatiblem Envelope, CORS/Logging/Recover-Middleware sowie den
// Explore-Endpunkten (Services/Traces/Trace) und dem PromQL-Passthrough (501).
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/rocketplaneio/rocketplane/services/query/internal/promql"
	"github.com/rocketplaneio/rocketplane/services/query/internal/store"
)

// Server bündelt die Abhängigkeiten der HTTP-Schicht.
type Server struct {
	store  store.Store
	engine *promql.Engine
	log    *slog.Logger
	cors   map[string]bool
}

// New erstellt den API-Server. corsOrigins ist die Allowlist erlaubter Origins.
func New(st store.Store, engine *promql.Engine, log *slog.Logger, corsOrigins []string) *Server {
	cors := make(map[string]bool, len(corsOrigins))
	for _, o := range corsOrigins {
		cors[o] = true
	}
	return &Server{store: st, engine: engine, log: log, cors: cors}
}

// Handler registriert alle Routen und umschließt sie mit der Middleware-Kette.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.HandleFunc("GET /api/v1/services", s.handleServices)
	mux.HandleFunc("GET /api/v1/traces", s.handleTraces)
	mux.HandleFunc("GET /api/v1/traces/{traceId}", s.handleTrace)
	mux.HandleFunc("GET /api/v1/query", s.handleQuery)
	mux.HandleFunc("GET /api/v1/query_range", s.handleQueryRange)
	return s.recoverMW(s.logMW(s.corsMW(mux)))
}

// --- Health / Readiness -----------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "query"})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- PromQL-Passthrough (in dieser Scheibe 501) -----------------------------

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	expr := r.URL.Query().Get("query")
	if expr == "" {
		writeError(w, http.StatusBadRequest, "bad_data", "missing 'query' parameter")
		return
	}
	at, _ := parseTime(r.URL.Query().Get("time"))
	if _, err := s.engine.Instant(r.Context(), expr, at); err != nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "PromQL engine arrives in milestone M1")
		return
	}
	writeSuccess(w, map[string]any{"resultType": "vector", "result": []any{}})
}

func (s *Server) handleQueryRange(w http.ResponseWriter, r *http.Request) {
	expr := r.URL.Query().Get("query")
	if expr == "" {
		writeError(w, http.StatusBadRequest, "bad_data", "missing 'query' parameter")
		return
	}
	writeError(w, http.StatusNotImplemented, "not_implemented", "PromQL engine arrives in milestone M1")
}

// --- Envelope-Helfer --------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeSuccess(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "data": data})
}

func writeError(w http.ResponseWriter, code int, errType, msg string) {
	writeJSON(w, code, map[string]any{"status": "error", "errorType": errType, "error": msg})
}

// --- Parameter-Parser -------------------------------------------------------

// parseTime akzeptiert unix-Sekunden (auch mit Nachkomma) oder RFC3339.
func parseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		sec := int64(f)
		return time.Unix(sec, int64((f-float64(sec))*1e9)), true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// parseDurationSeconds akzeptiert Sekunden (Zahl) oder Go-Duration ("30s").
func parseDurationSeconds(s string) time.Duration {
	if s == "" {
		return 0
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return time.Duration(f * float64(time.Second))
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	return 0
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func parseInt(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
