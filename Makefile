# rocketplane — Convenience-Targets ueber den polyglotten Stack (Node + Go + Compose).
# `make help` listet alle Targets.

SHELL := /bin/bash
.DEFAULT_GOAL := help

COMPOSE := docker compose -f deploy/compose/docker-compose.yml

.PHONY: help
help: ## Diese Hilfe anzeigen
	@grep -E '^[a-zA-Z0-9_.-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

## ── Setup ──────────────────────────────────────────────────
.PHONY: install
install: ## JS-Deps (pnpm) + Go-Module synchronisieren
	pnpm install
	cd services/ingest && go mod tidy
	cd services/query && go mod tidy

## ── Dev ────────────────────────────────────────────────────
.PHONY: dev
dev: ## Frontend-Dev-Server (Next.js) starten
	pnpm --filter @rocketplane/web dev

.PHONY: dev-ingest
dev-ingest: ## Ingest-Service starten (Go)
	cd services/ingest && go run ./cmd/ingest

.PHONY: dev-query
dev-query: ## Query-Service starten (Go, Seed-Store)
	cd services/query && go run ./cmd/query

## ── Live-Daten (ClickHouse) ────────────────────────────────
CH_ENV := CLICKHOUSE_URL=http://localhost:8123 CLICKHOUSE_DB=otel CLICKHOUSE_USER=rocketplane CLICKHOUSE_PASSWORD=rocketplane

.PHONY: ch-schema
ch-schema: ## otel_traces-Schema in ClickHouse anlegen
	curl -s -u rocketplane:rocketplane "http://localhost:8123/?database=otel" \
		--data-binary @deploy/clickhouse/otel_traces.sql && echo "schema ok"

.PHONY: tracegen
tracegen: ## Live-Trace-Generator: speist otel_traces (Ctrl-C zum Stoppen)
	cd services/query && $(CH_ENV) go run ./cmd/tracegen -backfill 15m -every 2s

.PHONY: dev-query-ch
dev-query-ch: ## Query-Service gegen ClickHouse starten (echte Daten)
	cd services/query && QUERY_STORE=clickhouse $(CH_ENV) go run ./cmd/query

.PHONY: live
live: up ## Voller Live-Stack-Hinweis (Infra hoch + Anleitung)
	@sleep 2 && $(MAKE) ch-schema
	@echo ""
	@echo "Live-Stack — in drei Terminals starten:"
	@echo "  make tracegen       # kontinuierliche otel_traces-Daten"
	@echo "  make dev-query-ch   # query-Service gegen ClickHouse (:7080)"
	@echo "  make dev            # Web-App (:4173) -> /explore"

## ── Build / Check ──────────────────────────────────────────
.PHONY: build
build: ## Alles bauen (turbo + go)
	pnpm build
	pnpm go:build

.PHONY: test
test: ## Alle Tests (turbo + go)
	pnpm test
	pnpm go:test

.PHONY: lint
lint: ## Lint (turbo) + go vet
	pnpm lint
	go vet ./services/...

## ── Infra (lokal) ──────────────────────────────────────────
.PHONY: up
up: ## ClickHouse + Postgres + OTel-Collector hochfahren
	$(COMPOSE) up -d

.PHONY: down
down: ## Lokale Infra stoppen
	$(COMPOSE) down

.PHONY: logs
logs: ## Compose-Logs folgen
	$(COMPOSE) logs -f
