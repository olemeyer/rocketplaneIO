package main

import (
	"context"
	"testing"

	"github.com/rocketplaneio/rocketplane/services/query/internal/config"
	"github.com/rocketplaneio/rocketplane/services/query/internal/store"
)

func TestOpenStoreSeedDefault(t *testing.T) {
	st := openStore(config.Config{Backend: config.BackendSeed})
	if st == nil {
		t.Fatal("openStore returned nil")
	}
	defer st.Close()
	if err := st.Ping(context.Background()); err != nil {
		t.Fatalf("seed Ping = %v, want nil", err)
	}
	res, err := st.Services(context.Background(), store.ServicesQuery{})
	if err != nil {
		t.Fatalf("Services: %v", err)
	}
	if len(res.Services) == 0 {
		t.Fatal("seed store returned no services")
	}
}

func TestOpenStoreClickHouse(t *testing.T) {
	st := openStore(config.Config{Backend: config.BackendClickHouse})
	if st == nil {
		t.Fatal("openStore returned nil for clickhouse")
	}
	_ = st.Close()
}
