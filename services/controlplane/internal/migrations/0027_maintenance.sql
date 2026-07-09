-- Maintenance windows (Round 5): planned suppression of alert notifications and
-- auto-incident declaration during known-noisy periods (deploys, upgrades,
-- migrations). While a window is active for a cluster, the alert evaluator still
-- tracks rule state and writes alert_events, but does NOT notify or auto-declare
-- incidents for the matching scope. A window is cluster-scoped and may optionally
-- be limited to a single namespace (empty = whole cluster).
CREATE TABLE IF NOT EXISTS maintenance_windows (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  cluster_id      uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  title           text NOT NULL,
  scope_namespace text NOT NULL DEFAULT '',   -- '' = whole cluster
  starts_at       timestamptz NOT NULL,
  ends_at         timestamptz NOT NULL,
  created_by      uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at      timestamptz NOT NULL DEFAULT now()
);
-- The hot path is "is this cluster in maintenance right now?" — index by cluster
-- and end time (active windows have ends_at in the future).
CREATE INDEX IF NOT EXISTS idx_maintenance_active
  ON maintenance_windows(cluster_id, ends_at);
