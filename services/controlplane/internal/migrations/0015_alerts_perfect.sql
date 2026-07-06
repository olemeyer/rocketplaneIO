-- Alerts-Vollausbau:
--   query                — PromQL-Bedingung direkt in der Rule (kind='promql')
--   muted_until          — Snooze: State-Machine läuft, Notifications pausieren
--   action_definition_id — Auto-Remediation: firing dispatcht diesen Workflow
--   action_args          — Argumente für den Workflow
ALTER TABLE alert_rules
  ADD COLUMN IF NOT EXISTS query text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS muted_until timestamptz,
  ADD COLUMN IF NOT EXISTS action_definition_id uuid REFERENCES action_definitions(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS action_args jsonb NOT NULL DEFAULT '{}';
