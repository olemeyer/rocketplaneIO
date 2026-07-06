-- Alerts: Provider (Versandkanäle, org-weit) + Rules (typed Checks je Cluster,
-- Dash0-Muster: Threshold + for-Dauer auf einer Metrik-Bedingung) + Events.
CREATE TABLE IF NOT EXISTS alert_providers (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id     uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  name       text NOT NULL,
  type       text NOT NULL, -- webhook | slack | email
  config     jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, name)
);

CREATE TABLE IF NOT EXISTS alert_rules (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  cluster_id   uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  name         text NOT NULL,
  kind         text NOT NULL, -- log_errors | trace_error_ratio | trace_p95_ms | node_cpu_pct | node_mem_pct | node_disk_pct | workload_unready
  params       jsonb NOT NULL DEFAULT '{}',
  op           text NOT NULL DEFAULT 'gt', -- gt | lt
  threshold    double precision NOT NULL,
  window_seconds int NOT NULL DEFAULT 300,
  for_seconds  int NOT NULL DEFAULT 60,
  severity     text NOT NULL DEFAULT 'warning', -- warning | critical
  provider_ids uuid[] NOT NULL DEFAULT '{}',
  enabled      boolean NOT NULL DEFAULT true,
  state        text NOT NULL DEFAULT 'ok', -- ok | pending | firing
  state_since  timestamptz NOT NULL DEFAULT now(),
  last_value   double precision NOT NULL DEFAULT 0,
  last_eval_at timestamptz,
  last_error   text NOT NULL DEFAULT '',
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (cluster_id, name)
);
CREATE INDEX IF NOT EXISTS idx_alert_rules_cluster ON alert_rules(cluster_id);

CREATE TABLE IF NOT EXISTS alert_events (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  rule_id    uuid NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
  cluster_id uuid NOT NULL,
  at         timestamptz NOT NULL DEFAULT now(),
  from_state text NOT NULL,
  to_state   text NOT NULL,
  value      double precision NOT NULL,
  message    text NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_alert_events_cluster ON alert_events(cluster_id, at DESC);
