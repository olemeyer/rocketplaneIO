# Telemetry pipeline (ClickHouse + OTel Collector + Beyla)

Deploys the full zero-instrumentation telemetry pipeline into the `rocketplane`
namespace. This is what turns the service map, traces, logs and RED metrics on.

```
Beyla (eBPF DaemonSet, every node)
  ├─ L7 traces (HTTP/gRPC/SQL)  ─┐
  └─ L4 network flows            ├─→ OTel Collector ─→ ClickHouse ─→ control plane
                                 ┘
```

## Install

1. `clickhouse.yaml` — replace `__CH_PASS__` with a random secret and set your
   storage class, then apply.
2. `otel-collector.yaml` — apply as-is. The `filter/noise` processor drops
   Beyla's synthetic `in queue`/`processing` spans and *successful* health-check
   spans (~30% raw span volume); failing health checks (>=500) are kept. The
   collector has **no logs pipeline on purpose** — the control plane owns the
   `otel_logs` schema and fills it via the agent's log collector.
3. `beyla.yaml` — set `BEYLA_KUBE_CLUSTER_NAME`, then apply.
4. Wire the control plane to ClickHouse (`CLICKHOUSE_URL/USER/PASSWORD/DB`).

## Hard-won settings (do not change casually)

- `BEYLA_BPF_CONTEXT_PROPAGATION=headers` — **never** `all`: its IP/connection
  heuristic merges unrelated keep-alive requests into one trace.
- `BEYLA_SKIP_GO_SPECIFIC_TRACERS=true` — Go apps using pgx/clickhouse-go do
  their I/O outside `database/sql`, so Go uprobes miss them entirely (zero
  client spans). Generic kprobes capture the Postgres protocol for Go too.
- `hostNetwork: true` on Beyla — required for the L4 network-flow metrics; in a
  pod netns Beyla only sees its own traffic. Standard for network monitors.
- No `application_service_graph` metrics feature — it crashes the ClickHouse
  exporter (v0.116) and the map derives edges from traces + flows instead.
- Beyla memory limit 1Gi — 512Mi OOMs under real load.
