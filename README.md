<div align="center">

<h2>An AI SRE for your Kubernetes cluster —<br>that can actually fix things, safely.</h2>

<p><b>Self-hosted · Apache-2.0 · bring your own LLM · air-gapped capable</b></p>

![Status](https://img.shields.io/badge/status-alpha-e5484d)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)
![Go](https://img.shields.io/badge/go-1.25+-00ADD8)

[**Quick start**](#quick-start) · [**Is it safe?**](#is-it-safe) · [**Under the hood**](#under-the-hood)

</div>

<br>

<a href=".github/assets/demo.mp4">
  <img alt="rocketplaneIO walkthrough: connect a cluster and the live service map draws itself in, then the Copilot investigates a checkout latency spike across logs, traces and metrics and names the root cause" src=".github/assets/demo.gif" width="100%">
</a>

<br>

> **Alpha.** The full loop works end-to-end today, developed against minikube. APIs and schemas
> still change without notice — don't point it at production yet.

## Three things you don't get anywhere else

### 1 · An AI SRE with a safety catalog — not an AI with kubectl

The Copilot investigates on its own: eBPF traces, logs, PromQL metrics, the live service map and
the **full Kubernetes inventory** (Services, Ingress, ConfigMaps, policies — everything). But it
can only change the cluster through **~30 named, reversible action pipelines**. No shell, no
`kubectl`, no YAML it could hallucinate.

Every action is risk-graded — and *you* set the approval rule per level:

| Level | Examples | Default approval |
|---|---|---|
| ◎ read-only | debug bundle, rollout history, drain preview | runs automatically |
| ↺ reversible | scale up, restart, set image, config edits | one click |
| ◇ disruptive | evict pod, rollout undo, cleanup | one click |
| △ destructive | drain, **scale-to-0**, NoExecute taint | **type the target's name to arm** |

Risk is parameter-aware (`scale replicas=3` is reversible; `replicas=0` is destructive), scope is
enforced server-side (a namespace-scoped session cannot touch nodes or other namespaces), and
every level can be set to `auto`, `click`, `confirm` or `off`.

**The model proposes. Deterministic pipelines execute, verify at pod level, and roll back.**
The LLM never touches the cluster directly.

Bring any Anthropic- or OpenAI-compatible model — including a fully local, air-gapped one. Your
telemetry never leaves your infrastructure either way.

### 2 · Traces for services you never instrumented

One outbound-only agent plus an eBPF DaemonSet
([OTel eBPF Instrumentation](https://github.com/grafana/beyla), née Grafana Beyla). HTTP/gRPC
spans **with cross-service context propagation — including compiled Go binaries** — plus SQL,
Redis and Kafka client spans. No SDKs, no sidecars, no code changes.

<img alt="A real 500 investigated: the failure cascades across three services, correlated error logs on the right" src=".github/assets/shot-trace.png" width="100%">
<div align="center"><sub>A real 500 on <code>GET /checkout</code>: the failure cascades frontdoor → checkout → catalog,
the exact ERROR log lines are correlated on the right. <b>No SDK in any of these services.</b>
An ERROR log line is two clicks away from this view. PromQL runs on the real Prometheus engine, embedded, on ClickHouse.</sub></div>

### 3 · Every change proves itself — or undoes itself

Actions aren't fire-and-forget `kubectl` calls. Each one is a pipeline —
**trigger → observe → verify** — that only reports success when the cluster actually converged:
old pods gone, new pods Ready, stable. Cancel, timeout or a failed verify triggers automatic
rollback from a snapshot taken *before* the mutation.

<img alt="The Runs audit trail: every execution as an expandable row with its full pipeline, and a one-click revert" src=".github/assets/shot-runs.png" width="100%">
<div align="center"><sub><b>Runs</b> — the audit trail. Who ran what, the full step timeline, and
<b>↺ revert</b>: one click re-applies the exact state captured before the change. Cancel always
terminates — a reaper finalizes anything a dead agent leaves behind, and force-cancel never waits.</sub></div>

## Quick start

The whole platform runs from published images ([ghcr.io](https://github.com/olemeyer?tab=packages&repo_name=rocketplaneIO)).
You need Docker and a Kubernetes cluster to point it at (minikube is fine).

**1 — run the platform**

```bash
curl -O https://raw.githubusercontent.com/olemeyer/rocketplaneIO/main/deploy/compose/docker-compose.prod.yml
curl -o .env https://raw.githubusercontent.com/olemeyer/rocketplaneIO/main/deploy/compose/.env.example

# REQUIRED: a real session secret — the control plane refuses to start without one.
echo "RP_SESSION_SECRET=$(openssl rand -hex 32)" >> .env

docker compose --env-file .env -f docker-compose.prod.yml up -d
```

The UI comes up on **http://localhost:4173**, the control plane on `:8090`. It runs
in production mode by default (no anonymous login) — you create the first account in
step 2. For a throwaway localhost trial you can add `RP_ENV=dev` to `.env` to skip
account setup.

**2 — connect your cluster**

Open the UI, create the owner account, hit **Connect cluster** — it hands you one copy-paste
command that installs the agent and the Beyla DaemonSet (a rendered `kubectl apply`, or Helm).
When the service map draws your namespaces and spans appear under Traces, you're live — without
touching a line of your code.

> Local minikube note: your cluster reaches the control plane at `http://host.minikube.internal:8090`,
> not `localhost` — set `RP_AGENT_CONTROLPLANE_URL` to that in `.env` before connecting.

**3 — turn on the Copilot**

Open it from the top bar and connect any Anthropic- or OpenAI-compatible provider (including a
local one). The key stays on your instance; requests go straight from your control plane to the
provider you chose.

<sub>Images ship as pinned releases (see <code>RP_VERSION</code> in <code>.env</code>);
<code>edge</code> tracks <code>main</code> for the latest unreleased changes. A platform Helm
chart is the next milestone. Want a demo workload? A Python + Redis shop behind
nginx — the one in every screenshot here — ships in
<a href="deploy/dev/">deploy/dev/</a> (<code>kubectl apply -f deploy/dev/shop-realistic.yaml -f deploy/dev/frontdoor.yaml</code>).</sub>

## Is it safe?

The section every platform team reads first:

- **Outbound-only.** The agent dials out over HTTPS; nothing connects into your cluster, nothing
  listens. Actions are *claimed* by the agent — never pushed in.
- **Auth.** In production the dev-login bypass is off: the first account is created at `/setup`, or
  you wire up Google SSO. Sessions are HMAC-signed with `RP_SESSION_SECRET`, and the control plane
  *refuses to start* with a missing or weak one — no forgeable cookies, no shipped default.
- **Network / TLS.** The control plane speaks plain HTTP on `:8090` — put it behind a TLS
  reverse proxy (or the platform Helm chart's ingress) for anything past `localhost`. The OTLP
  ingest ports `4317`/`4318` are unauthenticated by design; keep them on a trusted network
  (in-cluster or behind the proxy), not on the public internet.
- **Enumerated RBAC, two blocks** ([`deploy/install.yaml`](deploy/install.yaml)): *observe* is
  read-only; *act* holds exactly the write verbs the action catalog needs. Delete the act block
  (or set `rbac.actions=false` in Helm) for a strictly observe-only agent. No wildcards, no
  cluster-admin.
- **Secrets:** the inventory shows names, types and key counts — never values. That restraint is
  enforced in agent code; if you'd rather enforce it with RBAC too, delete the one `secrets` rule
  and the agent degrades gracefully.
- **The LLM is caged.** It reads through the same authenticated APIs you use, and mutates only
  via the whitelisted, validated, risk-gated action catalog. Every proposal, approval and result
  lands in the audit trail.
- **eBPF:** Beyla runs as a privileged DaemonSet (kernel ≥ 5.8 with BTF). The capture layer is
  upstream OpenTelemetry tooling — rocketplaneIO is the investigation-and-action loop on top.
- **Footprint** (measured on single-node minikube under continuous synthetic load — indicative,
  not a production benchmark): the rocketplaneIO **agent** is one lightweight pod at **~2m CPU
  and ~10Mi memory**, cluster-wide. **Beyla** is the real cost and scales with request volume —
  **~0.02 core idle, spiking to ~0.25 core under load, ~280Mi memory per node** (bounded; its
  default limit is 512Mi). One agent for the cluster, one Beyla per node.

## What else is in the box

<details>
<summary><b>Live service map · alerts with auto-remediation · PromQL on ClickHouse · full K8s inventory · Starlark workflows · more screens</b></summary>
<br>

<img alt="Live service map with automatic technology detection" src=".github/assets/shot-servicemap.png" width="100%">
<sub><b>Service map</b> — topology from real eBPF traffic flows; tech logos matched from container
images; live-updating.</sub>

<br><br>

<img alt="Alert rule firing and dispatching an auto-remediation workflow" src=".github/assets/shot-alerts.png" width="100%">
<sub><b>Alerts</b> — typed checks or PromQL conditions with <code>for</code>-durations and
sparklines. A firing rule can dispatch a remediation workflow: once per transition, fully audited,
still subject to verify-or-rollback.</sub>

<br><br>

<img alt="PromQL editor on ClickHouse" src=".github/assets/shot-promql.png" width="100%">
<sub><b>PromQL</b> — the actual Prometheus evaluation engine, embedded and pointed at ClickHouse
(<a href="services/controlplane/internal/promqlx">internal/promqlx</a>); editor built on
codemirror-promql. Custom metrics are named PromQL expressions, validated at save.</sub>

<br><br>

<img alt="Full Kubernetes inventory, tabbed by resource kind" src=".github/assets/shot-resources.png" width="100%">
<sub><b>Resources</b> — the complete cluster inventory (Services, Ingress, ConfigMaps, batch,
policies, volumes, quotas), synced every 60s. The same data the Copilot reads via
<code>list_resources</code>.</sub>

<br><br>

<img alt="Actions catalog, grouped by category with risk levels" src=".github/assets/shot-actions.png" width="100%">
<sub><b>Actions</b> — the catalog, searchable and risk-graded. Every built-in is also readable,
forkable <b>Starlark</b>: automate what an operator does by hand, with typed parameters that
render as forms. Deterministic, compiled at save.</sub>

<br><br>

<img alt="Log stream with severity histogram" src=".github/assets/shot-logs.png" width="100%">
<sub><b>Logs</b> — severity histogram, brushing, and the two-click path to the distributed
trace.</sub>

<br><br>

<img alt="Node infrastructure with kubelet stats" src=".github/assets/shot-nodes.png" width="100%">
<sub><b>Nodes</b> — kubelet-level stats; cordon/drain are verified actions, with a read-only
<code>drain preview</code> that shows the blast radius first.</sub>

<br><br>

<sub>The UI follows a strict instrument-panel design system (RETICLE) — healthy is calm, only
anomalies speak.</sub>
</details>

## Under the hood

```
 YOUR CLUSTER   agent (Go, outbound-only, enumerated RBAC)      beyla eBPF DaemonSet
                topology · logs · inventory · action pipelines   HTTP/gRPC/Go · SQL/Redis/Kafka
                        │ HTTPS                                          │ OTLP
                        ▼                                                ▼
 CONTROL PLANE  control plane (Go, single binary)                OTel collector → ClickHouse
                API · auth/orgs · alert evaluator · action queue · Copilot loop
                embedded Prometheus engine (PromQL → ClickHouse) · SSE broker
                        │
 DATA           PostgreSQL (state, rules, runs, chats)  ·  ClickHouse (logs, traces, metrics)
 ACCESS         web (Next.js) — service map · copilot · logs · traces · metrics · alerts · actions · runs
```

The Copilot is a tool-calling loop inside the control plane: 16 read tools (logs, traces —
including single-trace waterfalls and trace↔log correlation — PromQL, service map, inventory,
debug bundles) plus exactly one mutating tool, `run_safe_action`, which pauses the stream for
human approval. The same toolbox is exposed as an [MCP server](services/controlplane/cmd/mcp),
so Claude Code or Cursor can operate your cluster through the identical guardrails.

<details>
<summary><b>Monorepo layout</b></summary>

```
├── agent/                      # in-cluster agent (Go): sync, logs, inventory, action pipelines + revert snapshots
├── services/controlplane/      # control plane (Go): API, auth, alerts, Copilot loop, PromQL engine
│   └── internal/
│       ├── api/                #   REST + SSE + copilot_* (loop, guardrails, approval gate)
│       ├── promqlx/            #   embedded Prometheus engine on ClickHouse
│       ├── alerts/ · telemetry/ · events/ · store/ · migrations/
│       └── ../cmd/mcp/         #   MCP server — same tools for external agents
├── apps/web/                   # Next.js UI (RETICLE design system)
└── deploy/                     # compose (platform + dev stores), helm chart, install.yaml, demo shop
```
</details>

<details>
<summary><b>Develop from source</b></summary>
<br>

Prereqs: Go 1.25+, Node 22+ with pnpm, Docker.

```bash
git clone https://github.com/olemeyer/rocketplaneIO && cd rocketplaneIO

docker compose -f deploy/compose/docker-compose.yml up -d          # dev data stores + collector
go run ./services/controlplane/cmd/controlplane                    # control plane on :8090
cd apps/web && pnpm install && pnpm dev                            # UI on :4173
```

Building the platform images yourself (what CI publishes):

```bash
docker build -t rocketplaneio/controlplane -f services/controlplane/Dockerfile .
docker build -t rocketplaneio/web          -f apps/web/Dockerfile .
docker build -t rocketplaneio/agent        -f agent/Dockerfile .
```
</details>

## Status

| Works end-to-end today | On the roadmap |
|---|---|
| Copilot: BYO-LLM investigation → guardrailed fixes | Tagged releases + platform Helm chart |
| eBPF traces incl. compiled Go, log→trace correlation | Hosted demo |
| ~30 safe actions with verify, rollback and revert | Production-scale overhead benchmarks |
| Runs audit trail, guaranteed-terminating cancel | Multi-user RBAC hardening |
| Alerts with auto-remediation dispatch | Server-side LLM key vault |
| PromQL + custom metrics on ClickHouse, full K8s inventory | |
| Published multi-arch images (`edge`) + agent install.yaml | |

## Contributing

Reports from real clusters are the contribution we want most —
[open an issue](https://github.com/olemeyer/rocketplaneIO/issues). See
[CONTRIBUTING.md](CONTRIBUTING.md). If this resonates, a star helps others find it.

## License

[Apache-2.0](LICENSE).

---

<div align="center">
<sub>Built with Go, Next.js, eBPF and ClickHouse · rocketplaneIO</sub>
</div>
