-- Derived Metrics (Better-Stack-Muster, typed statt Query-Sprache): eine
-- benannte Metrik aus vorhandenen Logs/Spans — Filter + Extraktion + Aggregation.
-- Zero-Instrumentation: kein App-Code, die Signale sind schon da.
CREATE TABLE IF NOT EXISTS metric_definitions (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  cluster_id  uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  name        text NOT NULL,
  description text NOT NULL DEFAULT '',
  source      text NOT NULL, -- logs | spans
  namespace   text NOT NULL DEFAULT '',
  workload    text NOT NULL DEFAULT '', -- logs: WorkloadName · spans: ServiceName
  search      text NOT NULL DEFAULT '', -- logs: Body-Substring · spans: SpanName-Substring
  value_mode  text NOT NULL DEFAULT 'count', -- count | regex (logs) | duration (spans)
  pattern     text NOT NULL DEFAULT '',      -- regex mit EINER capture group (value_mode=regex)
  agg         text NOT NULL DEFAULT 'rate',  -- rate (count) | avg|p50|p95|p99|max|sum (werte)
  unit        text NOT NULL DEFAULT '',
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (cluster_id, name)
);
CREATE INDEX IF NOT EXISTS idx_metric_defs_cluster ON metric_definitions(cluster_id);
