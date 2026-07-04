// Command ingest ist der OTLP-Ingestion-Service von rocketplane: nimmt OTLP/HTTP-
// Traces entgegen und schreibt sie nach ClickHouse (otel_traces).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rocketplaneio/rocketplane/services/ingest/internal/chsink"
	"github.com/rocketplaneio/rocketplane/services/ingest/internal/config"
	"github.com/rocketplaneio/rocketplane/services/ingest/internal/otlp"
)

func main() {
	cfg := config.Load()
	level := slog.LevelInfo
	if cfg.Debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	sink := chsink.New(cfg.ClickHouse, nil)
	handler := otlp.New(log, sink).Mux()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Info("ingest listening", "addr", cfg.Addr, "protocol", "otlp/http", "clickhouse", cfg.ClickHouse.URL)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
}
