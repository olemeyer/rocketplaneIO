// Package config loads the Control-Plane configuration from the environment
// (see docs/architecture.md §9) applying sensible defaults for local dev.
package config

import (
	"net/url"
	"os"
	"strings"
)

// Config holds all runtime configuration for the Control-Plane.
type Config struct {
	Env                string // RP_ENV (dev enables the dev-login bypass)
	DatabaseURL        string // DATABASE_URL
	Listen             string // RP_LISTEN
	PublicURL          string // RP_PUBLIC_URL (install-command + OIDC redirect base)
	SessionSecret      string   // RP_SESSION_SECRET (HMAC key)
	GoogleClientID     string   // GOOGLE_CLIENT_ID
	GoogleClientSecret string   // GOOGLE_CLIENT_SECRET
	PlatformAdmins     []string // RP_PLATFORM_ADMINS (comma-separated emails granted super-admin on boot)

	// ── Agent-Install (was der „Connect cluster"-Command generiert) ──
	// Alles konfigurierbar, damit derselbe UI-Command in jeder Umgebung passt:
	// prod (Helm+ghcr) genauso wie lokal (kubectl+lokales Image+host.minikube.internal).
	AgentInstallMethod   string // RP_AGENT_INSTALL_METHOD: "helm" (default) | "kubectl"
	AgentImage           string // RP_AGENT_IMAGE: Container-Image-Ref des Agents
	AgentChart           string // RP_AGENT_CHART: OCI-Ref des Helm-Charts
	AgentControlPlaneURL string // RP_AGENT_CONTROLPLANE_URL: URL, die der Agent-POD nutzt
	// (≠ PublicURL: aus dem Cluster ist localhost nicht erreichbar)
	AgentOTLPEndpoint string // RP_AGENT_OTLP_ENDPOINT: OTLP-Ziel für Beyla im Ziel-Cluster
	// (leer = aus AgentControlPlaneURL abgeleitet: gleicher Host, :4318)
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
		PlatformAdmins:     splitList(os.Getenv("RP_PLATFORM_ADMINS")),

		AgentInstallMethod: env("RP_AGENT_INSTALL_METHOD", "helm"),
		AgentImage:         env("RP_AGENT_IMAGE", "ghcr.io/olemeyer/rocketplaneio/agent:edge"),
		AgentChart:         env("RP_AGENT_CHART", "oci://ghcr.io/olemeyer/rocketplaneio/charts/rocketplane-agent"),
		// Fällt auf PublicURL zurück, wenn nicht explizit gesetzt.
		AgentControlPlaneURL: strings.TrimRight(env("RP_AGENT_CONTROLPLANE_URL", env("RP_PUBLIC_URL", "http://localhost:8090")), "/"),
		AgentOTLPEndpoint:    strings.TrimRight(env("RP_AGENT_OTLP_ENDPOINT", ""), "/"),
		AgentFlows:           env("RP_AGENT_FLOWS", "false") == "true",

		ClickHouseURL:      env("CLICKHOUSE_URL", "http://localhost:8123"),
		ClickHouseUser:     env("CLICKHOUSE_USER", "rocketplane"),
		ClickHousePassword: env("CLICKHOUSE_PASSWORD", "rocketplane"),
		ClickHouseDB:       env("CLICKHOUSE_DB", "otel"),
	}
}

// IsDev reports whether the Control-Plane runs in dev mode.
func (c *Config) IsDev() bool { return c.Env == "dev" }

// AgentOTLP returns the OTLP endpoint Beyla exports to inside an enrolled
// cluster — baked into every generated install command so copy-paste works in
// every topology. Explicit RP_AGENT_OTLP_ENDPOINT wins (set it to the in-cluster
// collector Service, e.g. http://otel-collector:4318, for an all-in-one prod
// cluster, or to a public collector for a hosted control plane). Otherwise it is
// derived from the agent-facing control-plane host with port 4318 — correct for
// the common co-located deployment (docker-compose self-host) where the collector
// sits next to the control plane on the same host.
func (c *Config) AgentOTLP() string {
	if c.AgentOTLPEndpoint != "" {
		return c.AgentOTLPEndpoint
	}
	if u, err := url.Parse(c.AgentControlPlaneURL); err == nil && u.Hostname() != "" {
		scheme := u.Scheme
		if scheme == "" {
			scheme = "http"
		}
		return scheme + "://" + u.Hostname() + ":4318"
	}
	return "http://otel-collector:4318"
}

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

// splitList parses a comma-separated env value into a trimmed, lowercased list.
func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(strings.ToLower(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}
