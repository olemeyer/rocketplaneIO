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
dev-query: ## Query-Service starten (Go)
	cd services/query && go run ./cmd/query

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
