// Package otlp implements the OTLP ingestion surface of the ingest service.
//
// Heutiger Stand: Health-Checks funktionieren, die OTLP/HTTP-Signal-Endpunkte
// (/v1/traces, /v1/metrics, /v1/logs) sind als 501-Stubs angelegt. Der reale
// Pfad wird OTLP → Validierung/OTTL-Transform → ClickHouse-Writer sein.
package otlp

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// Handler bündelt die HTTP-Routen der Ingestion.
type Handler struct {
	log *slog.Logger
}

// New erstellt einen Ingestion-Handler.
func New(log *slog.Logger) *Handler {
	return &Handler{log: log}
}

// Mux registriert alle Routen und gibt einen fertigen http.Handler zurück.
func (h *Handler) Mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /readyz", h.health)
	mux.HandleFunc("POST /v1/traces", h.notImplemented("traces"))
	mux.HandleFunc("POST /v1/metrics", h.notImplemented("metrics"))
	mux.HandleFunc("POST /v1/logs", h.notImplemented("logs"))
	return h.withLogging(mux)
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "ingest"})
}

func (h *Handler) notImplemented(signal string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"error":  "not implemented",
			"signal": signal,
			"hint":   "OTLP-Ingestion folgt in Meilenstein M0 (Ingest & Store).",
		})
	}
}

func (h *Handler) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.log.Debug("request", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
