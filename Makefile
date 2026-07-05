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
dev-ingest: ## OTLP-Ingest-Service starten (:4318 -> ClickHouse)
	cd services/ingest && $(CH_ENV) go run ./cmd/ingest

.PHONY: dev-query
dev-query: ## Query-Service starten (Go, Seed-Store)
	cd services/query && go run ./cmd/query

## ── Live-Daten (ClickHouse) ────────────────────────────────
CH_ENV := CLICKHOUSE_URL=http://localhost:8123 CLICKHOUSE_DB=otel CLICKHOUSE_USER=rocketplane CLICKHOUSE_PASSWORD=rocketplane

.PHONY: ch-schema
ch-schema: ## otel_traces + otel_logs + otel_metrics Schema in ClickHouse anlegen
	curl -s -u rocketplane:rocketplane "http://localhost:8123/?database=otel" \
		--data-binary @deploy/clickhouse/otel_traces.sql && echo "otel_traces ok"
	curl -s -u rocketplane:rocketplane "http://localhost:8123/?database=otel" \
		--data-binary @deploy/clickhouse/otel_logs.sql && echo "otel_logs ok"
	@# otel_metrics.sql enthält mehrere Statements -> pro Statement senden.
	awk 'BEGIN{RS=";"} /CREATE TABLE/ { print $$0 ";" > "/tmp/rp-ch-stmt.sql"; \
		system("curl -s -u rocketplane:rocketplane \"http://localhost:8123/?database=otel\" --data-binary @/tmp/rp-ch-stmt.sql") }' \
		deploy/clickhouse/otel_metrics.sql && echo "otel_metrics ok"

.PHONY: tracegen
tracegen: ## Direkter Generator: schreibt otel_traces (ohne OTLP; Backfill)
	cd services/query && $(CH_ENV) go run ./cmd/tracegen -backfill 15m -every 2s

.PHONY: otlpgen
otlpgen: ## OTLP-Generator: sendet echte OTLP-Traces an den ingest-Service
	cd services/ingest && go run ./cmd/otlpgen -endpoint http://localhost:4318 -every 2s

.PHONY: dev-query-ch
dev-query-ch: ## Query-Service gegen ClickHouse starten (echte Daten)
	cd services/query && QUERY_STORE=clickhouse $(CH_ENV) go run ./cmd/query

.PHONY: live
live: up ## Voller Live-Stack-Hinweis (Infra hoch + Anleitung)
	@sleep 2 && $(MAKE) ch-schema
	@echo ""
	@echo "Live-Stack (OTLP-Pfad) — in vier Terminals starten:"
	@echo "  make dev-ingest     # OTLP-Ingest (:4318) -> ClickHouse"
	@echo "  make otlpgen        # sendet OTLP-Traces an den Ingest"
	@echo "  make dev-query-ch   # query-Service gegen ClickHouse (:7080)"
	@echo "  make dev            # Web-App (:4173) -> /explore"
	@echo ""
	@echo "Alternativ ohne OTLP: 'make tracegen' schreibt direkt in otel_traces."

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
	go -C services/ingest vet ./...
	go -C services/query vet ./...

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
