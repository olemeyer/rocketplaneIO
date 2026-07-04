// Command query ist der PromQL-Query-Service von rocketplane.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rocketplaneio/rocketplane/services/query/internal/promql"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel()}))
	addr := envOr("QUERY_ADDR", ":7080")
	engine := promql.NewEngine()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "query"})
	})
	mux.HandleFunc("GET /api/v1/query", queryHandler(engine, false))
	mux.HandleFunc("GET /api/v1/query_range", queryHandler(engine, true))

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Info("query listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
}

// queryHandler bindet die (noch nicht implementierte) Engine an die
// Prometheus-kompatible HTTP-API an.
func queryHandler(engine *promql.Engine, _ bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		expr := r.URL.Query().Get("query")
		if expr == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"status": "error", "errorType": "bad_data", "error": "missing 'query' parameter",
			})
			return
		}
		_, err := engine.Instant(r.Context(), expr, time.Time{})
		if errors.Is(err, promql.ErrNotImplemented) {
			writeJSON(w, http.StatusNotImplemented, map[string]string{
				"status": "error",
				"error":  "PromQL-Engine folgt in Meilenstein M0/M1.",
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
	}
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func logLevel() slog.Level {
	if os.Getenv("DEBUG") != "" {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}
