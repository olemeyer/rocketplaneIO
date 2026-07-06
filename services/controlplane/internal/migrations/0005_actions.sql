-- Safe-Actions: vom User angeforderte Kubernetes-Aktionen auf Workloads.
-- Der Agent (outbound-only) POLLT pending-Aktionen, führt sie im Cluster aus
-- und meldet das Ergebnis zurück — die Control-Plane hat NIE Cluster-Zugriff.
CREATE TABLE IF NOT EXISTS cluster_actions (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  cluster_id       uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  requested_by     uuid REFERENCES users(id) ON DELETE SET NULL,
  kind             text NOT NULL, -- rollout_restart | scale | delete_pod
  target_namespace text NOT NULL,
  target_kind      text NOT NULL, -- Deployment | StatefulSet | DaemonSet | Pod
  target_name      text NOT NULL,
  params           jsonb NOT NULL DEFAULT '{}', -- scale: {"replicas": n}
  status           text NOT NULL DEFAULT 'pending', -- pending|running|succeeded|failed
  result           text NOT NULL DEFAULT '',
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_cluster_actions_pending
  ON cluster_actions(cluster_id, status) WHERE status IN ('pending', 'running');
CREATE INDEX IF NOT EXISTS idx_cluster_actions_recent
  ON cluster_actions(cluster_id, created_at DESC);
