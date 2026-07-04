<div align="center">

# 🚀 rocketplane

**Open-source, OpenTelemetry-native observability — mit einer keyboard-first UI/UX.**

Traces · Metrics · Logs · RUM · Synthetics — self-hostbar, EU-souverän, ohne Lock-in.

</div>

---

> **Status:** frühes Scaffold (M0). Dieses Monorepo enthält bisher das Grundgerüst
> (Frontend-SPA, Go-Services, geteilte Packages, lokale Infra). Die eigentliche
> Feature-Arbeit folgt der Roadmap aus der internen Produkt-Doku.

rocketplane ist die quelloffene Antwort auf kommerzielle Observability-Plattformen
wie Dash0: dieselben starken Ideen — **keyboard-first Bedienung**, **resource-zentrische
Navigation**, **durchgängige Cross-Signal-Korrelation**, **offene Standards** (OTLP,
PromQL, Perses) — aber vollständig self-hostbar und transparent.

## Repository-Struktur

```
rocketplane/
├─ apps/
│  └─ web/            # Next.js (App Router) + Tailwind, keyboard-first UI
├─ services/
│  ├─ ingest/         # Go — OTLP-Ingestion (gRPC/HTTP → ClickHouse)
│  └─ query/          # Go — PromQL-über-alle-Signale-Engine
├─ packages/
│  ├─ ui/             # Offenes Design-System (Tokens, später Komponenten)
│  └─ tsconfig/       # Geteilte TypeScript-Konfigurationen
├─ deploy/
│  ├─ compose/        # Lokale Infra: ClickHouse + Postgres + OTel-Collector
│  └─ helm/           # Helm-Chart-Stub für Self-Hosting
├─ go.work            # Go-Workspace (services/*)
├─ pnpm-workspace.yaml# pnpm-Workspace (apps/*, packages/*)
├─ turbo.json         # Turborepo-Task-Pipeline
└─ Makefile           # Convenience-Targets über den ganzen Stack
```

**Polyglottes Tooling:** Node/TypeScript via **pnpm + Turborepo**, Go via
**go.work**. Beide Welten leben nebeneinander und sind über den `Makefile`
und die npm-Scripts (`pnpm go:build`, `pnpm compose:up`, …) verbunden.

## Voraussetzungen

| Tool | Version | Zweck |
| --- | --- | --- |
| Node | ≥ 22 (`.nvmrc`) | Frontend + Tooling |
| pnpm | ≥ 10 | Workspace-Paketmanager |
| Go | ≥ 1.22 | Backend-Services |
| Docker | aktuell | lokale Infra (ClickHouse/Postgres/Collector) |

## Schnellstart

```bash
# 1) Abhängigkeiten (JS + Go) installieren
make install          # == pnpm install && go mod tidy (je Service)

# 2) Lokale Infra hochfahren (ClickHouse, Postgres, OTel-Collector)
make up

# 3a) Frontend-Dev-Server
make dev              # Next.js auf http://localhost:4173

# 3b) Backend-Services (je eigenes Terminal)
make dev-ingest       # OTLP-Ingestion  → :4318 (HTTP) / :4317 (gRPC, geplant)
make dev-query        # Query-API       → :7080
```

Weitere Targets: `make help`.

## Architektur in einem Satz

Telemetrie kommt per **OTLP** rein (Collector → `ingest`), landet OTel-nativ in
**ClickHouse** (Settings/Metadaten in **Postgres**), und wird über eine
**PromQL-Engine** (`query`) für alle Signale abgefragt — visualisiert in einer
**keyboard-first SPA**. Details, Begründungen und Roadmap: siehe interne Doku
(unten).

## Interne Doku (nicht Teil dieses Repos)

Die ausführliche Recherche- und Produkt-Doku (Dash0-Analyse, UI/UX-Prinzipien,
Feature-Katalog, Roadmap, Tech-Stack-Begründung) liegt **bewusst außerhalb dieses
Repositories** im Eltern-Ordner `../docs/` und wird **nicht** mit veröffentlicht.
Da `git` nur in diesem Unterordner initialisiert ist, kann diese Doku technisch
nicht in das Open-Source-Repo gelangen. Einstieg dort: `../docs/README.md`.

## Lizenz

[Apache License 2.0](./LICENSE) — passend zum OpenTelemetry-/ClickHouse-/Perses-Ökosystem.
