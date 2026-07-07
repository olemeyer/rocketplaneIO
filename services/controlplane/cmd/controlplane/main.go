// Command controlplane is the rocketplaneIO Control-Plane: the central,
// multi-tenant SaaS core (auth, tenancy, cluster enrollment, agent sync).
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/api"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/auth"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/config"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/db"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/migrations"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("controlplane: %v", err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.Load()

	// Database pool + migrations.
	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	pool, err := db.New(startCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.ApplyMigrations(startCtx, pool, migrations.FS); err != nil {
		return err
	}
	log.Printf("migrations applied")

	// Wiring.
	st := store.New(pool)
	au, err := auth.New(ctx, cfg, st)
	if err != nil {
		return err
	}
	server := api.New(cfg, st, au, pool)
	server.StartBackground(ctx)

	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	// SSE-Streams (Browser + Agenten) enden nie von selbst — beim Shutdown
	// aktiv schließen, sonst wartet Shutdown() vergeblich bis zum Timeout.
	httpServer.RegisterOnShutdown(server.NotifyShutdown)

	// Serve.
	errCh := make(chan error, 1)
	go func() {
		mode := "google-oidc"
		if !cfg.GoogleConfigured() {
			mode = "dev-login"
		}
		log.Printf("controlplane listening on %s (env=%s, auth=%s, public=%s)", cfg.Listen, cfg.Env, mode, cfg.PublicURL)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Printf("shutdown signal received")
	}

	// Graceful shutdown.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return err
	}
	log.Printf("controlplane stopped")
	return nil
}
