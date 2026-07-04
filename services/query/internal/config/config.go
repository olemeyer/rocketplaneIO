// Package config lädt die Konfiguration des query-Service aus der Umgebung.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rocketplaneio/rocketplane/services/query/internal/store/clickhouse"
)

// Backend wählt die Store-Implementierung.
type Backend string

const (
	BackendSeed       Backend = "seed"
	BackendClickHouse Backend = "clickhouse"
)

// Config ist die aufgelöste Laufzeit-Konfiguration.
type Config struct {
	Addr            string
	CORSOrigins     []string
	Backend         Backend
	ClickHouse      clickhouse.Config
	ShutdownTimeout time.Duration
	Debug           bool
}

// Load liest die Config aus Env-Variablen (mit Defaults) und validiert sie.
func Load() (Config, error) {
	cfg := Config{
		Addr:            envOr("QUERY_ADDR", ":7080"),
		CORSOrigins:     splitCSV(envOr("QUERY_CORS_ORIGINS", "http://localhost:4173")),
		Backend:         Backend(envOr("QUERY_STORE", string(BackendSeed))),
		ShutdownTimeout: 10 * time.Second,
		Debug:           os.Getenv("DEBUG") != "",
		ClickHouse: clickhouse.Config{
			URL:      envOr("CLICKHOUSE_URL", "http://localhost:8123"),
			Database: envOr("CLICKHOUSE_DB", "otel"),
			User:     os.Getenv("CLICKHOUSE_USER"),
			Password: os.Getenv("CLICKHOUSE_PASSWORD"),
		},
	}

	switch cfg.Backend {
	case BackendSeed, BackendClickHouse:
	default:
		return Config{}, fmt.Errorf("invalid QUERY_STORE %q (want seed|clickhouse)", cfg.Backend)
	}
	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
