// Package config loads the Control-Plane configuration from the environment
// (see docs/architecture.md §9) applying sensible defaults for local dev.
package config

import (
	"os"
	"strings"
)

// Config holds all runtime configuration for the Control-Plane.
type Config struct {
	Env                string // RP_ENV (dev enables the dev-login bypass)
	DatabaseURL        string // DATABASE_URL
	Listen             string // RP_LISTEN
	PublicURL          string // RP_PUBLIC_URL (install-command + OIDC redirect base)
	SessionSecret      string // RP_SESSION_SECRET (HMAC key)
	GoogleClientID     string // GOOGLE_CLIENT_ID
	GoogleClientSecret string // GOOGLE_CLIENT_SECRET

	// ── Agent-Install (was der „Connect cluster"-Command generiert) ──
	// Alles konfigurierbar, damit derselbe UI-Command in jeder Umgebung passt:
	// prod (Helm+ghcr) genauso wie lokal (kubectl+lokales Image+host.minikube.internal).
	AgentInstallMethod   string // RP_AGENT_INSTALL_METHOD: "helm" (default) | "kubectl"
	AgentImage           string // RP_AGENT_IMAGE: Container-Image-Ref des Agents
	AgentChart           string // RP_AGENT_CHART: OCI-Ref des Helm-Charts
	AgentControlPlaneURL string // RP_AGENT_CONTROLPLANE_URL: URL, die der Agent-POD nutzt
	// (≠ PublicURL: aus dem Cluster ist localhost nicht erreichbar)
	AgentFlows bool // RP_AGENT_FLOWS: DaemonSet + hostNetwork + NET_ADMIN (conntrack-Flows)

	// ── Telemetrie-Store (ClickHouse) ──
	ClickHouseURL      string // CLICKHOUSE_URL (leer = Telemetrie aus)
	ClickHouseUser     string // CLICKHOUSE_USER
	ClickHousePassword string // CLICKHOUSE_PASSWORD
	ClickHouseDB       string // CLICKHOUSE_DB
}

// Load reads the configuration from the environment, applying defaults.
func Load() *Config {
	return &Config{
		Env:                env("RP_ENV", "dev"),
		DatabaseURL:        env("DATABASE_URL", "postgres://rocketplane:rocketplane@localhost:5432/rocketplane?sslmode=disable"),
		Listen:             env("RP_LISTEN", ":8090"),
		PublicURL:          strings.TrimRight(env("RP_PUBLIC_URL", "http://localhost:8090"), "/"),
		SessionSecret:      env("RP_SESSION_SECRET", "dev-insecure-session-secret-change-me"),
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),

		AgentInstallMethod: env("RP_AGENT_INSTALL_METHOD", "helm"),
		AgentImage:         env("RP_AGENT_IMAGE", "ghcr.io/rocketplaneio/agent:latest"),
		AgentChart:         env("RP_AGENT_CHART", "oci://ghcr.io/rocketplaneio/charts/rocketplane-agent"),
		// Fällt auf PublicURL zurück, wenn nicht explizit gesetzt.
		AgentControlPlaneURL: strings.TrimRight(env("RP_AGENT_CONTROLPLANE_URL", env("RP_PUBLIC_URL", "http://localhost:8090")), "/"),
		AgentFlows:           env("RP_AGENT_FLOWS", "false") == "true",

		ClickHouseURL:      env("CLICKHOUSE_URL", "http://localhost:8123"),
		ClickHouseUser:     env("CLICKHOUSE_USER", "rocketplane"),
		ClickHousePassword: env("CLICKHOUSE_PASSWORD", "rocketplane"),
		ClickHouseDB:       env("CLICKHOUSE_DB", "otel"),
	}
}

// IsDev reports whether the Control-Plane runs in dev mode.
func (c *Config) IsDev() bool { return c.Env == "dev" }

// GoogleConfigured reports whether Google OIDC credentials are present.
// When false (and IsDev), the dev-login bypass is enabled.
func (c *Config) GoogleConfigured() bool {
	return c.GoogleClientID != "" && c.GoogleClientSecret != ""
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
