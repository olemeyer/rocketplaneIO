// Package config lädt die Konfiguration des ingest-Service aus der Umgebung.
package config

import (
	"os"
	"time"

	"github.com/rocketplaneio/rocketplane/services/ingest/internal/chsink"
)

// Config ist die aufgelöste Laufzeit-Konfiguration.
type Config struct {
	Addr            string
	ClickHouse      chsink.Config
	ShutdownTimeout time.Duration
	Debug           bool
}

// Load liest die Config aus Env-Variablen (mit Defaults).
func Load() Config {
	return Config{
		Addr:            envOr("INGEST_ADDR", ":4318"),
		ShutdownTimeout: 10 * time.Second,
		Debug:           os.Getenv("DEBUG") != "",
		ClickHouse: chsink.Config{
			URL:      envOr("CLICKHOUSE_URL", "http://localhost:8123"),
			Database: envOr("CLICKHOUSE_DB", "otel"),
			User:     os.Getenv("CLICKHOUSE_USER"),
			Password: os.Getenv("CLICKHOUSE_PASSWORD"),
		},
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
