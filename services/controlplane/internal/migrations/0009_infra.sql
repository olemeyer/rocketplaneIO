-- Infrastruktur: Nodes (Kapazität + kubelet-Auslastung) und PVCs (inkl.
-- echter Volume-Belegung + Mounts). Full-Sync vom Agenten, Teil des
-- Topologie-Pushes — dasselbe SSE-Signal hält den Infrastructure-Bereich live.
CREATE TABLE IF NOT EXISTS nodes (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  cluster_id       uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  name             text NOT NULL,
  role             text NOT NULL DEFAULT 'worker',
  kubelet_version  text NOT NULL DEFAULT '',
  os_image         text NOT NULL DEFAULT '',
  arch             text NOT NULL DEFAULT '',
  internal_ip      text NOT NULL DEFAULT '',
  ready            boolean NOT NULL DEFAULT true,
  pressure         text NOT NULL DEFAULT '',
  cpu_capacity_m   bigint NOT NULL DEFAULT 0,
  cpu_allocatable_m bigint NOT NULL DEFAULT 0,
  mem_capacity     bigint NOT NULL DEFAULT 0,
  mem_allocatable  bigint NOT NULL DEFAULT 0,
  pod_capacity     bigint NOT NULL DEFAULT 0,
  cpu_usage_m      bigint NOT NULL DEFAULT -1,
  mem_usage        bigint NOT NULL DEFAULT -1,
  fs_used          bigint NOT NULL DEFAULT -1,
  fs_capacity      bigint NOT NULL DEFAULT -1,
  image_fs_used    bigint NOT NULL DEFAULT -1,
  last_seen_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (cluster_id, name)
);
CREATE INDEX IF NOT EXISTS idx_nodes_cluster ON nodes(cluster_id);

CREATE TABLE IF NOT EXISTS pvcs (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  cluster_id      uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  namespace       text NOT NULL,
  name            text NOT NULL,
  phase           text NOT NULL DEFAULT 'Pending',
  storage_class   text NOT NULL DEFAULT '',
  access_modes    text[] NOT NULL DEFAULT '{}',
  volume_name     text NOT NULL DEFAULT '',
  requested_bytes bigint NOT NULL DEFAULT 0,
  capacity_bytes  bigint NOT NULL DEFAULT 0,
  used_bytes      bigint NOT NULL DEFAULT -1,
  mounted_by      text[] NOT NULL DEFAULT '{}',
  last_seen_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (cluster_id, namespace, name)
);
CREATE INDEX IF NOT EXISTS idx_pvcs_cluster ON pvcs(cluster_id);
