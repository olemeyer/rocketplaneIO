-- On-call escalation + incident notification routing (Round 3): an
-- escalation policy is an ordered chain of notification steps
-- (PagerDuty pattern without personal rotation) — each step fires after
-- `afterMinutes` (relative to the previous step, or to declaration) across a
-- set of existing alert providers (webhook/slack/email). If an incident stays
-- unacknowledged (status='open'), the escalator advances to the next step;
-- acknowledge/mitigate/resolve stops the escalation.

CREATE TABLE IF NOT EXISTS escalation_policies (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id     uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  name       text NOT NULL,
  -- steps: [{"afterMinutes": int, "providerIds": [uuid, …]}] in order.
  steps      jsonb NOT NULL DEFAULT '[]'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_escalation_policies_org ON escalation_policies(org_id, name);

-- Default policy per cluster: new incidents in this cluster inherit it at
-- declaration time. Kept in a separate table to avoid touching the
-- heavily used clusters query.
CREATE TABLE IF NOT EXISTS cluster_escalation (
  cluster_id uuid PRIMARY KEY REFERENCES clusters(id) ON DELETE CASCADE,
  policy_id  uuid REFERENCES escalation_policies(id) ON DELETE SET NULL
);

-- Escalation state on the incident: which policy, how many steps have fired,
-- when the next one is due (NULL = no escalation pending).
ALTER TABLE incidents
  ADD COLUMN IF NOT EXISTS escalation_policy_id uuid REFERENCES escalation_policies(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS escalation_step int NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS next_escalation_at timestamptz;

-- The escalator scans for due, open incidents.
CREATE INDEX IF NOT EXISTS idx_incidents_escalation_due
  ON incidents(next_escalation_at) WHERE next_escalation_at IS NOT NULL AND status = 'open';
