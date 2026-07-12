// Command controlplane is the rocketplaneIO Control-Plane: the central,
// multi-tenant SaaS core (auth, tenancy, cluster enrollment, agent sync).
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/api"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/auth"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/config"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/db"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/migrations"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

func main() {
	// Admin subcommands (recovery, run via `kubectl exec deploy/controlplane -- …`).
	if len(os.Args) > 1 && os.Args[1] == "reset-password" {
		if err := resetPassword(os.Args[2:]); err != nil {
			log.Fatalf("reset-password: %v", err)
		}
		return
	}
	if err := run(); err != nil {
		log.Fatalf("controlplane: %v", err)
	}
}

// resetPassword sets a local account's password from the CLI — the self-hosted
// recovery path when the last admin is locked out. Usage:
//
//	controlplane reset-password <email> [new-password]
//
// If no password is given a strong one is generated and printed. Uses the same
// DATABASE_URL as the server; does NOT start the HTTP server.
func resetPassword(args []string) error {
	if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
		return errors.New("usage: controlplane reset-password <email> [new-password]")
	}
	email := strings.TrimSpace(strings.ToLower(args[0]))
	pw := ""
	if len(args) > 1 {
		pw = args[1]
	}
	if pw == "" {
		b := make([]byte, 12)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		pw = "Rp-" + base64.RawURLEncoding.EncodeToString(b)
	}
	if len(pw) < 8 {
		return errors.New("password must be at least 8 characters")
	}

	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := db.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := store.New(pool).SetPasswordByEmail(ctx, email, string(hash)); err != nil {
		return fmt.Errorf("no local account for %q: %w", email, err)
	}
	fmt.Printf("password reset for %s\nnew password: %s\n", email, pw)
	return nil
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		return err
	}

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
			// Ohne Google: lokales Passwort-Login. Der dev-login-Bypass gilt NUR
			// unter RP_ENV=dev — sonst würde die Startzeile fälschlich einen
			// Bypass in Produktion melden.
			if cfg.IsDev() {
				mode = "dev-login"
			} else {
				mode = "local"
			}
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
