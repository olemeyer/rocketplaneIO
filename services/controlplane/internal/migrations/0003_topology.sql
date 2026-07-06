-- Topologie + Flows für die Service-Map. Knoten = workloads (Deployment/StatefulSet/
-- DaemonSet/…) und services; Kanten = beobachtete Pod-zu-Pod-Flows, aggregiert auf
-- Workload-Ebene. Alles je Cluster; der Agent liefert Full-Syncs.

-- ── Workloads (Map-Knoten: die eigentlichen „Services") ────────────────────
CREATE TABLE IF NOT EXISTS workloads (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  cluster_id    uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  namespace     text NOT NULL,
  name          text NOT NULL,
  kind          text NOT NULL DEFAULT 'Deployment', -- Deployment|StatefulSet|DaemonSet|ReplicaSet|Job|CronJob|Pod
  replicas_desired int NOT NULL DEFAULT 0,
  replicas_ready   int NOT NULL DEFAULT 0,
  labels        jsonb NOT NULL DEFAULT '{}',
  first_seen_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (cluster_id, namespace, kind, name)
);
CREATE INDEX IF NOT EXISTS idx_workloads_cluster ON workloads(cluster_id);

-- ── Pods (feinere Ebene; für IP→Workload-Mapping + Health) ─────────────────
CREATE TABLE IF NOT EXISTS pods (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  cluster_id    uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  namespace     text NOT NULL,
  name          text NOT NULL,
  ip            text NOT NULL DEFAULT '',
  node_name     text NOT NULL DEFAULT '',
  phase         text NOT NULL DEFAULT 'Running',
  ready         boolean NOT NULL DEFAULT false,
  restarts      int NOT NULL DEFAULT 0,
  workload_kind text NOT NULL DEFAULT '',
  workload_name text NOT NULL DEFAULT '',
  first_seen_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (cluster_id, namespace, name)
);
CREATE INDEX IF NOT EXISTS idx_pods_cluster ON pods(cluster_id);
CREATE INDEX IF NOT EXISTS idx_pods_ip ON pods(cluster_id, ip);

-- ── Services (K8s Services; für Namen/DNS + Map-Knoten-Klasse) ─────────────
CREATE TABLE IF NOT EXISTS k8s_services (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  cluster_id    uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  namespace     text NOT NULL,
  name          text NOT NULL,
  type          text NOT NULL DEFAULT 'ClusterIP',
  cluster_ip    text NOT NULL DEFAULT '',
  selector      jsonb NOT NULL DEFAULT '{}',
  first_seen_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (cluster_id, namespace, name)
);
CREATE INDEX IF NOT EXISTS idx_k8s_services_cluster ON k8s_services(cluster_id);

-- ── Flows (aggregierte Kanten der Service-Map) ─────────────────────────────
-- from/to sind Workload-Referenzen (namespace + kind + name). Der Agent liefert
-- je Sync-Fenster ein Snapshot der beobachteten Kanten; die Control-Plane merged.
CREATE TABLE IF NOT EXISTS flows (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  cluster_id     uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  from_namespace text NOT NULL,
  from_kind      text NOT NULL DEFAULT 'Deployment',
  from_name      text NOT NULL,
  to_namespace   text NOT NULL,
  to_kind        text NOT NULL DEFAULT 'Deployment',
  to_name        text NOT NULL,
  to_port        int  NOT NULL DEFAULT 0,
  conn_count     bigint NOT NULL DEFAULT 0,  -- beobachtete Verbindungen im Fenster
  last_seen_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (cluster_id, from_namespace, from_kind, from_name, to_namespace, to_kind, to_name, to_port)
);
CREATE INDEX IF NOT EXISTS idx_flows_cluster ON flows(cluster_id);
