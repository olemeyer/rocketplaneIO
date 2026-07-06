-- Dashboards als Code auf dem OFFENEN STANDARD (Perses, CNCF — wie Dash0).
-- Die Quelle der Wahrheit ist eine Perses-kompatible YAML-Spec (kind: Dashboard ·
-- metadata · spec.panels · layouts · PrometheusTimeSeriesQuery). Org-weit, damit
-- ein Dashboard einmal geschrieben und über Cluster hinweg genutzt werden kann —
-- versionierbar, teilbar, provisionierbar (späterer Operator/Terraform-Pfad).
CREATE TABLE IF NOT EXISTS dashboards (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id      uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  name        text NOT NULL,
  description text NOT NULL DEFAULT '',
  -- Perses-YAML (der komplette Dashboard-Spec, roh gespeichert = 1:1 portabel).
  spec        text NOT NULL,
  created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, name)
);
CREATE INDEX IF NOT EXISTS idx_dashboards_org ON dashboards(org_id);
