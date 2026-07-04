package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	// Alle relevanten Env-Vars leeren, damit Defaults greifen.
	for _, k := range []string{"QUERY_ADDR", "QUERY_CORS_ORIGINS", "QUERY_STORE", "DEBUG"} {
		t.Setenv(k, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":7080" {
		t.Errorf("Addr = %q, want :7080", cfg.Addr)
	}
	if cfg.Backend != BackendSeed {
		t.Errorf("Backend = %q, want seed", cfg.Backend)
	}
	if len(cfg.CORSOrigins) != 1 || cfg.CORSOrigins[0] != "http://localhost:4173" {
		t.Errorf("CORSOrigins = %v", cfg.CORSOrigins)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("QUERY_ADDR", ":9999")
	t.Setenv("QUERY_STORE", "clickhouse")
	t.Setenv("QUERY_CORS_ORIGINS", "http://a.test, http://b.test ,")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":9999" {
		t.Errorf("Addr = %q", cfg.Addr)
	}
	if cfg.Backend != BackendClickHouse {
		t.Errorf("Backend = %q", cfg.Backend)
	}
	if len(cfg.CORSOrigins) != 2 {
		t.Errorf("CORSOrigins = %v, want 2 entries", cfg.CORSOrigins)
	}
}

func TestLoadInvalidBackend(t *testing.T) {
	t.Setenv("QUERY_STORE", "postgres")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid backend")
	}
}
