-- K8s-Inventar je Cluster: eine Zeile pro Ressourcen-Kind, die Items als
-- kompaktes JSONB (generisches Format {namespace,name,createdAt,info{}}).
-- Vom Agenten alle 60s voll-gesynct (Upsert) — Basis der Resources-Seite und
-- des list_resources-Tools des Copilots.
CREATE TABLE IF NOT EXISTS cluster_inventory (
  cluster_id uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  kind       text NOT NULL,
  items      jsonb NOT NULL DEFAULT '[]'::jsonb,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (cluster_id, kind)
);
