-- On-Call-Eskalation + Incident-Notification-Routing (Round 3): eine
-- Eskalations-Policy ist eine geordnete Kette von Notification-Schritten
-- (PagerDuty-Muster ohne persönliche Rotation) — jeder Schritt feuert nach
-- `afterMinutes` (relativ zum vorherigen Schritt bzw. zur Deklaration) über eine
-- Menge bestehender Alert-Provider (webhook/slack/email). Bleibt ein Incident
-- unbestätigt (status='open'), wandert der Escalator zum nächsten Schritt;
-- acknowledge/mitigate/resolve stoppt die Eskalation.

CREATE TABLE IF NOT EXISTS escalation_policies (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id     uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  name       text NOT NULL,
  -- steps: [{"afterMinutes": int, "providerIds": [uuid, …]}] in Reihenfolge.
  steps      jsonb NOT NULL DEFAULT '[]'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_escalation_policies_org ON escalation_policies(org_id, name);

-- Default-Policy je Cluster: neue Incidents dieses Clusters übernehmen sie beim
-- Deklarieren. Separate Tabelle, um die vielfach genutzte clusters-Query nicht
-- anzufassen.
CREATE TABLE IF NOT EXISTS cluster_escalation (
  cluster_id uuid PRIMARY KEY REFERENCES clusters(id) ON DELETE CASCADE,
  policy_id  uuid REFERENCES escalation_policies(id) ON DELETE SET NULL
);

-- Eskalationszustand am Incident: welche Policy, wie viele Schritte gefeuert,
-- wann der nächste fällig ist (NULL = keine Eskalation ausstehend).
ALTER TABLE incidents
  ADD COLUMN IF NOT EXISTS escalation_policy_id uuid REFERENCES escalation_policies(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS escalation_step int NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS next_escalation_at timestamptz;

-- Der Escalator scannt fällige, offene Incidents.
CREATE INDEX IF NOT EXISTS idx_incidents_escalation_due
  ON incidents(next_escalation_at) WHERE next_escalation_at IS NOT NULL AND status = 'open';
