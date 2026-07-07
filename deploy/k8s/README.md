# Run the rocketplaneIO platform on Kubernetes (HA)

Manifests to run the **platform itself** (control plane + web UI) inside a
cluster, with every component at `replicas: 2`. This is how our own production
instance runs. For the *agent* that connects a cluster, use the install command
the UI generates; for the telemetry pipeline see `../telemetry/`.

## What you get

- **controlplane × 2** — safe with multiple replicas: events fan out through
  Postgres LISTEN/NOTIFY, and singleton work (alert evaluator, action reaper)
  runs behind a Postgres advisory lock on exactly one replica.
- **web × 2** — stateless Next.js.
- **Postgres** — either the bundled single-instance StatefulSet in
  `controlplane-stack.yaml` (simplest start), or the HA CloudNativePG cluster
  in `cnpg-postgres.yaml` (1 primary + 1 streaming replica, automatic
  failover; requires the [CNPG operator](https://cloudnative-pg.io)). For CNPG,
  point `DATABASE_URL` at `rocketplane-pg-rw:5432` and scale the bundled
  StatefulSet to 0.
- **Ingress** — the example uses the Tailscale ingress class; swap for your
  ingress of choice. The UI must be reached over HTTPS (session cookie).

## Install

1. Replace the placeholders in `controlplane-stack.yaml`:
   `__SESSION_SECRET__` (long random string), `__PG_PASS__`, the `RP_PUBLIC_URL`
   and ingress host, and the storage class. Same for `cnpg-postgres.yaml` if
   you use it.
2. `kubectl apply -f controlplane-stack.yaml` (and optionally
   `cnpg-postgres.yaml`).
3. Open the UI, create the first admin, connect clusters. The agent install
   command the UI generates already points at the in-cluster service
   (`RP_AGENT_CONTROLPLANE_URL`).

The agent itself is also HA-capable: `helm ... --set replicaCount=2` — a
Kubernetes Lease elects exactly one leader; standbys take over in ~10s.

Known single points (acceptable, documented): ClickHouse runs single-node
(telemetry has a 72h TTL; replication would need a Keeper +
ReplicatedMergeTree setup and is intentionally out of scope here).
