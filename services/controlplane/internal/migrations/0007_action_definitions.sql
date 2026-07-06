-- Action-Definitionen: wiederverwendbare, org-weite Problemlösungs-Workflows
-- in Starlark (hermetischer Python-Dialekt — sandboxed, deterministisch, keine
-- Endlosschleifen). Die Built-ins (rollout restart / scale / recreate pod)
-- leben im Agenten; hier liegen die CUSTOM-Workflows. Beim Dispatch wird die
-- source in die cluster_action gesnapshottet (Audit: was lief, bleibt exakt).
CREATE TABLE IF NOT EXISTS action_definitions (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id      uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  name        text NOT NULL,
  description text NOT NULL DEFAULT '',
  -- Eingabe-Parameter: [{name, label, type: string|int, default}]
  params      jsonb NOT NULL DEFAULT '[]',
  source      text NOT NULL,
  created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, name)
);
CREATE INDEX IF NOT EXISTS idx_action_definitions_org ON action_definitions(org_id);
