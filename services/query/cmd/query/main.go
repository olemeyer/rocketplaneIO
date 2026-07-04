// Command query ist der Read-/PromQL-Service von rocketplane.
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

	"github.com/rocketplaneio/rocketplane/services/query/internal/api"
	"github.com/rocketplaneio/rocketplane/services/query/internal/config"
	"github.com/rocketplaneio/rocketplane/services/query/internal/promql"
	"github.com/rocketplaneio/rocketplane/services/query/internal/store"
	"github.com/rocketplaneio/rocketplane/services/query/internal/store/clickhouse"
	"github.com/rocketplaneio/rocketplane/services/query/internal/store/seed"
)

func main() {
	cfg, err := config.Load()
	logLevel := slog.LevelInfo
	if cfg.Debug {
		logLevel = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	st := openStore(cfg)
	defer func() { _ = st.Close() }()

	engine := promql.NewEngine()
	handler := api.New(st, engine, log, cfg.CORSOrigins).Handler()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Info("query listening", "addr", cfg.Addr, "store", cfg.Backend)
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

// openStore wählt die Store-Implementierung anhand der Config.
func openStore(cfg config.Config) store.Store {
	switch cfg.Backend {
	case config.BackendClickHouse:
		return clickhouse.New(cfg.ClickHouse, nil)
	default:
		return seed.New()
	}
}
