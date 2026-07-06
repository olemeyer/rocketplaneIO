# Contributing to rocketplaneIO

Thanks for your interest! rocketplaneIO is in **alpha** and moving fast — the most valuable
contributions right now are:

- **Bug reports from real clusters** — kernel version, K8s distribution, what broke, and the
  agent/Beyla logs if you have them. [Open an issue](https://github.com/olemeyer/rocketplaneio/issues).
- **Feedback on the loop** — where the investigation flow (logs → trace → alert → action)
  loses you in practice.

## Dev setup

The [Quick Start](README.md#quick-start--from-source-alpha) in the README *is* the dev setup:
Go 1.25+, Node 22+ with pnpm, Docker. `docker compose -f deploy/compose/docker-compose.yml up -d`,
`go run ./services/controlplane/cmd/controlplane`, `pnpm install && pnpm dev`.

- Web typecheck: `pnpm typecheck`
- Go build: `cd services/controlplane && go build ./...`
- Migrations live in `services/controlplane/internal/migrations/` and apply at boot.

## Conventions

- Conventional commits (`feat:`, `fix:`, `docs:`, …), atomic — one concern per commit.
- Go: `gofmt`; TypeScript: the repo's ESLint/Prettier config.
- UI changes follow the RETICLE design guidelines — the short version: healthy is calm,
  only anomalies speak; status colors are green/gold/crimson with colorblind-safe glyphs
  (▲◆●○); numbers are monospaced and readable.

Expect APIs and schemas to change without notice while we're alpha. If you're planning
anything bigger than a fix, open an issue first so we don't waste your time.
